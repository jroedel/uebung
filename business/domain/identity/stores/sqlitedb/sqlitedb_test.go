package sqlitedb_test

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jroedel/uebung/business/domain/identity/identitybus"
	"github.com/jroedel/uebung/business/domain/identity/stores/sqlitedb"
	"github.com/jroedel/uebung/business/types/email"
	"github.com/jroedel/uebung/business/types/nickname"
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

// --- nicknames --------------------------------------------------------------

func TestNicknameRoundTrips(t *testing.T) {
	s := open(t)
	user := sampleUser()

	if err := s.SaveUser(t.Context(), user); err != nil {
		t.Fatalf("SaveUser: %v", err)
	}

	claimed, err := s.SetNickname(t.Context(), user.ID, nickname.MustParse("Blaue Eule"))
	if err != nil || !claimed {
		t.Fatalf("SetNickname: claimed = %v, err = %v", claimed, err)
	}

	got, found, err := s.UserByID(t.Context(), user.ID)
	if err != nil || !found {
		t.Fatalf("UserByID: found = %v, err = %v", found, err)
	}
	if got.Nickname.String() != "Blaue Eule" {
		t.Errorf("nickname round-tripped as %q, want %q", got.Nickname, "Blaue Eule")
	}
}

// An account with no nickname reads back as the zero value, not as an error and
// not as a name that happens to be empty.
func TestMissingNicknameIsTheZeroValue(t *testing.T) {
	s := open(t)
	user := sampleUser()

	if err := s.SaveUser(t.Context(), user); err != nil {
		t.Fatalf("SaveUser: %v", err)
	}

	got, found, err := s.UserByID(t.Context(), user.ID)
	if err != nil || !found {
		t.Fatalf("UserByID: found = %v, err = %v", found, err)
	}
	if !got.Nickname.IsZero() {
		t.Errorf("nickname is %q, want the zero value", got.Nickname)
	}
}

func TestUserByNicknameMatchesTheFoldedForm(t *testing.T) {
	s := open(t)
	user := sampleUser()

	if err := s.SaveUser(t.Context(), user); err != nil {
		t.Fatalf("SaveUser: %v", err)
	}
	if _, err := s.SetNickname(t.Context(), user.ID, nickname.MustParse("Blaue Eule")); err != nil {
		t.Fatalf("SetNickname: %v", err)
	}

	for _, variant := range []string{"Blaue Eule", "blaue eule", "BLAUE-EULE", "BlaueEule"} {
		got, found, err := s.UserByNickname(t.Context(), nickname.MustParse(variant))
		if err != nil {
			t.Fatalf("UserByNickname(%q): %v", variant, err)
		}
		if !found {
			t.Errorf("UserByNickname(%q) found nothing, want the holder", variant)

			continue
		}
		if got.ID != user.ID {
			t.Errorf("UserByNickname(%q) found %q, want %q", variant, got.ID, user.ID)
		}
	}
}

func TestSetNicknameRefusesATakenName(t *testing.T) {
	s := open(t)

	first := sampleUser()
	second := identitybus.User{
		ID:       userid.MustParse("user-b"),
		Email:    email.MustParse("other@example.com"),
		Created:  first.Created,
		LastSeen: first.LastSeen,
	}
	for _, u := range []identitybus.User{first, second} {
		if err := s.SaveUser(t.Context(), u); err != nil {
			t.Fatalf("SaveUser: %v", err)
		}
	}

	if claimed, err := s.SetNickname(t.Context(), first.ID, nickname.MustParse("Blaue Eule")); err != nil || !claimed {
		t.Fatalf("first claim: claimed = %v, err = %v", claimed, err)
	}

	claimed, err := s.SetNickname(t.Context(), second.ID, nickname.MustParse("blaue-eule"))
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if claimed {
		t.Error("two accounts were allowed to hold the same folded nickname")
	}
}

// Re-spelling a name you already hold must not collide with your own row.
func TestSetNicknameAllowsRespellingYourOwn(t *testing.T) {
	s := open(t)
	user := sampleUser()

	if err := s.SaveUser(t.Context(), user); err != nil {
		t.Fatalf("SaveUser: %v", err)
	}
	if _, err := s.SetNickname(t.Context(), user.ID, nickname.MustParse("blaue eule")); err != nil {
		t.Fatalf("SetNickname: %v", err)
	}

	claimed, err := s.SetNickname(t.Context(), user.ID, nickname.MustParse("Blaue Eule"))
	if err != nil || !claimed {
		t.Fatalf("re-spelling own nickname: claimed = %v, err = %v", claimed, err)
	}
}

