package authapp_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jroedel/uebung/app/authapp"
	"github.com/jroedel/uebung/business/domain/identity/identitybus"
	"github.com/jroedel/uebung/business/domain/identity/nicknamer"
	"github.com/jroedel/uebung/business/domain/identity/stores/memdb"
	"github.com/jroedel/uebung/business/types/email"
	"github.com/jroedel/uebung/business/types/nickname"
	"github.com/jroedel/uebung/business/types/userid"
)

type captureMailer struct{ links []string }

func (m *captureMailer) SendLoginLink(_ context.Context, _ email.Email, link string) error {
	m.links = append(m.links, link)

	return nil
}

type fixture struct {
	h      http.Handler
	mailer *captureMailer
}

func newFixture(t *testing.T, secureCookies bool) *fixture {
	t.Helper()

	mailer := &captureMailer{}

	// The real generator, not a stub: these tests exercise the HTTP surface end
	// to end, and a suggestion that failed to parse would be a bug worth
	// catching here as much as anywhere.
	namer, err := nicknamer.New(nicknamer.Config{})
	if err != nil {
		t.Fatalf("nicknamer.New: %v", err)
	}

	var n int
	identity, err := identitybus.NewBusiness(identitybus.Config{
		Storer:   memdb.New(),
		Mailer:   mailer,
		Namer:    namer,
		LinkBase: "https://uebung.club/auth/callback",
		Now:      time.Now,
		NewID: func() (userid.UserID, error) {
			n++

			return userid.Parse("user-" + string(rune('a'+n-1)))
		},
	})
	if err != nil {
		t.Fatalf("NewBusiness: %v", err)
	}

	app := authapp.New(authapp.Config{
		Identity:      identity,
		Log:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:           time.Now,
		SecureCookies: secureCookies,
	})

	return &fixture{h: app.Handler(), mailer: mailer}
}

// token asks for a link and returns the raw token from it.
func (f *fixture) token(t *testing.T, addr string) string {
	t.Helper()

	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, jsonReq(http.MethodPost, "/auth/request", `{"email":"`+addr+`"}`))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("/auth/request status = %d, want 202", rec.Code)
	}
	if len(f.mailer.links) == 0 {
		t.Fatal("no link was sent")
	}

	_, tok, _ := strings.Cut(f.mailer.links[len(f.mailer.links)-1], "?token=")
	if tok == "" {
		t.Fatal("link carries no token")
	}

	return tok
}

func jsonReq(method, target, body string) *http.Request {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")

	return r
}

func cookie(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}

	return nil
}

// A magic link must NOT sign anyone in on GET.
//
// Following a link is something an attacker can make a browser do, and signup is
// open, so an attacker can hold a valid token for their own account and steer a
// victim onto it. Redeeming on GET would silently sign the victim in as the
// attacker and write everything they study into the attacker's deck.
func TestGetCallbackDoesNotSignIn(t *testing.T) {
	f := newFixture(t, true)
	tok := f.token(t, "learner@example.com")

	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/auth/callback?token="+tok, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (a confirmation page)", rec.Code)
	}
	if c := cookie(rec, authapp.SessionCookieName); c != nil {
		t.Fatal("GET /auth/callback issued a session cookie")
	}

	body := rec.Body.String()
	if !strings.Contains(body, "learner@example.com") {
		t.Error("the confirmation page does not show which address it would sign in as")
	}
	if !strings.Contains(body, `method="POST"`) {
		t.Error("the confirmation page has no POST form")
	}

	// And the token must still be spendable afterwards: merely looking at the
	// page cannot consume someone's only link.
	rec2 := httptest.NewRecorder()
	f.h.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/auth/callback?token="+tok, nil))
	if rec2.Code != http.StatusOK {
		t.Fatalf("second GET status = %d: the peek consumed the token", rec2.Code)
	}
}

