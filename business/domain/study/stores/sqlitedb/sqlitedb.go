// Package sqlitedb is a studybus.Storer backed by SQLite.
//
// It is the shipping store. Unlike the JSON file it replaced, a Save touches one
// row instead of rewriting the whole document, the (user, lang, deck, item) key is
// enforced by the database rather than by a Go map, and a corrupt half-write is
// impossible because every Save is a single transaction. The driver is
// modernc.org/sqlite — a pure-Go implementation, so there is no CGO and no C
// toolchain in the build, and `CGO_ENABLED=0` cross-compiles still work.
//
// Two tables, and the difference between them is the point:
//
//   - study_progress is where each card stands now. One row per card, overwritten
//     by every review.
//   - study_review is what happened. One row per graded answer, never updated and
//     never deleted, so a change to how points are scored can be recomputed from
//     the history instead of being stranded by it.
//
// Rows here are the Storage edge: dbProgress and dbReview hold natives and strings
// only — CardState is stored as its text name, never as an enum int whose meaning
// depends on iota order — and the toBus*/toDB* pairs are the converters across the
// Storage↔Business boundary, with the toBus direction returning an error when
// stored text fails to parse back into a strong type.
//
// Times are stored as RFC3339Nano text in UTC rather than as an integer, so a row
// is readable in a sqlite3 shell and the zero time is the recognisable
// 0001-01-01T00:00:00Z rather than a large negative number. One consequence worth
// knowing: a time written in a local zone comes back in UTC. It is the same
// instant, so time.Time.Equal is unaffected, but == and reflect.DeepEqual on a
// Progress will not match across a round-trip.
package sqlitedb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jroedel/uebung/business/domain/study/studybus"
	"github.com/jroedel/uebung/business/types/cardstate"
	"github.com/jroedel/uebung/business/types/deckid"
	"github.com/jroedel/uebung/business/types/langcode"
	"github.com/jroedel/uebung/business/types/rating"
	"github.com/jroedel/uebung/business/types/roleanswer"
	"github.com/jroedel/uebung/business/types/userid"
)

// timeLayout is how the timestamp columns are encoded. RFC3339Nano drops trailing
// zeros in the fractional part, which loses no information — the parsed instant is
// identical — and keeps same-second values in correct byte order.
const timeLayout = time.RFC3339Nano

// schema is applied on every Open. It is idempotent, so opening an existing
// database is the same code path as creating a new one.
//
// STRICT makes SQLite reject a value of the wrong type instead of silently
// coercing it, so a bug in a converter fails loudly at the write rather than
// surfacing as unparseable text on some later read.
//
// study_progress's primary key is the natural (user, lang, deck, item) quadruple
// that studybus.Storer is phrased in — there is no synthetic id because a card
// simply is one user's memory of one item. List filters on (user_id, lang, deck),
// a prefix of that key, so it is already index-served and needs no second index.
//
// study_review has no primary key on purpose. It is a log: two identical answers
// at two different moments are two facts, and any natural key here would have to
// include the timestamp, which is a uniqueness constraint nobody wants enforced.
// SQLite's implicit rowid orders it. The two indexes serve the two questions the
// log exists to answer — "what has this learner done lately" (points, streaks) and
// "has this learner ever touched this deck" (whether to show the deck's
// introduction).
const schema = `
CREATE TABLE IF NOT EXISTS study_progress (
	user_id     TEXT    NOT NULL,
	lang        TEXT    NOT NULL,
	deck        TEXT    NOT NULL,
	item        TEXT    NOT NULL,
	stability   REAL    NOT NULL,
	difficulty  REAL    NOT NULL,
	state       TEXT    NOT NULL,
	reps        INTEGER NOT NULL,
	lapses      INTEGER NOT NULL,
	last_review TEXT    NOT NULL,
	due         TEXT    NOT NULL,
	PRIMARY KEY (user_id, lang, deck, item)
) STRICT;

CREATE TABLE IF NOT EXISTS study_review (
	user_id     TEXT    NOT NULL,
	lang        TEXT    NOT NULL,
	deck        TEXT    NOT NULL,
	item        TEXT    NOT NULL,
	rating      TEXT    NOT NULL,
	role_answer TEXT    NOT NULL,
	given       TEXT    NOT NULL DEFAULT '',
	answer_ms   INTEGER NOT NULL DEFAULT 0,
	reviewed_at TEXT    NOT NULL
) STRICT;

CREATE INDEX IF NOT EXISTS study_review_by_time ON study_review (user_id, reviewed_at);
CREATE INDEX IF NOT EXISTS study_review_by_deck ON study_review (user_id, lang, deck);
`

