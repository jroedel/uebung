// Package sqlitedb is a studybus.Storer backed by SQLite.
//
// It is the shipping store. Unlike the JSON file it replaced, a Save touches one
// row instead of rewriting the whole document, the (user, lang, lemma) key is
// enforced by the database rather than by a Go map, and a corrupt half-write is
// impossible because every Save is a single atomic statement. The driver is
// modernc.org/sqlite — a pure-Go implementation, so there is no CGO and no C
// toolchain in the build, and `CGO_ENABLED=0` cross-compiles still work.
//
// Rows here are the Storage edge: dbProgress holds natives and strings only —
// CardState is stored as its text name, never as an enum int whose meaning
// depends on iota order — and toBusProgress/toDBProgress are the converters
// across the Storage↔Business boundary, with toBusProgress returning an error
// when stored text fails to parse back into a strong type.
//
// Times are stored as RFC3339Nano text in UTC rather than as an integer, so a
// row is readable in a sqlite3 shell and the zero time is the recognisable
// 0001-01-01T00:00:00Z rather than a large negative number. One consequence
// worth knowing: a time written in a local zone comes back in UTC. It is the
// same instant, so time.Time.Equal is unaffected, but == and reflect.DeepEqual
// on a Progress will not match across a round-trip.
package sqlitedb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jroedel/uebung/business/domain/study/studybus"
	"github.com/jroedel/uebung/business/types/cardstate"
	"github.com/jroedel/uebung/business/types/langcode"
	"github.com/jroedel/uebung/business/types/userid"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// timeLayout is how the two timestamp columns are encoded. RFC3339Nano drops
// trailing zeros in the fractional part, which loses no information — the parsed
// instant is identical — and keeps same-second values in correct byte order.
const timeLayout = time.RFC3339Nano

// schema is applied on every Open. It is idempotent, so opening an existing
// database is the same code path as creating a new one.
//
// STRICT makes SQLite reject a value of the wrong type instead of silently
// coercing it, so a bug in a converter fails loudly at the write rather than
// surfacing as unparseable text on some later read.
//
// The primary key is the natural (user, lang, lemma) triple that studybus.Storer
// is phrased in — there is no synthetic id because a card simply is one user's
// memory of one lemma. List filters on (user_id, lang), a prefix of that key, so
// it is already index-served and needs no second index.
const schema = `
CREATE TABLE IF NOT EXISTS study_progress (
	user_id     TEXT    NOT NULL,
	lang        TEXT    NOT NULL,
	lemma       TEXT    NOT NULL,
	stability   REAL    NOT NULL,
	difficulty  REAL    NOT NULL,
	state       TEXT    NOT NULL,
	reps        INTEGER NOT NULL,
	lapses      INTEGER NOT NULL,
	last_review TEXT    NOT NULL,
	due         TEXT    NOT NULL,
	PRIMARY KEY (user_id, lang, lemma)
) STRICT;
`

// columns is the select list, ordered to match dbProgress field order and the
// scan in scanProgress. Naming them explicitly rather than using * means adding
// a column later cannot silently break the scan.
const columns = `user_id, lang, lemma, stability, difficulty, state, reps, lapses, last_review, due`

// dbProgress is one card as persisted: natives and strings only, no strong
// types. This is what SQLite sees.
type dbProgress struct {
	User       string
	Lang       string
	Lemma      string
	Stability  float64
	Difficulty float64
	State      string
	Reps       int
	Lapses     int
	LastReview string
	Due        string
}

// Store is a SQLite-backed card store.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the database at path and applies the schema.
//
// WAL keeps a reader from blocking on the writer, and busy_timeout means a
// contended write waits rather than failing immediately with SQLITE_BUSY. The
// pool is capped at one connection: SQLite serialises writes anyway, and for a
// single learner's few hundred cards a serial pool removes lock contention as a
// class of bug at no cost worth measuring.
func Open(ctx context.Context, path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", path)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlitedb: opening %s: %w", path, err)
	}

	db.SetMaxOpenConns(1)

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlitedb: connecting to %s: %w", path, err)
	}

	if _, err := db.ExecContext(ctx, schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlitedb: applying schema to %s: %w", path, err)
	}

	return &Store{db: db}, nil
}

// Close releases the underlying database handle.
func (s *Store) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("sqlitedb: closing: %w", err)
	}

	return nil
}

// List returns every card for a user and language, converted to Business models.
func (s *Store) List(ctx context.Context, user userid.UserID, lang langcode.LangCode) ([]studybus.Progress, error) {
	const q = `SELECT ` + columns + ` FROM study_progress WHERE user_id = ? AND lang = ?`

	rows, err := s.db.QueryContext(ctx, q, user.String(), lang.String())
	if err != nil {
		return nil, fmt.Errorf("sqlitedb: listing progress: %w", err)
	}
	defer rows.Close()

	var out []studybus.Progress
	for rows.Next() {
		r, err := scanProgress(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlitedb: scanning progress: %w", err)
		}

		p, err := toBusProgress(r)
		if err != nil {
			return nil, fmt.Errorf("sqlitedb: card %q: %w", r.Lemma, err)
		}

		out = append(out, p)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlitedb: listing progress: %w", err)
	}

	return out, nil
}

