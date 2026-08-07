// Package sqlitedb is the SQLite-backed identitybus.Storer.
//
// It follows the same Storage-edge rules as the study store: rows hold natives
// and strings only, times are RFC3339Nano text in UTC, and toBus* converters
// parse them back into strong types and return an error rather than defaulting.
//
// Two things here are security-relevant rather than merely persistent:
//
//   - The token and session tables store only hashes. There is no column that
//     could be replayed as a credential, so a leaked backup grants nothing.
//   - MarkLoginTokenUsed is a single conditional UPDATE, not a read followed by a
//     write. That makes single-use a property of the database rather than of the
//     interleaving of two goroutines, which is what stops a double-clicked link
//     from minting two sessions.
package sqlitedb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jroedel/uebung/business/domain/identity/identitybus"
	"github.com/jroedel/uebung/business/types/email"
	"github.com/jroedel/uebung/business/types/userid"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

const timeLayout = time.RFC3339Nano

// schema is applied on open and is idempotent.
//
// users.email is UNIQUE because the address is the account: two rows for one
// address would mean two decks and a link that signs you into an arbitrary one.
// The lookup indexes exist because session resolution runs on every request.
const schema = `
CREATE TABLE IF NOT EXISTS users (
	id        TEXT    NOT NULL PRIMARY KEY,
	email     TEXT    NOT NULL UNIQUE,
	verified  INTEGER NOT NULL,
	created   TEXT    NOT NULL,
	last_seen TEXT    NOT NULL
) STRICT;

CREATE TABLE IF NOT EXISTS login_tokens (
	hash    TEXT NOT NULL PRIMARY KEY,
	user_id TEXT NOT NULL,
	created TEXT NOT NULL,
	expires TEXT NOT NULL,
	used    TEXT NOT NULL DEFAULT ''
) STRICT;

CREATE TABLE IF NOT EXISTS sessions (
	hash    TEXT NOT NULL PRIMARY KEY,
	user_id TEXT NOT NULL,
	created TEXT NOT NULL,
	expires TEXT NOT NULL
) STRICT;

CREATE INDEX IF NOT EXISTS idx_login_tokens_expires ON login_tokens(expires);
CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires);
`

// Store is the SQLite identity store. It borrows an existing *sql.DB so identity
// and study share one file, one connection pool and therefore one writer.
type Store struct {
	db *sql.DB
}

// Open applies the identity schema to an already-open database and returns the
// store. It takes a *sql.DB rather than a path because accounts and progress live
// in the same file: opening a second handle to one SQLite database would put two
// writers behind the single-connection assumption the study store relies on.
func Open(ctx context.Context, db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, errors.New("sqlitedb: a database handle is required")
	}

	if _, err := db.ExecContext(ctx, schema); err != nil {
		return nil, fmt.Errorf("sqlitedb: applying identity schema: %w", err)
	}

	return &Store{db: db}, nil
}

const userColumns = `id, email, verified, created, last_seen`

func (s *Store) UserByEmail(ctx context.Context, addr email.Email) (identitybus.User, bool, error) {
	const q = `SELECT ` + userColumns + ` FROM users WHERE email = ?`

	return s.scanUser(s.db.QueryRowContext(ctx, q, addr.String()))
}

func (s *Store) UserByID(ctx context.Context, id userid.UserID) (identitybus.User, bool, error) {
	const q = `SELECT ` + userColumns + ` FROM users WHERE id = ?`

	return s.scanUser(s.db.QueryRowContext(ctx, q, id.String()))
}

func (s *Store) scanUser(row *sql.Row) (identitybus.User, bool, error) {
	var r dbUser

	err := row.Scan(&r.ID, &r.Email, &r.Verified, &r.Created, &r.LastSeen)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return identitybus.User{}, false, nil
	case err != nil:
		return identitybus.User{}, false, fmt.Errorf("sqlitedb: scanning user: %w", err)
	}

	u, err := toBusUser(r)
	if err != nil {
		return identitybus.User{}, false, fmt.Errorf("sqlitedb: user %q: %w", r.ID, err)
	}

	return u, true, nil
}