// The POST must be same-site. Without the Strict-cookie nonce, an auto-submitting
// cross-site form would be able to stand in for the person and reinstate the
// whole login-CSRF problem the interstitial exists to stop.
func TestPostCallbackRequiresTheConfirmationNonce(t *testing.T) {
	f := newFixture(t, true)
	tok := f.token(t, "learner@example.com")

	tests := map[string]struct {
		form   url.Values
		cookie *http.Cookie
	}{
		"no nonce at all":  {url.Values{"token": {tok}}, nil},
		"nonce, no cookie": {url.Values{"token": {tok}, "nonce": {"abc"}}, nil},
		"mismatched nonce": {url.Values{"token": {tok}, "nonce": {"abc"}},
			&http.Cookie{Name: "__Host-uebung_confirm", Value: "different"}},
		"empty nonce, empty cookie": {url.Values{"token": {tok}, "nonce": {""}},
			&http.Cookie{Name: "__Host-uebung_confirm", Value: ""}},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/auth/callback", strings.NewReader(tc.form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if tc.cookie != nil {
				r.AddCookie(tc.cookie)
			}

			rec := httptest.NewRecorder()
			f.h.ServeHTTP(rec, r)

			if c := cookie(rec, authapp.SessionCookieName); c != nil {
				t.Fatal("a session was issued without a matching confirmation nonce")
			}
			if rec.Code != http.StatusSeeOther {
				t.Fatalf("status = %d, want 303", rec.Code)
			}
		})
	}
}

