package identitybus_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jroedel/uebung/business/domain/identity/identitybus"
	"github.com/jroedel/uebung/business/domain/identity/stores/memdb"
	"github.com/jroedel/uebung/business/types/email"
	"github.com/jroedel/uebung/business/types/nickname"
	"github.com/jroedel/uebung/business/types/userid"
	"github.com/jroedel/uebung/foundation/secret"
)

// captureMailer records the link instead of sending it, so a test can follow it.
type captureMailer struct {
	links []string
	fail  error
}

func (m *captureMailer) SendLoginLink(_ context.Context, _ email.Email, link string) error {
	if m.fail != nil {
		return m.fail
	}
	m.links = append(m.links, link)

	return nil
}

// lastToken returns the token from the most recent link.
func (m *captureMailer) lastToken(t *testing.T) string {
	t.Helper()

	if len(m.links) == 0 {
		t.Fatal("no sign-in link was sent")
	}
	_, tok, found := strings.Cut(m.links[len(m.links)-1], "?token=")
	if !found || tok == "" {
		t.Fatalf("link %q carries no token", m.links[len(m.links)-1])
	}

	return tok
}

// stubNamer hands out names from a fixed sequence rather than at random, so a
// test can arrange a collision exactly instead of hoping the real generator
// produces one.
//
// Past the end of the sequence it falls back to a numbered name, which is what
// the real generator does for the same reason: to keep producing fresh
// candidates once the obvious ones are gone.
type stubNamer struct {
	names []string
	fail  error
	calls int
}

func (n *stubNamer) Generate(attempt int) (nickname.Nickname, error) {
	n.calls++

	if n.fail != nil {
		return nickname.Nickname{}, n.fail
	}
	if attempt < len(n.names) {
		return nickname.Parse(n.names[attempt])
	}

	return nickname.Parse("Blaue Eule " + strconv.Itoa(attempt))
}

type fixture struct {
	bus    *identitybus.Business
	store  *memdb.Store
	mailer *captureMailer
	namer  *stubNamer
	clock  *time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	f := &fixture{
		store:  memdb.New(),
		mailer: &captureMailer{},
		namer:  &stubNamer{names: []string{"Blaue Eule", "Dunkler Hund", "Leuchtendes Pferd"}},
		clock:  &now,
	}

	var n int
	bus, err := identitybus.NewBusiness(identitybus.Config{
		Storer:   f.store,
		Mailer:   f.mailer,
		Namer:    f.namer,
		LinkBase: "https://uebung.club/auth/callback",
		Now:      func() time.Time { return *f.clock },
		NewID: func() (userid.UserID, error) {
			n++

			return userid.Parse("user-" + string(rune('a'+n-1)))
		},
	})
	if err != nil {
		t.Fatalf("NewBusiness: %v", err)
	}
	f.bus = bus

	return f
}

func (f *fixture) advance(d time.Duration) { *f.clock = f.clock.Add(d) }

// signIn runs the whole flow and returns the raw session secret.
func (f *fixture) signIn(t *testing.T, addr string) string {
	t.Helper()

	if err := f.bus.RequestLogin(t.Context(), email.MustParse(addr)); err != nil {
		t.Fatalf("RequestLogin: %v", err)
	}

	session, _, err := f.bus.Redeem(t.Context(), f.mailer.lastToken(t))
	if err != nil {
		t.Fatalf("Redeem: %v", err)
	}

	return session
}

func TestSignInCreatesAndVerifiesAnAccount(t *testing.T) {
	f := newFixture(t)

	session := f.signIn(t, "learner@example.com")

	user, err := f.bus.Authenticate(t.Context(), session)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if user.Email != email.MustParse("learner@example.com") {
		t.Fatalf("signed in as %q", user.Email)
	}
	// Following the link is the proof of address ownership, so it is what flips
	// Verified — nothing else in the system does.
	if !user.Verified {
		t.Error("account is not verified after following the link")
	}
}