// reviewAnswerColumns are the two columns added after the log was already in
// service, in the order migrateReviewAddAnswer adds them.
//
// Each carries a DEFAULT so that the ALTER can fill the rows already there. Every
// one of those rows was written before the client reported either fact, and the
// defaults are exactly what the Business model reads as "not reported" — so a
// pre-migration review comes back saying it does not know what was answered,
// which is true, rather than claiming an empty answer at zero milliseconds.
var reviewAnswerColumns = []struct{ name, ddl string }{
	{"given", `ALTER TABLE study_review ADD COLUMN given TEXT NOT NULL DEFAULT ''`},
	{"answer_ms", `ALTER TABLE study_review ADD COLUMN answer_ms INTEGER NOT NULL DEFAULT 0`},
}

// progressColumns is the select list, ordered to match dbProgress field order and
// the scan in scanProgress. Naming them explicitly rather than using * means
// adding a column later cannot silently break the scan.
const progressColumns = `user_id, lang, deck, item, stability, difficulty, state, reps, lapses, last_review, due`

// reviewColumns is the insert list for the log, in dbReview field order.
const reviewColumns = `user_id, lang, deck, item, rating, role_answer, given, answer_ms, reviewed_at`

// dbProgress is one card as persisted: natives and strings only, no strong types.
// This is what SQLite sees.
type dbProgress struct {
	User       string
	Lang       string
	Deck       string
	Item       string
	Stability  float64
	Difficulty float64
	State      string
	Reps       int
	Lapses     int
	LastReview string
	Due        string
}

// dbReview is one logged answer as persisted.
//
// Given is a plain string and AnswerMS a plain integer, both carrying the empty
// value for "the client did not report this". Neither is nullable: the two facts
// arrive together with the grade or not at all, there is nothing a NULL would say
// that an empty string and a zero do not, and a nullable column here would put a
// sql.Null wrapper on the read path of every row in the log's hottest table to
// encode a distinction nobody can act on.
type dbReview struct {
	User       string
	Lang       string
	Deck       string
	Item       string
	Rating     string
	RoleAnswer string
	Given      string
	AnswerMS   int64
	ReviewedAt string
}

// dbConfusion is one grouped tally as the database returns it: a card, an answer
// it drew, and how many times.
type dbConfusion struct {
	Item  string
	Given string
	Count int
}

// Store is a SQLite-backed card store.
type Store struct {
	db *sql.DB
}

// Open brings the study schema up to date on an already-open database and returns
// the store. It takes a *sql.DB rather than a path because identity and progress
// live in the same file: opening a second handle to one SQLite database would put
// two pools behind the single-writer assumption this store relies on.
// foundation/sqldb owns the pragmas and the connection cap.
func Open(ctx context.Context, db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, errors.New("sqlitedb: a database handle is required")
	}

	if err := migrateProgressToDecks(ctx, db); err != nil {
		return nil, err
	}

	if _, err := db.ExecContext(ctx, schema); err != nil {
		return nil, fmt.Errorf("sqlitedb: applying study schema: %w", err)
	}

	// After the schema, not before: on a fresh database the CREATE above already
	// makes study_review with both columns, and this then finds nothing to do. On
	// an existing one the CREATE is the no-op and this is what brings the table
	// up to date.
	if err := migrateReviewAddAnswer(ctx, db); err != nil {
		return nil, err
	}

	return &Store{db: db}, nil
}