// The whole flow, the way a browser walks it.
func TestConfirmThenPostSignsIn(t *testing.T) {
	f := newFixture(t, true)
	tok := f.token(t, "learner@example.com")

	// GET the confirmation, keep its nonce cookie and the nonce in the form.
	get := httptest.NewRecorder()
	f.h.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/auth/callback?token="+tok, nil))

	confirm := cookie(get, "__Host-uebung_confirm")
	if confirm == nil {
		t.Fatal("no confirmation cookie was set")
	}
	if confirm.SameSite != http.SameSiteStrictMode {
		t.Errorf("confirmation cookie SameSite = %v, want Strict: Strict is what blocks a cross-site POST", confirm.SameSite)
	}
	if !confirm.HttpOnly || !confirm.Secure {
		t.Errorf("confirmation cookie should be HttpOnly and Secure, got %+v", confirm)
	}

	nonce := regexp.MustCompile(`name="nonce" value="([^"]+)"`).FindStringSubmatch(get.Body.String())
	if nonce == nil {
		t.Fatal("no nonce in the confirmation form")
	}

	post := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/auth/callback",
		strings.NewReader(url.Values{"token": {tok}, "nonce": {nonce[1]}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(confirm)
	f.h.ServeHTTP(post, r)

	if post.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body=%s", post.Code, post.Body)
	}

	session := cookie(post, authapp.SessionCookieName)
	if session == nil {
		t.Fatal("no session cookie after confirming")
	}
	if !session.Secure || !session.HttpOnly || session.SameSite != http.SameSiteLaxMode {
		t.Errorf("session cookie = %+v; want Secure, HttpOnly, SameSite=Lax", session)
	}

	// And it authenticates.
	me := httptest.NewRecorder()
	meReq := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	meReq.AddCookie(session)
	f.h.ServeHTTP(me, meReq)

	if me.Code != http.StatusOK || !strings.Contains(me.Body.String(), "learner@example.com") {
		t.Fatalf("/auth/me = %d %s", me.Code, me.Body)
	}
}

// Cookie security must be a property of the deployment, fixed at startup, and
// must NOT be inferable from a request header.
//
// This is the regression that mattered: when Secure and the __Host- prefix were
// derived per request from X-Forwarded-Proto, a deployment behind Apache — where
// r.TLS is nil and mod_proxy_http sends no X-Forwarded-Proto — issued 90-day
// session cookies with neither protection, silently, because the site still
// worked.
func TestCookieSecurityIsFixedAtStartupNotSniffedPerRequest(t *testing.T) {
	signIn := func(t *testing.T, f *fixture, mutate func(*http.Request)) *http.Cookie {
		t.Helper()

		tok := f.token(t, "learner@example.com")

		get := httptest.NewRecorder()
		gr := httptest.NewRequest(http.MethodGet, "/auth/callback?token="+tok, nil)
		mutate(gr)
		f.h.ServeHTTP(get, gr)

		nonce := regexp.MustCompile(`name="nonce" value="([^"]+)"`).FindStringSubmatch(get.Body.String())
		if nonce == nil {
			t.Fatal("no nonce in the confirmation form")
		}
		var confirm *http.Cookie
		for _, c := range get.Result().Cookies() {
			if strings.HasSuffix(c.Name, "uebung_confirm") {
				confirm = c
			}
		}
		if confirm == nil {
			t.Fatal("no confirmation cookie")
		}

		post := httptest.NewRecorder()
		pr := httptest.NewRequest(http.MethodPost, "/auth/callback",
			strings.NewReader(url.Values{"token": {tok}, "nonce": {nonce[1]}}.Encode()))
		pr.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		pr.AddCookie(confirm)
		mutate(pr)
		f.h.ServeHTTP(post, pr)

		for _, c := range post.Result().Cookies() {
			if strings.HasSuffix(c.Name, "uebung_session") {
				return c
			}
		}
		t.Fatalf("no session cookie; status=%d", post.Code)

		return nil
	}

	// A secure deployment stays secure even though r.TLS is nil on every request
	// and no forwarded header is present — which is exactly the Apache case.
	t.Run("secure deployment, no forwarded header", func(t *testing.T) {
		c := signIn(t, newFixture(t, true), func(*http.Request) {})

		if !c.Secure {
			t.Error("session cookie lacks Secure in a secure deployment")
		}
		if c.Name != authapp.SessionCookieName {
			t.Errorf("cookie name = %q, want the __Host- prefixed %q", c.Name, authapp.SessionCookieName)
		}
	})

	// A client claiming plain HTTP must not be able to talk the server out of
	// Secure or out of the __Host- prefix.
	t.Run("secure deployment, client claims http", func(t *testing.T) {
		c := signIn(t, newFixture(t, true), func(r *http.Request) {
			r.Header.Set("X-Forwarded-Proto", "http")
		})

		if !c.Secure || c.Name != authapp.SessionCookieName {
			t.Errorf("a spoofed X-Forwarded-Proto downgraded the cookie: %+v", c)
		}
	})

	// And an insecure (localhost) deployment must not be talked INTO the __Host-
	// prefix, which browsers reject without Secure.
	t.Run("insecure deployment, client claims https", func(t *testing.T) {
		c := signIn(t, newFixture(t, false), func(r *http.Request) {
			r.Header.Set("X-Forwarded-Proto", "https")
		})

		if c.Secure {
			t.Error("insecure deployment issued a Secure cookie")
		}
		if strings.HasPrefix(c.Name, "__Host-") {
			t.Errorf("cookie name = %q: __Host- without Secure is rejected by browsers", c.Name)
		}
	})
}

// Requesting a link must look identical whatever the address, so the endpoint
// cannot be asked who has an account.
func TestRequestLoginDoesNotRevealWhetherAnAccountExists(t *testing.T) {
	f := newFixture(t, true)

	// Create one account.
	f.token(t, "known@example.com")

	var bodies []string
	for _, addr := range []string{"known@example.com", "stranger@example.com"} {
		rec := httptest.NewRecorder()
		f.h.ServeHTTP(rec, jsonReq(http.MethodPost, "/auth/request", `{"email":"`+addr+`"}`))

		if rec.Code != http.StatusAccepted {
			t.Fatalf("%s: status = %d, want 202", addr, rec.Code)
		}
		bodies = append(bodies, rec.Body.String())
	}

	if bodies[0] != bodies[1] {
		t.Fatalf("responses differ for known and unknown addresses:\n known:   %s unknown: %s", bodies[0], bodies[1])
	}
}

func TestMeAndLogout(t *testing.T) {
	f := newFixture(t, true)

	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/auth/me", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("/auth/me unauthenticated = %d, want 401", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store: auth responses must not be cached", got)
	}
}

