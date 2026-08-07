package sqlitedb_test

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jroedel/uebung/business/domain/identity/identitybus"
	"github.com/jroedel/uebung/business/domain/identity/stores/sqlitedb"
	"github.com/jroedel/uebung/business/types/email"
	"github.com/jroedel/uebung/business/types/userid"
	"github.com/jroedel/uebung/foundation/sqldb"
)

func open(t *testing.T) *sqlitedb.Store {
	t.Helper()

	db, err := sqldb.Open(t.Context(), filepath.Join(t.TempDir(), "identity.db"))
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("closing database: %v", err)
		}
	})

	s, err := sqlitedb.Open(t.Context(), db)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	return s
}

func sampleUser() identitybus.User {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	return identitybus.User{
		ID:       userid.MustParse("user-a"),
		Email:    email.MustParse("learner@example.com"),
		Created:  now,
		LastSeen: now,
	}
}

func TestUserRoundTripsByIDAndEmail(t *testing.T) {
	s := open(t)
	want := sampleUser()

	if err := s.SaveUser(t.Context(), want); err != nil {
		t.Fatalf("SaveUser: %v", err)
	}

	byID, found, err := s.UserByID(t.Context(), want.ID)
	if err != nil || !found {
		t.Fatalf("UserByID: %v found=%v", err, found)
	}
	byEmail, found, err := s.UserByEmail(t.Context(), want.Email)
	if err != nil || !found {
		t.Fatalf("UserByEmail: %v found=%v", err, found)
	}

	if byID.ID != want.ID || byEmail.ID != want.ID || byID.Email != want.Email {
		t.Fatalf("round-trip mismatch: byID=%+v byEmail=%+v", byID, byEmail)
	}
	if byID.Verified {
		t.Error("a new account came back verified")
	}
	if !byID.Created.Equal(want.Created) {
		t.Errorf("created = %v, want %v", byID.Created, want.Created)
	}
}

func TestMissingLookupsAreNotErrors(t *testing.T) {
	s := open(t)

	if _, found, err := s.UserByEmail(t.Context(), email.MustParse("nobody@example.com")); err != nil || found {
		t.Fatalf("UserByEmail on missing: err=%v found=%v", err, found)
	}
	if _, found, err := s.SessionByHash(t.Context(), "nope"); err != nil || found {
		t.Fatalf("SessionByHash on missing: err=%v found=%v", err, found)
	}
	if _, found, err := s.LoginTokenByHash(t.Context(), "nope"); err != nil || found {
		t.Fatalf("LoginTokenByHash on missing: err=%v found=%v", err, found)
	}
}

// The single-use guarantee has to hold against concurrent redemption, not just
// sequential. A double-clicked link, or a mail scanner prefetching it at the same
// moment the user taps, must still yield exactly one session — which is why the
// claim is a conditional UPDATE rather than a read followed by a write.
func TestMarkLoginTokenUsedIsExactlyOnceUnderConcurrency(t *testing.T) {
	s := open(t)

	user := sampleUser()
	if err := s.SaveUser(t.Context(), user); err != nil {
		t.Fatalf("SaveUser: %v", err)
	}

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	if err := s.SaveLoginToken(t.Context(), identitybus.LoginToken{
		Hash: "token-hash", UserID: user.ID, Created: now, Expires: now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("SaveLoginToken: %v", err)
	}

	const racers = 8
	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		claims int
	)
	for range racers {
		wg.Go(func() {
			ok, err := s.MarkLoginTokenUsed(t.Context(), "token-hash", now)
			if err != nil {
				t.Errorf("MarkLoginTokenUsed: %v", err)

				return
			}
			if ok {
				mu.Lock()
				claims++
				mu.Unlock()
			}
		})
	}
	wg.Wait()

	if claims != 1 {
		t.Fatalf("%d of %d racers claimed the token, want exactly 1", claims, racers)
	}
}

func TestSessionLifecycle(t *testing.T) {
	s := open(t)

	user := sampleUser()
	if err := s.SaveUser(t.Context(), user); err != nil {
		t.Fatalf("SaveUser: %v", err)
	}

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	sess := identitybus.Session{
		Hash: "session-hash", UserID: user.ID, Created: now, Expires: now.Add(24 * time.Hour),
	}
	if err := s.SaveSession(t.Context(), sess); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	got, found, err := s.SessionByHash(t.Context(), sess.Hash)
	if err != nil || !found {
		t.Fatalf("SessionByHash: %v found=%v", err, found)
	}
	if got.UserID != user.ID || !got.Expires.Equal(sess.Expires) {
		t.Fatalf("session round-trip mismatch: %+v", got)
	}

	if err := s.DeleteSession(t.Context(), sess.Hash); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, found, _ := s.SessionByHash(t.Context(), sess.Hash); found {
		t.Fatal("session survived deletion")
	}
}

func TestDeleteExpiredPurgesOnlyExpired(t *testing.T) {
	s := open(t)

	user := sampleUser()
	if err := s.SaveUser(t.Context(), user); err != nil {
		t.Fatalf("SaveUser: %v", err)
	}

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	live := identitybus.Session{Hash: "live", UserID: user.ID, Created: now, Expires: now.Add(time.Hour)}
	dead := identitybus.Session{Hash: "dead", UserID: user.ID, Created: now, Expires: now.Add(-time.Hour)}
	for _, sess := range []identitybus.Session{live, dead} {
		if err := s.SaveSession(t.Context(), sess); err != nil {
			t.Fatalf("SaveSession: %v", err)
		}
	}

	if err := s.DeleteExpired(t.Context(), now); err != nil {
		t.Fatalf("DeleteExpired: %v", err)
	}

	if _, found, _ := s.SessionByHash(t.Context(), "dead"); found {
		t.Error("expired session survived the purge")
	}
	if _, found, _ := s.SessionByHash(t.Context(), "live"); !found {
		t.Error("live session was purged")
	}
}