// Check reports whether the store can actually serve, and is what /healthz is
// wired to.
//
// It reads both tables through the same column lists the real queries use,
// because "can serve" and "is connected" are different questions and only the
// first one is worth probing. A ping proves a connection is alive and touches no
// table, so it answers 200 against a database whose schema this binary cannot
// use — which is exactly the state a rolled-back deploy leaves behind. deploy.sh
// then health-checks the rollback, gets its 200, and reports success while every
// study request 500s.
//
// LIMIT 1 keeps it cheap enough to poll, and an empty table is a pass: a fresh
// deployment has no cards and is perfectly healthy. The question being asked is
// whether the columns exist and are readable, not whether anyone has studied.
//
// See "Schema migrations" in DEPLOYING.md for the failure this exists to make
// loud rather than silent.
func (s *Store) Check(ctx context.Context) error {
	for _, q := range []string{
		`SELECT ` + progressColumns + ` FROM study_progress LIMIT 1`,
		`SELECT ` + reviewColumns + ` FROM study_review LIMIT 1`,
	} {
		rows, err := s.db.QueryContext(ctx, q)
		if err != nil {
			return fmt.Errorf("sqlitedb: health check: %w", err)
		}

		// Only the query has to succeed; the rows are not read. Closing is still
		// required — an unclosed *sql.Rows holds the single connection this store
		// is allowed, and a leak here would deadlock the app on its own probe.
		err = rows.Err()
		if cerr := rows.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return fmt.Errorf("sqlitedb: health check: %w", err)
		}
	}

	return nil
}