// --- nicknames --------------------------------------------------------------

// signIn runs the whole confirm-then-post flow and returns the session cookie.
func (f *fixture) signIn(t *testing.T, addr string) *http.Cookie {
	t.Helper()

	tok := f.token(t, addr)

	get := httptest.NewRecorder()
	f.h.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/auth/callback?token="+tok, nil))

	confirm := cookie(get, "__Host-uebung_confirm")
	if confirm == nil {
		t.Fatal("no confirmation cookie was set")
	}

	nonce := regexp.MustCompile(`name="nonce" value="([^"]+)"`).FindStringSubmatch(get.Body.String())
	if nonce == nil {
		t.Fatal("no nonce in the confirmation form")
	}

	post := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/auth/callback",
		strings.NewReader(url.Values{"token": {tok}, "nonce": {nonce[1]}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(confirm)
	f.h.ServeHTTP(post, r)

	session := cookie(post, authapp.SessionCookieName)
	if session == nil {
		t.Fatalf("no session cookie after confirming; status = %d", post.Code)
	}

	return session
}

// as issues a request carrying a session cookie.
func (f *fixture) as(t *testing.T, session *http.Cookie, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()

	r := jsonReq(method, target, body)
	r.AddCookie(session)

	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, r)

	return rec
}

// me decodes /auth/me for a session.
func (f *fixture) me(t *testing.T, session *http.Cookie) map[string]any {
	t.Helper()

	rec := f.as(t, session, http.MethodGet, "/auth/me", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("/auth/me status = %d, want 200; body=%s", rec.Code, rec.Body)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding /auth/me: %v", err)
	}

	return body
}

// A learner who has just signed in for the first time has no name, and gets a
// suggestion to put on the skip button — both in the one response the client
// already makes on load.
func TestMeOffersASuggestionUntilANameIsChosen(t *testing.T) {
	f := newFixture(t, true)
	session := f.signIn(t, "learner@example.com")

	body := f.me(t, session)

	if got := body["nickname"]; got != "" {
		t.Errorf("nickname = %v, want empty for a fresh account", got)
	}

	suggestion, _ := body["suggestion"].(string)
	if suggestion == "" {
		t.Fatal("no suggestion offered to an account without a nickname")
	}

	// The suggestion has to be a name the learner could have typed themselves,
	// or the skip button leads somewhere the set endpoint would reject.
	if _, err := nickname.Parse(suggestion); err != nil {
		t.Errorf("suggestion %q is not a valid nickname: %v", suggestion, err)
	}
}

func TestSetNicknameThenMeReportsIt(t *testing.T) {
	f := newFixture(t, true)
	session := f.signIn(t, "learner@example.com")

	rec := f.as(t, session, http.MethodPost, "/auth/nickname", `{"nickname":"Blaue Eule"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("/auth/nickname status = %d, want 200; body=%s", rec.Code, rec.Body)
	}

	body := f.me(t, session)
	if got := body["nickname"]; got != "Blaue Eule" {
		t.Errorf("nickname = %v, want %q", got, "Blaue Eule")
	}

	// Nothing to suggest to someone who has a name.
	if got, ok := body["suggestion"]; ok && got != "" {
		t.Errorf("suggestion = %v, want none once a name is set", got)
	}
}

// Renaming is the same endpoint, and this PR ships it.
func TestNicknameCanBeChanged(t *testing.T) {
	f := newFixture(t, true)
	session := f.signIn(t, "learner@example.com")

	for _, name := range []string{"Blaue Eule", "Dunkler Hund"} {
		rec := f.as(t, session, http.MethodPost, "/auth/nickname", `{"nickname":"`+name+`"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("setting %q: status = %d, body=%s", name, rec.Code, rec.Body)
		}
	}

	if got := f.me(t, session)["nickname"]; got != "Dunkler Hund" {
		t.Errorf("nickname = %v after renaming, want %q", got, "Dunkler Hund")
	}
}

func TestSetNicknameRejectsInvalidNames(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int
	}{
		{"too short", `{"nickname":"Ei"}`, http.StatusBadRequest},
		{"bad characters", `{"nickname":"Blaue_Eule"}`, http.StatusBadRequest},
		{"homoglyph", `{"nickname":"Jеff Blau"}`, http.StatusBadRequest},
		{"reserved", `{"nickname":"Admin"}`, http.StatusBadRequest},
		{"profane", `{"nickname":"Scheisse"}`, http.StatusBadRequest},
		{"not json", `nickname=Blaue`, http.StatusBadRequest},
		{"unknown field", `{"nick":"Blaue Eule"}`, http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, true)
			session := f.signIn(t, "learner@example.com")

			rec := f.as(t, session, http.MethodPost, "/auth/nickname", tt.body)
			if rec.Code != tt.want {
				t.Errorf("status = %d, want %d; body=%s", rec.Code, tt.want, rec.Body)
			}
		})
	}
}