// A magic link is a bearer credential sitting in an inbox. It has to stop working
// the moment it is used, or a forwarded email is a permanent account handover.
func TestTokenIsSingleUse(t *testing.T) {
	f := newFixture(t)

	if err := f.bus.RequestLogin(t.Context(), email.MustParse("learner@example.com")); err != nil {
		t.Fatalf("RequestLogin: %v", err)
	}
	token := f.mailer.lastToken(t)

	if _, _, err := f.bus.Redeem(t.Context(), token); err != nil {
		t.Fatalf("first Redeem: %v", err)
	}

	_, _, err := f.bus.Redeem(t.Context(), token)
	if !errors.Is(err, identitybus.ErrInvalidCredential) {
		t.Fatalf("second Redeem err = %v, want ErrInvalidCredential", err)
	}
}

func TestTokenExpires(t *testing.T) {
	f := newFixture(t)

	if err := f.bus.RequestLogin(t.Context(), email.MustParse("learner@example.com")); err != nil {
		t.Fatalf("RequestLogin: %v", err)
	}
	token := f.mailer.lastToken(t)

	f.advance(16 * time.Minute) // default TTL is 15

	if _, _, err := f.bus.Redeem(t.Context(), token); !errors.Is(err, identitybus.ErrInvalidCredential) {
		t.Fatalf("Redeem after expiry err = %v, want ErrInvalidCredential", err)
	}
}

func TestGarbageTokensAreRejected(t *testing.T) {
	f := newFixture(t)

	for _, tok := range []string{"", "not-a-token", strings.Repeat("A", 43)} {
		if _, _, err := f.bus.Redeem(t.Context(), tok); !errors.Is(err, identitybus.ErrInvalidCredential) {
			t.Errorf("Redeem(%q) err = %v, want ErrInvalidCredential", tok, err)
		}
	}
}

// Requesting a link for an address that already has an account must not create a
// second one: the address is the identity, and two rows would mean two decks.
func TestRepeatRequestReusesTheAccount(t *testing.T) {
	f := newFixture(t)
	addr := email.MustParse("learner@example.com")

	for range 3 {
		if err := f.bus.RequestLogin(t.Context(), addr); err != nil {
			t.Fatalf("RequestLogin: %v", err)
		}
	}

	// Every link must still work against the same account, and each redemption
	// must land on the same user id.
	user, found, err := f.store.UserByEmail(t.Context(), addr)
	if err != nil || !found {
		t.Fatalf("UserByEmail: %v found=%v", err, found)
	}

	_, redeemed, err := f.bus.Redeem(t.Context(), f.mailer.lastToken(t))
	if err != nil {
		t.Fatalf("Redeem: %v", err)
	}
	if redeemed.ID != user.ID {
		t.Fatalf("redeemed as %q, want the existing account %q", redeemed.ID, user.ID)
	}
	if len(f.mailer.links) != 3 {
		t.Fatalf("sent %d links, want 3", len(f.mailer.links))
	}
}

// Nothing that could be replayed as a credential may be stored. If a token or
// session secret survives in the database, a leaked backup is a set of live
// logins.
func TestOnlyHashesAreStored(t *testing.T) {
	f := newFixture(t)

	if err := f.bus.RequestLogin(t.Context(), email.MustParse("learner@example.com")); err != nil {
		t.Fatalf("RequestLogin: %v", err)
	}
	token := f.mailer.lastToken(t)

	if _, found, _ := f.store.LoginTokenByHash(t.Context(), token); found {
		t.Fatal("the raw token is usable as a storage key: it was stored unhashed")
	}
	stored, found, err := f.store.LoginTokenByHash(t.Context(), secret.Hash(token))
	if err != nil || !found {
		t.Fatalf("token not found under its hash: %v found=%v", err, found)
	}
	if stored.Hash == token {
		t.Fatal("stored hash equals the raw token")
	}

	session := f.signIn(t, "other@example.com")
	if _, found, _ := f.store.SessionByHash(t.Context(), session); found {
		t.Fatal("the raw session secret is a storage key: it was stored unhashed")
	}
}