// Get returns one card and whether it existed. A card the user has never
// reviewed is not an error, so a missing row reports false with a nil error.
func (s *Store) Get(ctx context.Context, user userid.UserID, lang langcode.LangCode, lemma string) (studybus.Progress, bool, error) {
	const q = `SELECT ` + columns + ` FROM study_progress WHERE user_id = ? AND lang = ? AND lemma = ?`

	r, err := scanProgress(s.db.QueryRowContext(ctx, q, user.String(), lang.String(), lemma))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return studybus.Progress{}, false, nil
	case err != nil:
		return studybus.Progress{}, false, fmt.Errorf("sqlitedb: loading card %q: %w", lemma, err)
	}

	p, err := toBusProgress(r)
	if err != nil {
		return studybus.Progress{}, false, fmt.Errorf("sqlitedb: card %q: %w", lemma, err)
	}

	return p, true, nil
}

// Save writes a card, creating or replacing it by its (user, lang, lemma) key.
// The upsert is one statement, so a card is never briefly absent the way a
// delete-then-insert would leave it.
func (s *Store) Save(ctx context.Context, p studybus.Progress) error {
	const q = `
INSERT INTO study_progress (` + columns + `)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (user_id, lang, lemma) DO UPDATE SET
	stability   = excluded.stability,
	difficulty  = excluded.difficulty,
	state       = excluded.state,
	reps        = excluded.reps,
	lapses      = excluded.lapses,
	last_review = excluded.last_review,
	due         = excluded.due`

	r := toDBProgress(p)

	_, err := s.db.ExecContext(ctx, q,
		r.User, r.Lang, r.Lemma,
		r.Stability, r.Difficulty, r.State,
		r.Reps, r.Lapses, r.LastReview, r.Due,
	)
	if err != nil {
		return fmt.Errorf("sqlitedb: saving card %q: %w", p.Lemma, err)
	}

	return nil
}

// rowScanner is what *sql.Row and *sql.Rows have in common, so the single-row
// and multi-row reads share one scan and cannot drift apart.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanProgress reads one row into the storage struct. It does no parsing —
// turning these natives into strong types is toBusProgress's job.
func scanProgress(sc rowScanner) (dbProgress, error) {
	var r dbProgress

	err := sc.Scan(
		&r.User, &r.Lang, &r.Lemma,
		&r.Stability, &r.Difficulty, &r.State,
		&r.Reps, &r.Lapses, &r.LastReview, &r.Due,
	)
	if err != nil {
		return dbProgress{}, err
	}

	return r, nil
}

// toDBProgress flattens a Business Progress to a storage row, turning strong
// types into their string forms and times into UTC text.
func toDBProgress(p studybus.Progress) dbProgress {
	return dbProgress{
		User:       p.User.String(),
		Lang:       p.Lang.String(),
		Lemma:      p.Lemma,
		Stability:  p.Stability,
		Difficulty: p.Difficulty,
		State:      p.State.String(),
		Reps:       p.Reps,
		Lapses:     p.Lapses,
		LastReview: formatTime(p.LastReview),
		Due:        formatTime(p.Due),
	}
}

// toBusProgress parses a storage row back into a Business Progress, validating
// every stored string into its strong type and returning an error if the stored
// data has been corrupted into something unparseable.
func toBusProgress(r dbProgress) (studybus.Progress, error) {
	user, err := userid.Parse(r.User)
	if err != nil {
		return studybus.Progress{}, fmt.Errorf("user: %w", err)
	}

	lang, err := langcode.Parse(r.Lang)
	if err != nil {
		return studybus.Progress{}, fmt.Errorf("lang: %w", err)
	}

	state, err := cardstate.Parse(r.State)
	if err != nil {
		return studybus.Progress{}, fmt.Errorf("state: %w", err)
	}

	if r.Lemma == "" {
		return studybus.Progress{}, fmt.Errorf("empty lemma")
	}

	lastReview, err := parseTime("last_review", r.LastReview)
	if err != nil {
		return studybus.Progress{}, err
	}

	due, err := parseTime("due", r.Due)
	if err != nil {
		return studybus.Progress{}, err
	}

	return studybus.Progress{
		User:       user,
		Lang:       lang,
		Lemma:      r.Lemma,
		Stability:  r.Stability,
		Difficulty: r.Difficulty,
		State:      state,
		Reps:       r.Reps,
		Lapses:     r.Lapses,
		LastReview: lastReview,
		Due:        due,
	}, nil
}

// formatTime encodes an instant for storage. Normalising to UTC first means the
// stored text of a given instant does not depend on the writer's zone.
func formatTime(t time.Time) string {
	return t.UTC().Format(timeLayout)
}

// parseTime decodes a stored timestamp, naming the column in the error so a
// corrupt row says which of the two is wrong.
func parseTime(column, s string) (time.Time, error) {
	t, err := time.Parse(timeLayout, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s: %w", column, err)
	}

	return t.UTC(), nil
}