// A taken name is a 409, not a 400: the request was fine, the world was not.
// Unlike the sign-in path there is nothing to conceal — a nickname is public —
// so the response says plainly what happened.
func TestSetNicknameConflictsAre409(t *testing.T) {
	f := newFixture(t, true)

	first := f.signIn(t, "first@example.com")
	second := f.signIn(t, "second@example.com")

	if rec := f.as(t, first, http.MethodPost, "/auth/nickname", `{"nickname":"Blaue Eule"}`); rec.Code != http.StatusOK {
		t.Fatalf("first claim: status = %d, body=%s", rec.Code, rec.Body)
	}

	rec := f.as(t, second, http.MethodPost, "/auth/nickname", `{"nickname":"blaue-eule"}`)
	if rec.Code != http.StatusConflict {
		t.Errorf("second claim: status = %d, want 409; body=%s", rec.Code, rec.Body)
	}
}

// Skipping must hand over the exact name the button was showing. This runs
// against the real generator on purpose: the bug it guards against was invisible
// to a stub namer, whose fixed sequence made the offered and generated names
// agree by construction. With ~112,000 pairs to draw from, a fresh draw matches
// the suggestion about once in 112,000 runs, so this fails essentially always if
// the preference is dropped.
func TestSkipAssignsTheNameThatWasOffered(t *testing.T) {
	f := newFixture(t, true)
	session := f.signIn(t, "learner@example.com")

	offered, _ := f.me(t, session)["suggestion"].(string)
	if offered == "" {
		t.Fatal("no suggestion offered, so there is nothing for skip to honour")
	}

	rec := f.as(t, session, http.MethodPost, "/auth/nickname/skip", `{"nickname":"`+offered+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("/auth/nickname/skip status = %d, want 200; body=%s", rec.Code, rec.Body)
	}

	assigned, _ := f.me(t, session)["nickname"].(string)
	if assigned != offered {
		t.Errorf("the button offered %q but the account got %q", offered, assigned)
	}
}

// A skip with no body still has to produce a name, since the client may have had
// no suggestion to show.
func TestSkipWithoutABodyStillAssigns(t *testing.T) {
	f := newFixture(t, true)
	session := f.signIn(t, "learner@example.com")

	rec := f.as(t, session, http.MethodPost, "/auth/nickname/skip", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("/auth/nickname/skip status = %d, want 200; body=%s", rec.Code, rec.Body)
	}

	assigned, _ := f.me(t, session)["nickname"].(string)
	if assigned == "" {
		t.Fatal("skipping left the account without a name")
	}
	if _, err := nickname.Parse(assigned); err != nil {
		t.Errorf("assigned name %q is not a valid nickname: %v", assigned, err)
	}
}

