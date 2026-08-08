package sqlitedb_test

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/jroedel/uebung/business/domain/study/stores/sqlitedb"
	"github.com/jroedel/uebung/business/domain/study/studybus"
	"github.com/jroedel/uebung/business/types/cardstate"
	"github.com/jroedel/uebung/business/types/deckid"
	"github.com/jroedel/uebung/business/types/langcode"
	"github.com/jroedel/uebung/business/types/rating"
	"github.com/jroedel/uebung/business/types/roleanswer"
	"github.com/jroedel/uebung/business/types/userid"
	"github.com/jroedel/uebung/foundation/sqldb"
)

var nouns = deckid.DerDieDas

func sampleCard() studybus.Progress {
	return studybus.Progress{
		User:       userid.Local(),
		Lang:       langcode.German,
		Deck:       nouns,
		Item:       "Haus",
		Stability:  4.2,
		Difficulty: 5.1,
		State:      cardstate.Review,
		Reps:       3,
		Lapses:     1,
		LastReview: time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
		Due:        time.Date(2025, 1, 5, 12, 0, 0, 0, time.UTC),
	}
}

// reviewFor builds the log entry that accompanies a card's save, the way
// studybus.Grade does.
func reviewFor(p studybus.Progress) studybus.Review {
	return studybus.Review{
		User:   p.User,
		Lang:   p.Lang,
		Deck:   p.Deck,
		Item:   p.Item,
		Rating: rating.Good,
		Role:   roleanswer.None,
		At:     p.LastReview,
	}
}

// save writes a card with a matching review, which is the only way the store is
// ever called in production.
func save(t *testing.T, s *sqlitedb.Store, p studybus.Progress) {
	t.Helper()

	if err := s.Save(t.Context(), p, reviewFor(p)); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

// open makes a store on a fresh temp database, closed when the test ends.
func open(t *testing.T) *sqlitedb.Store {
	t.Helper()

	return openAt(t, filepath.Join(t.TempDir(), "cards.db"))
}

func openAt(t *testing.T, path string) *sqlitedb.Store {
	t.Helper()

	s, _ := openWithDB(t, path)

	return s
}

// openWithDB also hands back the raw handle, for the tests that need to look at
// the tables directly or to set up a pre-migration database.
func openWithDB(t *testing.T, path string) (*sqlitedb.Store, *sql.DB) {
	t.Helper()

	db := rawDB(t, path)

	s, err := sqlitedb.Open(t.Context(), db)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	return s, db
}

// rawDB opens the database without applying any study schema.
func rawDB(t *testing.T, path string) *sql.DB {
	t.Helper()

	// The store no longer owns the handle: identity and study share one database,
	// so foundation/sqldb opens it and each store applies its own schema.
	db, err := sqldb.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("closing database: %v", err)
		}
	})

	return db
}

// assertSameCard compares field by field, using Time.Equal for the timestamps.
// Struct equality would be wrong here: the store normalises times to UTC on the
// way in, so a card written in a non-UTC zone comes back as the same instant in a
// different location and == would report a spurious mismatch.
func assertSameCard(t *testing.T, got, want studybus.Progress) {
	t.Helper()

	if got.User != want.User || got.Lang != want.Lang || got.Deck != want.Deck || got.Item != want.Item {
		t.Errorf("identity: got (%v,%v,%v,%q), want (%v,%v,%v,%q)",
			got.User, got.Lang, got.Deck, got.Item, want.User, want.Lang, want.Deck, want.Item)
	}
	if got.Stability != want.Stability || got.Difficulty != want.Difficulty {
		t.Errorf("scalars: got stability=%v difficulty=%v, want %v/%v",
			got.Stability, got.Difficulty, want.Stability, want.Difficulty)
	}
	if got.State != want.State || got.Reps != want.Reps || got.Lapses != want.Lapses {
		t.Errorf("counters: got state=%v reps=%d lapses=%d, want %v/%d/%d",
			got.State, got.Reps, got.Lapses, want.State, want.Reps, want.Lapses)
	}
	if !got.LastReview.Equal(want.LastReview) {
		t.Errorf("last review: got %v, want %v", got.LastReview, want.LastReview)
	}
	if !got.Due.Equal(want.Due) {
		t.Errorf("due: got %v, want %v", got.Due, want.Due)
	}
}