func TestSetNicknameReportsFalseForAnUnknownAccount(t *testing.T) {
	s := open(t)

	claimed, err := s.SetNickname(t.Context(), userid.MustParse("user-nobody"), nickname.MustParse("Blaue Eule"))
	if err != nil {
		t.Fatalf("SetNickname: %v", err)
	}
	if claimed {
		t.Error("SetNickname claimed a name for an account that does not exist")
	}
}

// SaveUser runs on every sign-in. It must leave the nickname alone, including
// when handed a stale User that predates the name being set.
func TestSaveUserDoesNotDisturbTheNickname(t *testing.T) {
	s := open(t)
	user := sampleUser()

	if err := s.SaveUser(t.Context(), user); err != nil {
		t.Fatalf("SaveUser: %v", err)
	}
	if _, err := s.SetNickname(t.Context(), user.ID, nickname.MustParse("Blaue Eule")); err != nil {
		t.Fatalf("SetNickname: %v", err)
	}

	// `user` still carries the zero Nickname it was created with.
	stale := user
	stale.Verified = true
	if err := s.SaveUser(t.Context(), stale); err != nil {
		t.Fatalf("SaveUser: %v", err)
	}

	got, found, err := s.UserByID(t.Context(), user.ID)
	if err != nil || !found {
		t.Fatalf("UserByID: found = %v, err = %v", found, err)
	}
	if got.Nickname.String() != "Blaue Eule" {
		t.Errorf("nickname is %q after a stale SaveUser, want %q", got.Nickname, "Blaue Eule")
	}
	if !got.Verified {
		t.Error("SaveUser did not write the fields it is responsible for")
	}
}

// The migration has to run against the table as it exists in production: created
// by the previous schema, with rows in it. This builds exactly that and then
// opens the store over it.
func TestOpenMigratesAPopulatedPreNicknameTable(t *testing.T) {
	db, err := sqldb.Open(t.Context(), filepath.Join(t.TempDir(), "identity.db"))
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("closing database: %v", err)
		}
	})

	// The users table exactly as it was before this change.
	const oldSchema = `
CREATE TABLE users (
	id        TEXT    NOT NULL PRIMARY KEY,
	email     TEXT    NOT NULL UNIQUE,
	verified  INTEGER NOT NULL,
	created   TEXT    NOT NULL,
	last_seen TEXT    NOT NULL
) STRICT;`

	if _, err := db.ExecContext(t.Context(), oldSchema); err != nil {
		t.Fatalf("creating the old schema: %v", err)
	}

	// Two accounts, because the failure this guards against is a plain UNIQUE
	// index finding every pre-existing row in conflict with every other on the
	// empty fold. One row would not catch it.
	const insert = `
INSERT INTO users (id, email, verified, created, last_seen)
VALUES ('user-a', 'first@example.com', 1, '2026-01-01T12:00:00Z', '2026-01-01T12:00:00Z'),
       ('user-b', 'second@example.com', 1, '2026-01-01T12:00:00Z', '2026-01-01T12:00:00Z');`

	if _, err := db.ExecContext(t.Context(), insert); err != nil {
		t.Fatalf("seeding the old table: %v", err)
	}

	s, err := sqlitedb.Open(t.Context(), db)
	if err != nil {
		t.Fatalf("Open over a populated pre-nickname table: %v", err)
	}

	// The existing accounts survive, with no nickname.
	existing, found, err := s.UserByID(t.Context(), userid.MustParse("user-a"))
	if err != nil || !found {
		t.Fatalf("UserByID after migrating: found = %v, err = %v", found, err)
	}
	if !existing.Nickname.IsZero() {
		t.Errorf("a migrated account has nickname %q, want none", existing.Nickname)
	}

	// And the column works from here on.
	if claimed, err := s.SetNickname(t.Context(), existing.ID, nickname.MustParse("Blaue Eule")); err != nil || !claimed {
		t.Fatalf("SetNickname after migrating: claimed = %v, err = %v", claimed, err)
	}
}

// Open runs on every start, so the migration has to be a no-op the second time.
func TestOpenIsIdempotent(t *testing.T) {
	db, err := sqldb.Open(t.Context(), filepath.Join(t.TempDir(), "identity.db"))
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("closing database: %v", err)
		}
	})

	for i := range 3 {
		s, err := sqlitedb.Open(t.Context(), db)
		if err != nil {
			t.Fatalf("Open number %d: %v", i+1, err)
		}

		// Not just "did not error": the store still has to work afterwards.
		if _, _, err := s.UserByNickname(t.Context(), nickname.MustParse("Blaue Eule")); err != nil {
			t.Fatalf("UserByNickname after Open number %d: %v", i+1, err)
		}
	}
}