func TestSessionExpiresAndLogoutRevokes(t *testing.T) {
	t.Run("expiry", func(t *testing.T) {
		f := newFixture(t)
		session := f.signIn(t, "learner@example.com")

		f.advance(91 * 24 * time.Hour) // default session TTL is 90 days

		if _, err := f.bus.Authenticate(t.Context(), session); !errors.Is(err, identitybus.ErrInvalidCredential) {
			t.Fatalf("Authenticate after expiry err = %v, want ErrInvalidCredential", err)
		}
	})

	// Sign-out deletes the row rather than only clearing a cookie, so it also
	// covers a device that is no longer in your hands.
	t.Run("logout", func(t *testing.T) {
		f := newFixture(t)
		session := f.signIn(t, "learner@example.com")

		if err := f.bus.Logout(t.Context(), session); err != nil {
			t.Fatalf("Logout: %v", err)
		}
		if _, err := f.bus.Authenticate(t.Context(), session); !errors.Is(err, identitybus.ErrInvalidCredential) {
			t.Fatalf("Authenticate after logout err = %v, want ErrInvalidCredential", err)
		}
	})
}

// Two sessions for one account are independent: signing out on the laptop must
// not sign you out on the phone.
func TestSessionsAreIndependent(t *testing.T) {
	f := newFixture(t)

	first := f.signIn(t, "learner@example.com")
	second := f.signIn(t, "learner@example.com")

	if err := f.bus.Logout(t.Context(), first); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, err := f.bus.Authenticate(t.Context(), second); err != nil {
		t.Fatalf("the other session was revoked too: %v", err)
	}
}

// A send failure must not leave a usable account behind quietly; the caller needs
// the error so it can tell the user nothing is coming.
func TestSendFailureIsReported(t *testing.T) {
	f := newFixture(t)
	f.mailer.fail = errors.New("smtp is down")

	if err := f.bus.RequestLogin(t.Context(), email.MustParse("learner@example.com")); err == nil {
		t.Fatal("RequestLogin succeeded despite the mailer failing")
	}
}

// An account that has never followed a link is unverified, and an unverified
// account must not be able to hold a live session. Redeem is the only path that
// issues one, and it verifies first — this pins that invariant at the gate every
// study request passes through.
func TestUnverifiedAccountCannotAuthenticate(t *testing.T) {
	f := newFixture(t)
	addr := email.MustParse("learner@example.com")

	if err := f.bus.RequestLogin(t.Context(), addr); err != nil {
		t.Fatalf("RequestLogin: %v", err)
	}

	user, _, err := f.store.UserByEmail(t.Context(), addr)
	if err != nil {
		t.Fatalf("UserByEmail: %v", err)
	}
	if user.Verified {
		t.Fatal("account was verified merely by requesting a link")
	}

	// Forge a session for the unverified account, as a compromised store might.
	raw, err := secret.New()
	if err != nil {
		t.Fatalf("secret.New: %v", err)
	}
	if err := f.store.SaveSession(t.Context(), identitybus.Session{
		Hash: secret.Hash(raw), UserID: user.ID, Expires: f.clock.Add(time.Hour),
	}); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	if _, err := f.bus.Authenticate(t.Context(), raw); !errors.Is(err, identitybus.ErrInvalidCredential) {
		t.Fatalf("unverified account authenticated: err = %v", err)
	}
}

// --- nicknames --------------------------------------------------------------

// userFor signs an address in and returns the account behind the session.
func (f *fixture) userFor(t *testing.T, addr string) identitybus.User {
	t.Helper()

	user, err := f.bus.Authenticate(t.Context(), f.signIn(t, addr))
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}

	return user
}

func TestNewAccountHasNoNickname(t *testing.T) {
	f := newFixture(t)

	user := f.userFor(t, "learner@example.com")

	if user.HasNickname() {
		t.Errorf("a fresh account already has the nickname %q, want none", user.Nickname)
	}
	if !user.Nickname.IsZero() {
		t.Errorf("a fresh account's nickname is %q, want the zero value", user.Nickname)
	}
}