// countReviews reports how many log rows a card has.
func countReviews(t *testing.T, db *sql.DB, item string) int {
	t.Helper()

	var n int
	if err := db.QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM study_review WHERE item = ?`, item).Scan(&n); err != nil {
		t.Fatalf("counting reviews: %v", err)
	}

	return n
}

func TestSavePersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cards.db")

	want := sampleCard()
	save(t, openAt(t, path), want)

	// Reopen from disk: the card must come back in its strong form.
	got, ok, err := openAt(t, path).Get(t.Context(), want.User, want.Lang, want.Deck, want.Item)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("card missing after reopen")
	}

	assertSameCard(t, got, want)
}

func TestGetMissingCardIsNotAnError(t *testing.T) {
	_, ok, err := open(t).Get(t.Context(), userid.Local(), langcode.German, nouns, "Nichtvorhanden")
	if err != nil {
		t.Fatalf("Get on missing card errored: %v", err)
	}
	if ok {
		t.Fatal("missing card reported as found")
	}
}

func TestListFiltersByUserLangAndDeck(t *testing.T) {
	s := open(t)

	save(t, s, sampleCard())

	other := sampleCard()
	other.User = userid.MustParse("someone-else")
	other.Item = "Zeit"
	save(t, s, other)

	// Same user and language, different deck: must not appear either.
	elsewhere := sampleCard()
	elsewhere.Deck = deckid.MustParse("de-preps")
	elsewhere.Item = "mit"
	save(t, s, elsewhere)

	got, err := s.List(t.Context(), userid.Local(), langcode.German, nouns)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].Item != "Haus" {
		t.Fatalf("List = %+v, want only Local's Haus card in the noun deck", got)
	}
}

// The same item key in two decks is two cards. Without the deck in the primary
// key the second Save would overwrite the first.
func TestSameItemInTwoDecksIsTwoCards(t *testing.T) {
	s := open(t)

	inNouns := sampleCard()
	inNouns.Item = "mit"
	save(t, s, inNouns)

	inPreps := inNouns
	inPreps.Deck = deckid.MustParse("de-preps")
	inPreps.Reps = 99
	save(t, s, inPreps)

	got, ok, err := s.Get(t.Context(), inNouns.User, inNouns.Lang, nouns, "mit")
	if err != nil || !ok {
		t.Fatalf("Get in the noun deck: %v (found=%v)", err, ok)
	}
	if got.Reps != inNouns.Reps {
		t.Errorf("noun-deck card has Reps=%d, want %d -- the other deck overwrote it", got.Reps, inNouns.Reps)
	}
}

// A second Save of the same card must replace it, not add a duplicate. This is
// the behaviour studybus.Grade depends on every single review.
func TestSaveReplacesExistingCard(t *testing.T) {
	s, db := openWithDB(t, filepath.Join(t.TempDir(), "cards.db"))

	first := sampleCard()
	save(t, s, first)

	updated := first
	updated.Reps = 4
	updated.Stability = 9.9
	updated.Due = first.Due.AddDate(0, 0, 7)
	save(t, s, updated)

	all, err := s.List(t.Context(), first.User, first.Lang, first.Deck)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("List returned %d cards, want 1 -- the upsert duplicated the row", len(all))
	}

	assertSameCard(t, all[0], updated)

	// The card was replaced; its history was not.
	if n := countReviews(t, db, first.Item); n != 2 {
		t.Errorf("study_review holds %d rows for the card, want 2 -- the log must accumulate", n)
	}
}

// A card that has never been reviewed carries a zero LastReview. Storing times as
// text makes that the literal 0001-01-01T00:00:00Z, and it has to survive the
// round-trip as a zero time or IsNew-adjacent logic starts lying.
func TestZeroTimeRoundTrips(t *testing.T) {
	s := open(t)

	fresh := sampleCard()
	fresh.Item = "Neuling"
	fresh.State = cardstate.New
	fresh.Reps = 0
	fresh.Lapses = 0
	fresh.LastReview = time.Time{}

	save(t, s, fresh)

	got, ok, err := s.Get(t.Context(), fresh.User, fresh.Lang, fresh.Deck, fresh.Item)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("fresh card missing")
	}
	if !got.LastReview.IsZero() {
		t.Errorf("last review came back as %v, want the zero time", got.LastReview)
	}
	if !got.IsNew() {
		t.Error("card with zero reps did not report as new after a round-trip")
	}
}

// Times are normalised to UTC on write, so a card written in another zone must
// come back as the same instant.
func TestNonUTCTimeIsStoredAsTheSameInstant(t *testing.T) {
	s := open(t)

	zone := time.FixedZone("UTC+5", 5*60*60)
	c := sampleCard()
	c.Due = time.Date(2025, 3, 9, 8, 30, 0, 0, zone)

	save(t, s, c)

	got, _, err := s.Get(t.Context(), c.User, c.Lang, c.Deck, c.Item)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.Due.Equal(c.Due) {
		t.Errorf("due came back as %v, want the same instant as %v", got.Due, c.Due)
	}
}

// legacySchema is study_progress exactly as it shipped before decks existed. The
// migration test builds a real one rather than mocking it, because the thing under
// test is whether live data survives.
const legacySchema = `
CREATE TABLE study_progress (
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

// Check is what /healthz asks, and the whole reason it is not a ping is that a
// ping passes against a schema this binary cannot use. So the test that matters
// is the negative one: put the legacy schema in front of it and it must fail.
//
// If this ever starts passing, /healthz has gone back to proving only that a
// connection is alive, and a rolled-back deploy becomes silent again.
func TestCheckFailsAgainstASchemaTheBinaryCannotUse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "healthy.db")
	s, db := openWithDB(t, path)

	if err := s.Check(t.Context()); err != nil {
		t.Fatalf("Check on an empty but correct database: %v", err)
	}

	// An empty deck is healthy; so is one with cards in it.
	save(t, s, sampleCard())
	if err := s.Check(t.Context()); err != nil {
		t.Fatalf("Check with a card present: %v", err)
	}

	// A ping still passes here, which is exactly the problem being fixed.
	if err := db.PingContext(t.Context()); err != nil {
		t.Fatalf("ping on a healthy database: %v", err)
	}

	// Now put the pre-decks schema back under it, which is what a rolled-back
	// binary meets from the other direction: columns it queries are gone.
	for _, q := range []string{`DROP TABLE study_progress`, `DROP TABLE study_review`, legacySchema} {
		if _, err := db.ExecContext(t.Context(), q); err != nil {
			t.Fatalf("reverting the schema (%s): %v", q, err)
		}
	}

	if err := db.PingContext(t.Context()); err != nil {
		t.Fatal("the ping failed, so this test is no longer describing the case it was written for")
	}

	if err := s.Check(t.Context()); err == nil {
		t.Fatal("Check passed against a schema missing the columns the app queries -- " +
			"/healthz would report a rolled-back deploy as healthy")
	}
}

