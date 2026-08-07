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
	"github.com/jroedel/uebung/business/types/nickname"
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

// nicknameColumns are added to users after the fact, because the table above
// already exists in every deployment.
//
// SQLite has no ADD COLUMN IF NOT EXISTS, so addNicknameColumns consults
// PRAGMA table_info first. Both are declared with a default of ” rather than
// as nullable: an account without a name has one specific state, and having it
// be either NULL or ” depending on whether the row predates this change is a
// distinction nothing downstream wants to care about.
const nicknameSchema = `
ALTER TABLE users ADD COLUMN nickname TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN nickname_fold TEXT NOT NULL DEFAULT '';
`

// The uniqueness index is partial — WHERE nickname_fold <> ” — and that is the
// whole reason this works on a populated database. Every existing row has an
// empty fold, so a plain UNIQUE index would find them all in conflict with each
// other and refuse to be created.
//
// It is built on the folded form rather than on the displayed one, so that names
// differing only in case or separators cannot both exist. See nickname.Fold.
const nicknameIndex = `
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_nickname_fold
	ON users(nickname_fold) WHERE nickname_fold <> '';
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

	if err := addNicknameColumns(ctx, db); err != nil {
		return nil, err
	}

	if _, err := db.ExecContext(ctx, nicknameIndex); err != nil {
		return nil, fmt.Errorf("sqlitedb: creating nickname index: %w", err)
	}

	return &Store{db: db}, nil
}

// addNicknameColumns brings an existing users table up to date, and does nothing
// on one that is already current.
//
// The check is a read of PRAGMA table_info rather than an ALTER that tolerates
// its own failure. Swallowing the "duplicate column name" error would work, but
// it would also swallow every other reason an ALTER can fail, and a migration
// that cannot tell "already done" from "went wrong" is one that reports success
// on a half-changed table.
func addNicknameColumns(ctx context.Context, db *sql.DB) error {
	has, err := hasColumn(ctx, db, "users", "nickname")
	if err != nil {
		return err
	}
	if has {
		return nil
	}

	if _, err := db.ExecContext(ctx, nicknameSchema); err != nil {
		return fmt.Errorf("sqlitedb: adding nickname columns: %w", err)
	}

	return nil
}

func hasColumn(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	// PRAGMA does not accept a bound parameter for the table name. The value is
	// a constant from this package rather than anything caller-supplied, so
	// there is nothing here to inject.
	rows, err := db.QueryContext(ctx, `SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return false, fmt.Errorf("sqlitedb: reading %s columns: %w", table, err)
	}
	defer rows.Close()

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return false, fmt.Errorf("sqlitedb: reading %s columns: %w", table, err)
		}
		if name == column {
			return true, nil
		}
	}

	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("sqlitedb: reading %s columns: %w", table, err)
	}

	return false, nil
}

const userColumns = `id, email, nickname, verified, created, last_seen`

func (s *Store) UserByEmail(ctx context.Context, addr email.Email) (identitybus.User, bool, error) {
	const q = `SELECT ` + userColumns + ` FROM users WHERE email = ?`

	return s.scanUser(s.db.QueryRowContext(ctx, q, addr.String()))
}

func (s *Store) UserByID(ctx context.Context, id userid.UserID) (identitybus.User, bool, error) {
	const q = `SELECT ` + userColumns + ` FROM users WHERE id = ?`

	return s.scanUser(s.db.QueryRowContext(ctx, q, id.String()))
}

// UserByNickname looks an account up by the folded form of a display name, which
// is the same key the unique index is built on — so this finds exactly the rows
// that would refuse to coexist with name.
func (s *Store) UserByNickname(ctx context.Context, name nickname.Nickname) (identitybus.User, bool, error) {
	const q = `SELECT ` + userColumns + ` FROM users WHERE nickname_fold = ?`

	return s.scanUser(s.db.QueryRowContext(ctx, q, name.Fold()))
}

// SetNickname claims a display name in one statement.
//
// The NOT EXISTS subquery is the uniqueness decision, and it excludes the
// account doing the claiming — so re-spelling a name you already hold succeeds
// instead of colliding with your own row. The unique index behind it means that
// even if two of these run at once, only one can commit; the subquery is what
// turns the loser into a clean false rather than a constraint error the caller
// would have to parse a driver message to recognise.
func (s *Store) SetNickname(ctx context.Context, id userid.UserID, name nickname.Nickname) (bool, error) {
	const q = `
UPDATE users SET nickname = ?, nickname_fold = ?
WHERE id = ?
	AND NOT EXISTS (SELECT 1 FROM users WHERE nickname_fold = ? AND id <> ?)`

	res, err := s.db.ExecContext(ctx, q, name.String(), name.Fold(), id.String(), name.Fold(), id.String())
	if err != nil {
		return false, fmt.Errorf("sqlitedb: setting nickname: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("sqlitedb: setting nickname: %w", err)
	}

	return n == 1, nil
}

func (s *Store) scanUser(row *sql.Row) (identitybus.User, bool, error) {
	var r dbUser

	err := row.Scan(&r.ID, &r.Email, &r.Nickname, &r.Verified, &r.Created, &r.LastSeen)
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

// saveUserColumns is deliberately not userColumns: the nickname is absent from
// both halves of this statement.
//
// SetNickname owns that column, together with the folded key beside it that the
// unique index is built on. Writing the pair from here as well would mean two
// statements had to agree on deriving one from the other, and the sign-in path —
// which runs this on every redemption — would be one stale in-memory User away
// from clearing a name it was never asked to touch.
const saveUserColumns = `id, email, verified, created, last_seen`

func (s *Store) SaveUser(ctx context.Context, u identitybus.User) error {
	const q = `
INSERT INTO users (` + saveUserColumns + `) VALUES (?, ?, ?, ?, ?)
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
	ID    string
	Email string

	// Nickname is the display form. The folded key that sits beside it in the
	// table is derived on write and never read back, so it is not a field here:
	// it is an index key, not data.
	Nickname string

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
		Nickname: u.Nickname.String(),
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

	name, err := toBusNickname(r.Nickname)
	if err != nil {
		return identitybus.User{}, err
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
		Nickname: name,
		Verified: r.Verified != 0,
		Created:  created,
		LastSeen: lastSeen,
	}, nil
}

// toBusNickname parses the stored display name, treating the empty string as
// "not chosen yet" rather than as an error.
//
// That empty case is not a tolerance for bad data: it is the state every account
// is created in and the state every row predating this column is in, and the
// zero Nickname is exactly how the Business layer spells it. A non-empty value
// that will not parse is still an error, because it means something wrote a name
// that bypassed nickname.Parse.
func toBusNickname(s string) (nickname.Nickname, error) {
	if s == "" {
		return nickname.Nickname{}, nil
	}

	name, err := nickname.Parse(s)
	if err != nil {
		return nickname.Nickname{}, fmt.Errorf("nickname: %w", err)
	}

	return name, nil
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