func TestSetNicknameSticks(t *testing.T) {
	f := newFixture(t)

	user := f.userFor(t, "learner@example.com")

	updated, err := f.bus.SetNickname(t.Context(), user.ID, nickname.MustParse("Blaue Eule"))
	if err != nil {
		t.Fatalf("SetNickname: %v", err)
	}
	if updated.Nickname != nickname.MustParse("Blaue Eule") {
		t.Errorf("SetNickname returned %q, want %q", updated.Nickname, "Blaue Eule")
	}

	// And it is readable on the next request, not just in the return value.
	reloaded, found, err := f.store.UserByID(t.Context(), user.ID)
	if err != nil || !found {
		t.Fatalf("UserByID: found = %v, err = %v", found, err)
	}
	if reloaded.Nickname.String() != "Blaue Eule" {
		t.Errorf("stored nickname is %q, want %q", reloaded.Nickname, "Blaue Eule")
	}
}

func TestNicknameIsTakenAcrossCaseAndSeparators(t *testing.T) {
	// Each of these is the same name as "Blaue Eule" to anyone reading a
	// leaderboard, so each must be refused once the first is held.
	variants := []string{"Blaue Eule", "blaue eule", "BLAUE EULE", "blaue-eule", "BlaueEule"}

	for _, variant := range variants {
		t.Run(variant, func(t *testing.T) {
			f := newFixture(t)

			first := f.userFor(t, "first@example.com")
			second := f.userFor(t, "second@example.com")

			if _, err := f.bus.SetNickname(t.Context(), first.ID, nickname.MustParse("Blaue Eule")); err != nil {
				t.Fatalf("SetNickname for the first account: %v", err)
			}

			_, err := f.bus.SetNickname(t.Context(), second.ID, nickname.MustParse(variant))
			if !errors.Is(err, identitybus.ErrNicknameTaken) {
				t.Errorf("second account claiming %q: err = %v, want ErrNicknameTaken", variant, err)
			}
		})
	}
}

// Re-spelling a name you already hold folds to the key your own row has. It must
// not collide with itself.
func TestRespellingYourOwnNicknameSucceeds(t *testing.T) {
	f := newFixture(t)

	user := f.userFor(t, "learner@example.com")

	if _, err := f.bus.SetNickname(t.Context(), user.ID, nickname.MustParse("blaue eule")); err != nil {
		t.Fatalf("SetNickname: %v", err)
	}

	updated, err := f.bus.SetNickname(t.Context(), user.ID, nickname.MustParse("Blaue Eule"))
	if err != nil {
		t.Fatalf("re-spelling an own nickname: %v", err)
	}
	if updated.Nickname.String() != "Blaue Eule" {
		t.Errorf("nickname is %q after re-spelling, want %q", updated.Nickname, "Blaue Eule")
	}
}

func TestRenameFreesThePreviousName(t *testing.T) {
	f := newFixture(t)

	first := f.userFor(t, "first@example.com")
	second := f.userFor(t, "second@example.com")

	if _, err := f.bus.SetNickname(t.Context(), first.ID, nickname.MustParse("Blaue Eule")); err != nil {
		t.Fatalf("SetNickname: %v", err)
	}
	if _, err := f.bus.SetNickname(t.Context(), first.ID, nickname.MustParse("Dunkler Hund")); err != nil {
		t.Fatalf("renaming: %v", err)
	}

	// The abandoned name is available again immediately.
	if _, err := f.bus.SetNickname(t.Context(), second.ID, nickname.MustParse("Blaue Eule")); err != nil {
		t.Errorf("claiming an abandoned name: %v", err)
	}
}

// Signing in again runs SaveUser, which must leave the nickname alone. Getting
// this wrong would wipe everyone's name on their next visit — silently, since
// nothing else about sign-in would look different.
func TestSigningInAgainKeepsTheNickname(t *testing.T) {
	f := newFixture(t)

	user := f.userFor(t, "learner@example.com")
	if _, err := f.bus.SetNickname(t.Context(), user.ID, nickname.MustParse("Blaue Eule")); err != nil {
		t.Fatalf("SetNickname: %v", err)
	}

	again := f.userFor(t, "learner@example.com")

	if again.Nickname.String() != "Blaue Eule" {
		t.Errorf("nickname is %q after signing in again, want %q", again.Nickname, "Blaue Eule")
	}
}