// A database written before decks existed must open, and every card in it must
// come back with its scheduling intact, in the deck it was always implicitly in.
// Getting this wrong resets a live learner's deck to zero.
func TestOpenMigratesAPreDecksDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db := rawDB(t, path)

	if _, err := db.ExecContext(t.Context(), legacySchema); err != nil {
		t.Fatalf("creating the legacy schema: %v", err)
	}

	old := sampleCard()
	_, err := db.ExecContext(t.Context(),
		`INSERT INTO study_progress (user_id, lang, lemma, stability, difficulty, state, reps, lapses, last_review, due)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		old.User.String(), old.Lang.String(), old.Item,
		old.Stability, old.Difficulty, old.State.String(),
		old.Reps, old.Lapses,
		old.LastReview.Format(time.RFC3339Nano), old.Due.Format(time.RFC3339Nano),
	)
	if err != nil {
		t.Fatalf("seeding a legacy row: %v", err)
	}

	s, err := sqlitedb.Open(t.Context(), db)
	if err != nil {
		t.Fatalf("Open on a legacy database: %v", err)
	}

	got, ok, err := s.Get(t.Context(), old.User, old.Lang, deckid.DerDieDas, old.Item)
	if err != nil {
		t.Fatalf("Get after migration: %v", err)
	}
	if !ok {
		t.Fatal("the migrated card is missing -- a live learner's deck would have been reset")
	}

	assertSameCard(t, got, old)
}

// Open runs on every start, so the migration has to be safe to re-run. The second
// pass must find nothing to do and leave the data alone.
func TestOpenIsIdempotentAfterMigrating(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db := rawDB(t, path)

	if _, err := db.ExecContext(t.Context(), legacySchema); err != nil {
		t.Fatalf("creating the legacy schema: %v", err)
	}

	old := sampleCard()
	if _, err := db.ExecContext(t.Context(),
		`INSERT INTO study_progress (user_id, lang, lemma, stability, difficulty, state, reps, lapses, last_review, due)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		old.User.String(), old.Lang.String(), old.Item,
		old.Stability, old.Difficulty, old.State.String(),
		old.Reps, old.Lapses,
		old.LastReview.Format(time.RFC3339Nano), old.Due.Format(time.RFC3339Nano),
	); err != nil {
		t.Fatalf("seeding a legacy row: %v", err)
	}

	for i := range 3 {
		if _, err := sqlitedb.Open(t.Context(), db); err != nil {
			t.Fatalf("Open #%d: %v", i+1, err)
		}
	}

	s, err := sqlitedb.Open(t.Context(), db)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	all, err := s.List(t.Context(), old.User, old.Lang, deckid.DerDieDas)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("after repeated opens the deck holds %d cards, want 1", len(all))
	}

	assertSameCard(t, all[0], old)
}

// A claim moves the anonymous deck to a real account. The review log has to travel
// with it, or the account inherits a deck with no history behind it.
func TestReassignMovesProgressAndTheLog(t *testing.T) {
	s, db := openWithDB(t, filepath.Join(t.TempDir(), "cards.db"))

	anon := sampleCard()
	save(t, s, anon)

	target := userid.MustParse("real-account")

	moved, err := s.Reassign(t.Context(), anon.User, target)
	if err != nil {
		t.Fatalf("Reassign: %v", err)
	}
	if moved != 1 {
		t.Fatalf("Reassign moved %d cards, want 1", moved)
	}

	all, err := s.List(t.Context(), target, anon.Lang, anon.Deck)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("target holds %d cards after the claim, want 1", len(all))
	}

	var owned int
	if err := db.QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM study_review WHERE user_id = ?`, target.String()).Scan(&owned); err != nil {
		t.Fatalf("counting the target's log: %v", err)
	}
	if owned != 1 {
		t.Errorf("target owns %d log rows after the claim, want 1 -- the history was left behind", owned)
	}
}
