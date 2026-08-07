package identitybus_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jroedel/uebung/business/domain/identity/identitybus"
	"github.com/jroedel/uebung/business/domain/identity/stores/memdb"
	"github.com/jroedel/uebung/business/types/email"
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

type fixture struct {
	bus    *identitybus.Business
	store  *memdb.Store
	mailer *captureMailer
	clock  *time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	f := &fixture{store: memdb.New(), mailer: &captureMailer{}, clock: &now}

	var n int
	bus, err := identitybus.NewBusiness(identitybus.Config{
		Storer:   f.store,
		Mailer:   f.mailer,
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
