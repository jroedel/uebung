package authapp_test

import (
	"context"
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
	"github.com/jroedel/uebung/business/domain/identity/stores/memdb"
	"github.com/jroedel/uebung/business/types/email"
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
	var n int
	identity, err := identitybus.NewBusiness(identitybus.Config{
		Storer:   memdb.New(),
		Mailer:   mailer,
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