func TestAssignNicknameGivesAGeneratedName(t *testing.T) {
	f := newFixture(t)

	user := f.userFor(t, "learner@example.com")

	updated, err := f.bus.AssignNickname(t.Context(), user.ID)
	if err != nil {
		t.Fatalf("AssignNickname: %v", err)
	}
	if !updated.HasNickname() {
		t.Fatal("AssignNickname left the account without a name")
	}
	if updated.Nickname.String() != "Blaue Eule" {
		t.Errorf("assigned %q, want the namer's first offer %q", updated.Nickname, "Blaue Eule")
	}
}

// The suggestion shown on the skip button is not reserved, so by the time it is
// accepted somebody else may hold it. Losing that race must be silent: the
// person asked not to think about this.
func TestAssignNicknameRetriesPastATakenName(t *testing.T) {
	f := newFixture(t)

	first := f.userFor(t, "first@example.com")
	second := f.userFor(t, "second@example.com")

	// The first account takes what the namer offers first.
	if _, err := f.bus.SetNickname(t.Context(), first.ID, nickname.MustParse("Blaue Eule")); err != nil {
		t.Fatalf("SetNickname: %v", err)
	}

	updated, err := f.bus.AssignNickname(t.Context(), second.ID)
	if err != nil {
		t.Fatalf("AssignNickname: %v", err)
	}
	if updated.Nickname.String() != "Dunkler Hund" {
		t.Errorf("assigned %q, want the namer's second offer %q", updated.Nickname, "Dunkler Hund")
	}
}

func TestSuggestNicknameSkipsTakenNames(t *testing.T) {
	f := newFixture(t)

	holder := f.userFor(t, "holder@example.com")
	if _, err := f.bus.SetNickname(t.Context(), holder.ID, nickname.MustParse("Blaue Eule")); err != nil {
		t.Fatalf("SetNickname: %v", err)
	}

	suggestion, err := f.bus.SuggestNickname(t.Context())
	if err != nil {
		t.Fatalf("SuggestNickname: %v", err)
	}
	if suggestion.String() == "Blaue Eule" {
		t.Error("SuggestNickname offered a name that is already taken")
	}
}

// A suggestion is a proposal, not a reservation: asking twice in a row without
// anyone accepting must not consume anything.
func TestSuggestNicknameReservesNothing(t *testing.T) {
	f := newFixture(t)

	first, err := f.bus.SuggestNickname(t.Context())
	if err != nil {
		t.Fatalf("SuggestNickname: %v", err)
	}

	second, err := f.bus.SuggestNickname(t.Context())
	if err != nil {
		t.Fatalf("SuggestNickname: %v", err)
	}

	if first != second {
		t.Errorf("consecutive suggestions differ (%q then %q), so the first was consumed", first, second)
	}
}

func TestSetNicknameRejectsAnUnknownAccount(t *testing.T) {
	f := newFixture(t)

	stranger, err := userid.Parse("user-nobody")
	if err != nil {
		t.Fatalf("userid.Parse: %v", err)
	}

	if _, err := f.bus.SetNickname(t.Context(), stranger, nickname.MustParse("Blaue Eule")); !errors.Is(err, identitybus.ErrInvalidCredential) {
		t.Errorf("SetNickname for an unknown account: err = %v, want ErrInvalidCredential", err)
	}
}

func TestSetNicknameRejectsTheZeroValue(t *testing.T) {
	f := newFixture(t)

	user := f.userFor(t, "learner@example.com")

	if _, err := f.bus.SetNickname(t.Context(), user.ID, nickname.Nickname{}); err == nil {
		t.Error("SetNickname accepted the zero Nickname, which would clear the name")
	}
}

// A namer that cannot produce a name is a startup-shaped problem, but it must
// surface as an error rather than as an account silently left unnamed.
func TestAssignNicknameReportsAGeneratorFailure(t *testing.T) {
	f := newFixture(t)

	user := f.userFor(t, "learner@example.com")
	f.namer.fail = errors.New("word lists unavailable")

	if _, err := f.bus.AssignNickname(t.Context(), user.ID); err == nil {
		t.Error("AssignNickname succeeded despite the namer failing")
	}
}