func (s *Store) SaveUser(ctx context.Context, u identitybus.User) error {
	const q = `
INSERT INTO users (` + userColumns + `) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (id) DO UPDATE SET
	email = excluded.email, verified = excluded.verified, last_seen = excluded.last_seen`

	r := toDBUser(u)
	if _, err := s.db.ExecContext(ctx, q, r.ID, r.Email, r.Verified, r.Created, r.LastSeen); err != nil {
		return fmt.Errorf("sqlitedb: saving user: %w", err)
	}

	return nil
}

func (s *Store) SaveLoginToken(ctx context.Context, t identitybus.LoginToken) error {
	const q = `INSERT INTO login_tokens (hash, user_id, created, expires, used) VALUES (?, ?, ?, ?, ?)`

	if _, err := s.db.ExecContext(ctx, q,
		t.Hash, t.UserID.String(), formatTime(t.Created), formatTime(t.Expires), formatOptionalTime(t.Used),
	); err != nil {
		return fmt.Errorf("sqlitedb: saving login token: %w", err)
	}

	return nil
}

func (s *Store) LoginTokenByHash(ctx context.Context, hash string) (identitybus.LoginToken, bool, error) {
	const q = `SELECT hash, user_id, created, expires, used FROM login_tokens WHERE hash = ?`

	var r dbLoginToken
	err := s.db.QueryRowContext(ctx, q, hash).Scan(&r.Hash, &r.UserID, &r.Created, &r.Expires, &r.Used)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return identitybus.LoginToken{}, false, nil
	case err != nil:
		return identitybus.LoginToken{}, false, fmt.Errorf("sqlitedb: scanning login token: %w", err)
	}

	t, err := toBusLoginToken(r)
	if err != nil {
		return identitybus.LoginToken{}, false, fmt.Errorf("sqlitedb: login token: %w", err)
	}

	return t, true, nil
}

// MarkLoginTokenUsed claims a token in one statement. The `used = ”` predicate is
// the single-use guarantee: whichever of two concurrent clicks reaches the
// database first updates a row, and the loser matches nothing and gets false.
func (s *Store) MarkLoginTokenUsed(ctx context.Context, hash string, at time.Time) (bool, error) {
	const q = `UPDATE login_tokens SET used = ? WHERE hash = ? AND used = ''`

	res, err := s.db.ExecContext(ctx, q, formatTime(at), hash)
	if err != nil {
		return false, fmt.Errorf("sqlitedb: claiming login token: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("sqlitedb: claiming login token: %w", err)
	}

	return n == 1, nil
}

func (s *Store) SaveSession(ctx context.Context, sess identitybus.Session) error {
	const q = `INSERT INTO sessions (hash, user_id, created, expires) VALUES (?, ?, ?, ?)`

	if _, err := s.db.ExecContext(ctx, q,
		sess.Hash, sess.UserID.String(), formatTime(sess.Created), formatTime(sess.Expires),
	); err != nil {
		return fmt.Errorf("sqlitedb: saving session: %w", err)
	}

	return nil
}

func (s *Store) SessionByHash(ctx context.Context, hash string) (identitybus.Session, bool, error) {
	const q = `SELECT hash, user_id, created, expires FROM sessions WHERE hash = ?`

	var r dbSession
	err := s.db.QueryRowContext(ctx, q, hash).Scan(&r.Hash, &r.UserID, &r.Created, &r.Expires)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return identitybus.Session{}, false, nil
	case err != nil:
		return identitybus.Session{}, false, fmt.Errorf("sqlitedb: scanning session: %w", err)
	}

	sess, err := toBusSession(r)
	if err != nil {
		return identitybus.Session{}, false, fmt.Errorf("sqlitedb: session: %w", err)
	}

	return sess, true, nil
}