// migrateProgressToDecks rebuilds a pre-decks study_progress table, moving every
// existing row into the deck it was implicitly always in.
//
// Before decks the key was (user, lang, lemma), because there was one deck and its
// identity went without saying. SQLite cannot add a column to a primary key in
// place, so this is the documented rebuild — create, copy, drop, rename — rather
// than the guarded ALTER TABLE that a plain new column would need. All four
// statements run in one transaction: a crash mid-migration rolls back to the old
// table intact rather than leaving a half-copied deck behind.
//
// It is a no-op on a database that has no study_progress yet (Open's CREATE TABLE
// makes the current shape directly) and on one already carrying a deck column, so
// running it on every Open is safe.
func migrateProgressToDecks(ctx context.Context, db *sql.DB) error {
	legacy, err := hasLegacyProgressTable(ctx, db)
	if err != nil {
		return err
	}
	if !legacy {
		return nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlitedb: starting the deck migration: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// A leftover scratch table can only come from a failed run of an older build;
	// the transaction means this one cannot leave one behind.
	rebuild := `
DROP TABLE IF EXISTS study_progress_rebuild;

CREATE TABLE study_progress_rebuild (
	user_id     TEXT    NOT NULL,
	lang        TEXT    NOT NULL,
	deck        TEXT    NOT NULL,
	item        TEXT    NOT NULL,
	stability   REAL    NOT NULL,
	difficulty  REAL    NOT NULL,
	state       TEXT    NOT NULL,
	reps        INTEGER NOT NULL,
	lapses      INTEGER NOT NULL,
	last_review TEXT    NOT NULL,
	due         TEXT    NOT NULL,
	PRIMARY KEY (user_id, lang, deck, item)
) STRICT;

INSERT INTO study_progress_rebuild
	(user_id, lang, deck, item, stability, difficulty, state, reps, lapses, last_review, due)
SELECT
	user_id, lang, '` + legacyDeck + `', lemma, stability, difficulty, state, reps, lapses, last_review, due
FROM study_progress;

DROP TABLE study_progress;

ALTER TABLE study_progress_rebuild RENAME TO study_progress;
`

	if _, err := tx.ExecContext(ctx, rebuild); err != nil {
		return fmt.Errorf("sqlitedb: rebuilding study_progress for decks: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlitedb: committing the deck migration: %w", err)
	}

	return nil
}

// legacyDeck is the deck every pre-decks row belongs to: the German gender deck
// was the only one that existed, so that is what those rows have always been.
//
// Interpolated into the migration rather than bound as a parameter because it sits
// inside a multi-statement Exec, which takes no arguments. It is safe to
// interpolate precisely because deckid.Parse has already rejected everything that
// is not a lowercase slug — there is no quote to escape.
var legacyDeck = deckid.DerDieDas.String()

// hasLegacyProgressTable reports whether study_progress exists in the pre-decks
// shape. The test is the absence of a deck column rather than the presence of a
// lemma column, because "deck is missing" is exactly the condition the rebuild
// fixes and stays true no matter what else a future schema renames.
func hasLegacyProgressTable(ctx context.Context, db *sql.DB) (bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT name FROM pragma_table_info('study_progress')`)
	if err != nil {
		return false, fmt.Errorf("sqlitedb: inspecting study_progress: %w", err)
	}
	defer rows.Close()

	columns := 0
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return false, fmt.Errorf("sqlitedb: inspecting study_progress: %w", err)
		}

		columns++
		if name == "deck" {
			return false, nil
		}
	}

	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("sqlitedb: inspecting study_progress: %w", err)
	}

	// No columns at all means no such table, which is a fresh database.
	return columns > 0, nil
}

// migrateReviewAddAnswer adds the two answer columns to a study_review table that
// predates them.
//
// Unlike the deck migration this is a plain guarded ALTER rather than a rebuild:
// neither column belongs to a key, and SQLite adds a NOT NULL column with a
// DEFAULT in place. Each is added only if absent, so running it on every Open is a
// no-op once the table is current, and a database that has one column but not the
// other — a crash between the two statements — is completed on the next start.
//
// Deliberately not one transaction. Each ALTER is atomic on its own, they are
// independent, and there is no intermediate state worth rolling back: a table with
// `given` but not `answer_ms` is simply a table this function has more to do to.
//
// Existing rows take the defaults, which is the honest answer. Every review
// written before this migration happened without either fact being reported, and
// an empty answer at zero milliseconds is precisely how the Business model spells
// that.
func migrateReviewAddAnswer(ctx context.Context, db *sql.DB) error {
	have, err := reviewColumnSet(ctx, db)
	if err != nil {
		return err
	}

	// No columns at all means no such table. Open applies the schema before
	// calling this, so that can only be a database this store has not created —
	// and adding columns to a table that does not exist is not this function's
	// failure to report.
	if len(have) == 0 {
		return nil
	}

	for _, col := range reviewAnswerColumns {
		if have[col.name] {
			continue
		}

		if _, err := db.ExecContext(ctx, col.ddl); err != nil {
			return fmt.Errorf("sqlitedb: adding study_review.%s: %w", col.name, err)
		}
	}

	return nil
}

// reviewColumnSet reports which columns study_review currently has, empty when
// there is no such table.
func reviewColumnSet(ctx context.Context, db *sql.DB) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT name FROM pragma_table_info('study_review')`)
	if err != nil {
		return nil, fmt.Errorf("sqlitedb: inspecting study_review: %w", err)
	}
	defer rows.Close()

	have := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("sqlitedb: inspecting study_review: %w", err)
		}

		have[name] = true
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlitedb: inspecting study_review: %w", err)
	}

	return have, nil
}

// List returns every card for a user in one deck, converted to Business models.
func (s *Store) List(ctx context.Context, user userid.UserID, lang langcode.LangCode, deck deckid.DeckID) ([]studybus.Progress, error) {
	const q = `SELECT ` + progressColumns + ` FROM study_progress WHERE user_id = ? AND lang = ? AND deck = ?`

	rows, err := s.db.QueryContext(ctx, q, user.String(), lang.String(), deck.String())
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
			return nil, fmt.Errorf("sqlitedb: card %q: %w", r.Item, err)
		}

		out = append(out, p)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlitedb: listing progress: %w", err)
	}

	return out, nil
}