// The body goes through the same validation as the other endpoint, so skip is
// not a way round the rules. A name that would be refused there is ignored here
// and a generated one used instead.
func TestSkipIgnoresANameThatWouldBeRefused(t *testing.T) {
	for _, body := range []string{
		`{"nickname":"Admin"}`,
		`{"nickname":"Scheisse"}`,
		`{"nickname":"Blaue_Eule"}`,
		`{"nickname":"Ei"}`,
	} {
		t.Run(body, func(t *testing.T) {
			f := newFixture(t, true)
			session := f.signIn(t, "learner@example.com")

			rec := f.as(t, session, http.MethodPost, "/auth/nickname/skip", body)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
			}

			assigned, _ := f.me(t, session)["nickname"].(string)
			if _, err := nickname.Parse(assigned); err != nil {
				t.Errorf("assigned name %q is not a valid nickname: %v", assigned, err)
			}
		})
	}
}

// Two people offered the same name: the first gets it, the second gets something
// else rather than an error.
func TestSkipFallsBackWhenTheOfferedNameIsTaken(t *testing.T) {
	f := newFixture(t, true)

	first := f.signIn(t, "first@example.com")
	second := f.signIn(t, "second@example.com")

	contested, _ := f.me(t, first)["suggestion"].(string)
	if contested == "" {
		t.Fatal("no suggestion to contest")
	}

	if rec := f.as(t, first, http.MethodPost, "/auth/nickname/skip", `{"nickname":"`+contested+`"}`); rec.Code != http.StatusOK {
		t.Fatalf("first skip: status = %d, body=%s", rec.Code, rec.Body)
	}

	rec := f.as(t, second, http.MethodPost, "/auth/nickname/skip", `{"nickname":"`+contested+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("second skip: status = %d, want 200; body=%s", rec.Code, rec.Body)
	}

	got, _ := f.me(t, second)["nickname"].(string)
	if got == contested {
		t.Errorf("both accounts ended up with %q", contested)
	}
	if got == "" {
		t.Error("the second account got no name at all")
	}
}

// "Skip" must never overwrite a name someone chose.
func TestSkipDoesNotReplaceAnExistingName(t *testing.T) {
	f := newFixture(t, true)
	session := f.signIn(t, "learner@example.com")

	if rec := f.as(t, session, http.MethodPost, "/auth/nickname", `{"nickname":"Blaue Eule"}`); rec.Code != http.StatusOK {
		t.Fatalf("setting a name: status = %d, body=%s", rec.Code, rec.Body)
	}

	if rec := f.as(t, session, http.MethodPost, "/auth/nickname/skip", ""); rec.Code != http.StatusOK {
		t.Fatalf("/auth/nickname/skip status = %d, body=%s", rec.Code, rec.Body)
	}

	if got := f.me(t, session)["nickname"]; got != "Blaue Eule" {
		t.Errorf("nickname = %v after skipping, want the chosen %q", got, "Blaue Eule")
	}
}

// Both endpoints change an account, so both need a session. Without this they
// would be an unauthenticated way to write to whichever account the request
// happened to name.
func TestNicknameEndpointsRequireASession(t *testing.T) {
	f := newFixture(t, true)

	for _, target := range []string{"/auth/nickname", "/auth/nickname/skip"} {
		t.Run(target, func(t *testing.T) {
			rec := httptest.NewRecorder()
			f.h.ServeHTTP(rec, jsonReq(http.MethodPost, target, `{"nickname":"Blaue Eule"}`))

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", rec.Code)
			}
		})
	}
}

// The response says who is signed in, so it must not be cached anywhere on the
// way back — the same rule the rest of this surface follows.
func TestNicknameResponsesAreNotCached(t *testing.T) {
	f := newFixture(t, true)
	session := f.signIn(t, "learner@example.com")

	rec := f.as(t, session, http.MethodPost, "/auth/nickname", `{"nickname":"Blaue Eule"}`)
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}
