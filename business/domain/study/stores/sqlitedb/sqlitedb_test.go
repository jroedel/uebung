package sqlitedb_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/jroedel/uebung/business/domain/study/stores/sqlitedb"
	"github.com/jroedel/uebung/business/domain/study/studybus"
	"github.com/jroedel/uebung/business/types/cardstate"
	"github.com/jroedel/uebung/business/types/langcode"
	"github.com/jroedel/uebung/business/types/userid"
	"github.com/jroedel/uebung/foundation/sqldb"
)

func sampleCard() studybus.Progress {
	return studybus.Progress{
		User:       userid.Local(),
		Lang:       langcode.German,
		Lemma:      "Haus",
		Stability:  4.2,
		Difficulty: 5.1,
		State:      cardstate.Review,
		Reps:       3,
		Lapses:     1,
		LastReview: time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
		Due:        time.Date(2025, 1, 5, 12, 0, 0, 0, time.UTC),
	}
}

// open makes a store on a fresh temp database, closed when the test ends.
func open(t *testing.T) *sqlitedb.Store {
	t.Helper()

	return openAt(t, filepath.Join(t.TempDir(), "cards.db"))
}

func openAt(t *testing.T, path string) *sqlitedb.Store {
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

	s, err := sqlitedb.Open(t.Context(), db)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	return s
}

// assertSameCard compares field by field, using Time.Equal for the timestamps.
// Struct equality would be wrong here: the store normalises times to UTC on the
// way in, so a card written in a non-UTC zone comes back as the same instant in
// a different location and == would report a spurious mismatch.
func assertSameCard(t *testing.T, got, want studybus.Progress) {
	t.Helper()

	if got.User != want.User || got.Lang != want.Lang || got.Lemma != want.Lemma {
		t.Errorf("identity: got (%v,%v,%q), want (%v,%v,%q)",
			got.User, got.Lang, got.Lemma, want.User, want.Lang, want.Lemma)
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

func TestSavePersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cards.db")

	want := sampleCard()
	if err := openAt(t, path).Save(t.Context(), want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Reopen from disk: the card must come back in its strong form.
	got, ok, err := openAt(t, path).Get(t.Context(), want.User, want.Lang, want.Lemma)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("card missing after reopen")
	}

	assertSameCard(t, got, want)
}

func TestGetMissingCardIsNotAnError(t *testing.T) {
	_, ok, err := open(t).Get(t.Context(), userid.Local(), langcode.German, "Nichtvorhanden")
	if err != nil {
		t.Fatalf("Get on missing card errored: %v", err)
	}
	if ok {
		t.Fatal("missing card reported as found")
	}
}

func TestListFiltersByUserAndLang(t *testing.T) {
	s := open(t)

	if err := s.Save(t.Context(), sampleCard()); err != nil {
		t.Fatalf("Save: %v", err)
	}

	other := sampleCard()
	other.User = userid.MustParse("someone-else")
	other.Lemma = "Zeit"
	if err := s.Save(t.Context(), other); err != nil {
		t.Fatalf("Save other: %v", err)
	}

	got, err := s.List(t.Context(), userid.Local(), langcode.German)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].Lemma != "Haus" {
		t.Fatalf("List = %+v, want only Local's Haus card", got)
	}
}

// A second Save of the same card must replace it, not add a duplicate. This is
// the behaviour studybus.Grade depends on every single review.
func TestSaveReplacesExistingCard(t *testing.T) {
	s := open(t)

	first := sampleCard()
	if err := s.Save(t.Context(), first); err != nil {
		t.Fatalf("Save: %v", err)
	}

	updated := first
	updated.Reps = 4
	updated.Stability = 9.9
	updated.Due = first.Due.AddDate(0, 0, 7)
	if err := s.Save(t.Context(), updated); err != nil {
		t.Fatalf("re-Save: %v", err)
	}

	all, err := s.List(t.Context(), first.User, first.Lang)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("List returned %d cards, want 1 -- the upsert duplicated the row", len(all))
	}

	assertSameCard(t, all[0], updated)
}

// A card that has never been reviewed carries a zero LastReview. Storing times as
// text makes that the literal 0001-01-01T00:00:00Z, and it has to survive the
// round-trip as a zero time or IsNew-adjacent logic starts lying.
func TestZeroTimeRoundTrips(t *testing.T) {
	s := open(t)

	fresh := sampleCard()
	fresh.Lemma = "Neuling"
	fresh.State = cardstate.New
	fresh.Reps = 0
	fresh.Lapses = 0
	fresh.LastReview = time.Time{}

	if err := s.Save(t.Context(), fresh); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, ok, err := s.Get(t.Context(), fresh.User, fresh.Lang, fresh.Lemma)
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

	if err := s.Save(t.Context(), c); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, _, err := s.Get(t.Context(), c.User, c.Lang, c.Lemma)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.Due.Equal(c.Due) {
		t.Errorf("due came back as %v, want the same instant as %v", got.Due, c.Due)
	}
}