// Get returns one card and whether it existed. A card the user has never reviewed
// is not an error, so a missing row reports false with a nil error.
func (s *Store) Get(ctx context.Context, user userid.UserID, lang langcode.LangCode, deck deckid.DeckID, item string) (studybus.Progress, bool, error) {
	const q = `SELECT ` + progressColumns + ` FROM study_progress WHERE user_id = ? AND lang = ? AND deck = ? AND item = ?`

	r, err := scanProgress(s.db.QueryRowContext(ctx, q, user.String(), lang.String(), deck.String(), item))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return studybus.Progress{}, false, nil
	case err != nil:
		return studybus.Progress{}, false, fmt.Errorf("sqlitedb: loading card %q: %w", item, err)
	}

	p, err := toBusProgress(r)
	if err != nil {
		return studybus.Progress{}, false, fmt.Errorf("sqlitedb: card %q: %w", item, err)
	}

	return p, true, nil
}

// Save writes a card by its (user, lang, deck, item) key and appends the review
// that produced it, in one transaction. Either both land or neither does, which is
// what lets the log be read as a true history of the deck.
//
// The card itself is written with a single upsert, so it is never briefly absent
// the way a delete-then-insert would leave it.
func (s *Store) Save(ctx context.Context, p studybus.Progress, rev studybus.Review) error {
	const upsert = `
INSERT INTO study_progress (` + progressColumns + `)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (user_id, lang, deck, item) DO UPDATE SET
	stability   = excluded.stability,
	difficulty  = excluded.difficulty,
	state       = excluded.state,
	reps        = excluded.reps,
	lapses      = excluded.lapses,
	last_review = excluded.last_review,
	due         = excluded.due`

	const appendReview = `INSERT INTO study_review (` + reviewColumns + `) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`

	r := toDBProgress(p)
	v := toDBReview(rev)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlitedb: saving card %q: %w", p.Item, err)
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.ExecContext(ctx, upsert,
		r.User, r.Lang, r.Deck, r.Item,
		r.Stability, r.Difficulty, r.State,
		r.Reps, r.Lapses, r.LastReview, r.Due,
	)
	if err != nil {
		return fmt.Errorf("sqlitedb: saving card %q: %w", p.Item, err)
	}

	_, err = tx.ExecContext(ctx, appendReview,
		v.User, v.Lang, v.Deck, v.Item,
		v.Rating, v.RoleAnswer, v.Given, v.AnswerMS, v.ReviewedAt,
	)
	if err != nil {
		return fmt.Errorf("sqlitedb: logging the review of %q: %w", rev.Item, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlitedb: saving card %q: %w", p.Item, err)
	}

	return nil
}

// rowScanner is what *sql.Row and *sql.Rows have in common, so the single-row and
// multi-row reads share one scan and cannot drift apart.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanProgress reads one row into the storage struct. It does no parsing —
// turning these natives into strong types is toBusProgress's job.
func scanProgress(sc rowScanner) (dbProgress, error) {
	var r dbProgress

	err := sc.Scan(
		&r.User, &r.Lang, &r.Deck, &r.Item,
		&r.Stability, &r.Difficulty, &r.State,
		&r.Reps, &r.Lapses, &r.LastReview, &r.Due,
	)
	if err != nil {
		return dbProgress{}, err
	}

	return r, nil
}

// toDBProgress flattens a Business Progress to a storage row, turning strong types
// into their string forms and times into UTC text.
func toDBProgress(p studybus.Progress) dbProgress {
	return dbProgress{
		User:       p.User.String(),
		Lang:       p.Lang.String(),
		Deck:       p.Deck.String(),
		Item:       p.Item,
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

	deck, err := deckid.Parse(r.Deck)
	if err != nil {
		return studybus.Progress{}, fmt.Errorf("deck: %w", err)
	}

	state, err := cardstate.Parse(r.State)
	if err != nil {
		return studybus.Progress{}, fmt.Errorf("state: %w", err)
	}

	if r.Item == "" {
		return studybus.Progress{}, fmt.Errorf("empty item")
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
		Deck:       deck,
		Item:       r.Item,
		Stability:  r.Stability,
		Difficulty: r.Difficulty,
		State:      state,
		Reps:       r.Reps,
		Lapses:     r.Lapses,
		LastReview: lastReview,
		Due:        due,
	}, nil
}

// toDBReview flattens a Business Review to a log row.
//
// Answered is stored as whole milliseconds. The Business model carries a
// time.Duration, which is nanoseconds, and nothing here needs that resolution: an
// answer is a human pressing a button, the client measures in fractional
// milliseconds to begin with, and milliseconds keep the column an integer small
// enough to sum over a year of reviews without care. Truncation is toward zero, so
// a sub-millisecond duration — which no real answer is — would store as 0 and read
// back as "not reported".
func toDBReview(rev studybus.Review) dbReview {
	return dbReview{
		User:       rev.User.String(),
		Lang:       rev.Lang.String(),
		Deck:       rev.Deck.String(),
		Item:       rev.Item,
		Rating:     rev.Rating.String(),
		RoleAnswer: rev.Role.String(),
		Given:      rev.Given,
		AnswerMS:   rev.Answered.Milliseconds(),
		ReviewedAt: formatTime(rev.At),
	}
}

// toBusReview parses a log row back into a Business Review. Nothing reads the log
// yet — points and streaks are what it is being accumulated for — but the
// converter is written alongside its partner so the pair cannot drift, and so a
// row that cannot be read back is caught by the store's own tests today rather
// than by the feature that needs it later.
func toBusReview(r dbReview) (studybus.Review, error) {
	user, err := userid.Parse(r.User)
	if err != nil {
		return studybus.Review{}, fmt.Errorf("user: %w", err)
	}

	lang, err := langcode.Parse(r.Lang)
	if err != nil {
		return studybus.Review{}, fmt.Errorf("lang: %w", err)
	}

	deck, err := deckid.Parse(r.Deck)
	if err != nil {
		return studybus.Review{}, fmt.Errorf("deck: %w", err)
	}

	rat, err := rating.Parse(r.Rating)
	if err != nil {
		return studybus.Review{}, fmt.Errorf("rating: %w", err)
	}

	role, err := roleanswer.Parse(r.RoleAnswer)
	if err != nil {
		return studybus.Review{}, fmt.Errorf("role_answer: %w", err)
	}

	if r.Item == "" {
		return studybus.Review{}, fmt.Errorf("empty item")
	}

	at, err := parseTime("reviewed_at", r.ReviewedAt)
	if err != nil {
		return studybus.Review{}, err
	}

	// A negative answer time is a corrupt row rather than a slow learner. Nothing
	// can write one — Grade refuses it and the column is filled from a Duration —
	// so finding one means the file has been edited by hand or damaged, and the
	// averages this column exists to feed are better off refusing it than skewed
	// by it.
	if r.AnswerMS < 0 {
		return studybus.Review{}, fmt.Errorf("answer_ms: negative duration %d", r.AnswerMS)
	}

	return studybus.Review{
		User:     user,
		Lang:     lang,
		Deck:     deck,
		Item:     r.Item,
		Rating:   rat,
		Role:     role,
		Given:    r.Given,
		Answered: time.Duration(r.AnswerMS) * time.Millisecond,
		At:       at,
	}, nil
}

// Confusions counts how often each of a learner's cards in one deck drew each
// answer.
//
// The grouping is the database's. This reads an append-only log that grows with
// every swipe a learner ever makes, and the result is bounded by the deck's size
// times the number of answers it offers — a few hundred rows for a deck of two
// hundred cards. Pulling the log into Go to tally it would make the cost scale
// with how much someone has studied instead of with how much there is to say, and
// it is the same query whether they have answered a hundred cards or a hundred
// thousand.
//
// Reviews with no recorded answer are excluded rather than grouped under the empty
// string. They are reviews from before the client reported one, and admitting them
// would put a column of unattributable counts in the middle of every matrix built
// from this.
//
// study_review_by_deck covers the WHERE, which is what keeps this cheap enough to
// serve on a page load.
func (s *Store) Confusions(ctx context.Context, user userid.UserID, lang langcode.LangCode, deck deckid.DeckID) ([]studybus.Confusion, error) {
	const q = `
SELECT item, given, COUNT(*) AS n
FROM study_review
WHERE user_id = ? AND lang = ? AND deck = ? AND given <> ''
GROUP BY item, given
ORDER BY item, given`

	rows, err := s.db.QueryContext(ctx, q, user.String(), lang.String(), deck.String())
	if err != nil {
		return nil, fmt.Errorf("sqlitedb: counting answers in deck %q: %w", deck, err)
	}
	defer rows.Close()

	var out []studybus.Confusion
	for rows.Next() {
		var r dbConfusion
		if err := rows.Scan(&r.Item, &r.Given, &r.Count); err != nil {
			return nil, fmt.Errorf("sqlitedb: counting answers in deck %q: %w", deck, err)
		}

		out = append(out, toBusConfusion(r))
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlitedb: counting answers in deck %q: %w", deck, err)
	}

	return out, nil
}

// toBusConfusion lifts a grouped row into its Business form. There is nothing to
// parse: both fields are opaque keys in this domain, and the count is already a
// number. The converter exists anyway so that the crossing has a name, and so
// there is one place to change if either field ever gains a type.
func toBusConfusion(r dbConfusion) studybus.Confusion {
	return studybus.Confusion{
		Item:  r.Item,
		Given: r.Given,
		Count: r.Count,
	}
}

// formatTime encodes an instant for storage. Normalising to UTC first means the
// stored text of a given instant does not depend on the writer's zone.
func formatTime(t time.Time) string {
	return t.UTC().Format(timeLayout)
}

// parseTime decodes a stored timestamp, naming the column in the error so a
// corrupt row says which one is wrong.
func parseTime(column, s string) (time.Time, error) {
	t, err := time.Parse(timeLayout, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s: %w", column, err)
	}

	return t.UTC(), nil
}

// Reassign moves every row belonging to one learner to another, returning how many
// cards moved.
//
// It exists for exactly one migration: the deck built up before accounts existed
// is keyed to the built-in "local" learner, and without this it would be orphaned
// the moment sign-in became mandatory — you would log in and find a fresh deck,
// with months of FSRS scheduling still in the database but unreachable.
//
// Deliberately not part of studybus.Storer. Bulk re-keying is an operational act,
// not part of scheduling, and a business port that offers "change who owns these
// rows" invites it being called from a request handler.
func (s *Store) Reassign(ctx context.Context, from, to userid.UserID) (int64, error) {
	if from.IsZero() || to.IsZero() {
		return 0, errors.New("sqlitedb: reassign needs both a source and a target learner")
	}

	// A row for (to, lang, deck, item) may already exist, and the primary key would
	// reject the update. OR IGNORE skips those, leaving the target's own progress as
	// the winner: the account someone is actually using should not be overwritten by
	// an older anonymous deck.
	const moveProgress = `UPDATE OR IGNORE study_progress SET user_id = ? WHERE user_id = ?`

	// The log moves in full, including the reviews of cards whose progress was left
	// behind above. Those reviews are still things this person did — the claim is a
	// statement that the anonymous deck was always theirs — and the log is a history
	// rather than a key, so nothing collides.
	const moveReviews = `UPDATE study_review SET user_id = ? WHERE user_id = ?`

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("sqlitedb: reassigning progress: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, moveProgress, to.String(), from.String())
	if err != nil {
		return 0, fmt.Errorf("sqlitedb: reassigning progress: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("sqlitedb: reassigning progress: %w", err)
	}

	if _, err := tx.ExecContext(ctx, moveReviews, to.String(), from.String()); err != nil {
		return 0, fmt.Errorf("sqlitedb: reassigning the review log: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("sqlitedb: reassigning progress: %w", err)
	}

	return n, nil
}

// CountFor reports how many progress rows a learner owns, so a migration can say
// what it is about to move and what it moved.
func (s *Store) CountFor(ctx context.Context, user userid.UserID) (int64, error) {
	var n int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM study_progress WHERE user_id = ?`, user.String()).Scan(&n); err != nil {
		return 0, fmt.Errorf("sqlitedb: counting progress: %w", err)
	}

	return n, nil
}