func (s *Store) DeleteSession(ctx context.Context, hash string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE hash = ?`, hash); err != nil {
		return fmt.Errorf("sqlitedb: deleting session: %w", err)
	}

	return nil
}

func (s *Store) DeleteExpired(ctx context.Context, now time.Time) error {
	stamp := formatTime(now)

	if _, err := s.db.ExecContext(ctx, `DELETE FROM login_tokens WHERE expires <= ?`, stamp); err != nil {
		return fmt.Errorf("sqlitedb: purging login tokens: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires <= ?`, stamp); err != nil {
		return fmt.Errorf("sqlitedb: purging sessions: %w", err)
	}

	return nil
}

// --- rows and converters ---------------------------------------------------

type dbUser struct {
	ID       string
	Email    string
	Verified int
	Created  string
	LastSeen string
}

type dbLoginToken struct {
	Hash    string
	UserID  string
	Created string
	Expires string
	Used    string
}

type dbSession struct {
	Hash    string
	UserID  string
	Created string
	Expires string
}

func toDBUser(u identitybus.User) dbUser {
	verified := 0
	if u.Verified {
		verified = 1
	}

	return dbUser{
		ID:       u.ID.String(),
		Email:    u.Email.String(),
		Verified: verified,
		Created:  formatTime(u.Created),
		LastSeen: formatTime(u.LastSeen),
	}
}

func toBusUser(r dbUser) (identitybus.User, error) {
	id, err := userid.Parse(r.ID)
	if err != nil {
		return identitybus.User{}, fmt.Errorf("id: %w", err)
	}

	addr, err := email.Parse(r.Email)
	if err != nil {
		return identitybus.User{}, fmt.Errorf("email: %w", err)
	}

	created, err := parseTime("created", r.Created)
	if err != nil {
		return identitybus.User{}, err
	}

	lastSeen, err := parseTime("last_seen", r.LastSeen)
	if err != nil {
		return identitybus.User{}, err
	}

	return identitybus.User{
		ID:       id,
		Email:    addr,
		Verified: r.Verified != 0,
		Created:  created,
		LastSeen: lastSeen,
	}, nil
}

func toBusLoginToken(r dbLoginToken) (identitybus.LoginToken, error) {
	id, err := userid.Parse(r.UserID)
	if err != nil {
		return identitybus.LoginToken{}, fmt.Errorf("user_id: %w", err)
	}

	created, err := parseTime("created", r.Created)
	if err != nil {
		return identitybus.LoginToken{}, err
	}

	expires, err := parseTime("expires", r.Expires)
	if err != nil {
		return identitybus.LoginToken{}, err
	}

	used, err := parseOptionalTime("used", r.Used)
	if err != nil {
		return identitybus.LoginToken{}, err
	}

	return identitybus.LoginToken{
		Hash: r.Hash, UserID: id, Created: created, Expires: expires, Used: used,
	}, nil
}

func toBusSession(r dbSession) (identitybus.Session, error) {
	id, err := userid.Parse(r.UserID)
	if err != nil {
		return identitybus.Session{}, fmt.Errorf("user_id: %w", err)
	}

	created, err := parseTime("created", r.Created)
	if err != nil {
		return identitybus.Session{}, err
	}

	expires, err := parseTime("expires", r.Expires)
	if err != nil {
		return identitybus.Session{}, err
	}

	return identitybus.Session{Hash: r.Hash, UserID: id, Created: created, Expires: expires}, nil
}

func formatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

// formatOptionalTime encodes "not yet" as the empty string rather than as a zero
// timestamp, so the `used = ”` predicate in MarkLoginTokenUsed reads as the
// obvious thing it is.
func formatOptionalTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}

	return formatTime(t)
}

func parseTime(column, s string) (time.Time, error) {
	t, err := time.Parse(timeLayout, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s: %w", column, err)
	}

	return t.UTC(), nil
}

func parseOptionalTime(column, s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}

	return parseTime(column, s)
}
