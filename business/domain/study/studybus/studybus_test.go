package studybus_test

import (
	"context"
	"testing"
	"time"

	"github.com/jroedel/uebung/business/domain/study/stores/memdb"
	"github.com/jroedel/uebung/business/domain/study/studybus"
	"github.com/jroedel/uebung/business/types/langcode"
	"github.com/jroedel/uebung/business/types/rating"
	"github.com/jroedel/uebung/business/types/userid"
	"github.com/jroedel/uebung/foundation/fsrs"
)

var (
	anchor = time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	de     = langcode.German
)

func newBusiness(t *testing.T, maxNew int) (*studybus.Business, userid.UserID) {
	t.Helper()
	cfg := studybus.Config{FSRS: fsrs.Default(), MaxNewPerBatch: maxNew}
	return studybus.NewBusiness(memdb.New(), cfg), userid.Local()
}

func TestGradeNewLemmaCreatesReviewedCard(t *testing.T) {
	b, user := newBusiness(t, 20)
	ctx := context.Background()

	got, err := b.Grade(ctx, user, de, "Haus", rating.Good, anchor)
	if err != nil {
		t.Fatalf("Grade: %v", err)
	}
	if got.IsNew() {
		t.Fatal("card should no longer be new after grading")
	}
	if got.Reps != 1 {
		t.Fatalf("Reps = %d, want 1", got.Reps)
	}
	if !got.Due.After(anchor) {
		t.Fatalf("Due = %v, want after review time", got.Due)
	}

	// It must have been persisted.
	all, err := b.Progress(ctx, user, de)
	if err != nil {
		t.Fatalf("Progress: %v", err)
	}
	if len(all) != 1 || all[0].Lemma != "Haus" {
		t.Fatalf("stored progress = %+v, want one Haus card", all)
	}
}

func TestGradeRejectsInvalidRating(t *testing.T) {
	b, user := newBusiness(t, 20)
	if _, err := b.Grade(context.Background(), user, de, "Haus", rating.Rating(0), anchor); err == nil {
		t.Fatal("expected an error for an invalid rating")
	}
}

func TestBatchIntroducesNewCardsCappedAndInOrder(t *testing.T) {
	b, user := newBusiness(t, 3)
	deck := []string{"Mann", "Zeit", "Frau", "Tag", "Leben"}

	batch, err := b.Batch(context.Background(), user, de, deck, anchor, 10)
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}
	if len(batch) != 3 {
		t.Fatalf("batch size = %d, want 3 (maxNew cap)", len(batch))
	}
	want := []string{"Mann", "Zeit", "Frau"}
	for i, lemma := range want {
		if batch[i] != lemma {
			t.Fatalf("batch[%d] = %q, want %q (deck order)", i, batch[i], lemma)
		}
	}
}

func TestBatchRespectsOverallLimit(t *testing.T) {
	b, user := newBusiness(t, 20)
	deck := []string{"Mann", "Zeit", "Frau", "Tag", "Leben"}

	batch, err := b.Batch(context.Background(), user, de, deck, anchor, 2)
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}
	if len(batch) != 2 {
		t.Fatalf("batch size = %d, want 2 (limit)", len(batch))
	}
}

func TestBatchPrioritisesDueReviewsOverNewCards(t *testing.T) {
	b, user := newBusiness(t, 20)
	ctx := context.Background()
	deck := []string{"Mann", "Zeit", "Frau", "Tag", "Leben"}

	// Seed a graded card, then make it due by asking for a batch well after its Due.
	graded, err := b.Grade(ctx, user, de, "Tag", rating.Good, anchor)
	if err != nil {
		t.Fatalf("Grade: %v", err)
	}
	after := graded.Due.AddDate(0, 0, 1) // one day past due

	batch, err := b.Batch(ctx, user, de, deck, after, 3)
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}
	if len(batch) == 0 || batch[0] != "Tag" {
		t.Fatalf("batch = %v, want the due review \"Tag\" first", batch)
	}
	// A card already introduced must not reappear as a new card.
	seen := map[string]int{}
	for _, l := range batch {
		seen[l]++
	}
	if seen["Tag"] != 1 {
		t.Fatalf("due card appeared %d times, want exactly 1", seen["Tag"])
	}
}

func TestGradeAgainSchedulesSoonerThanGood(t *testing.T) {
	b, user := newBusiness(t, 20)
	ctx := context.Background()

	again, err := b.Grade(ctx, user, de, "Angst", rating.Again, anchor)
	if err != nil {
		t.Fatalf("Grade again: %v", err)
	}
	good, err := b.Grade(ctx, user, de, "Freund", rating.Good, anchor)
	if err != nil {
		t.Fatalf("Grade good: %v", err)
	}
	if !again.Due.Before(good.Due) {
		t.Fatalf("Again Due %v should be sooner than Good Due %v", again.Due, good.Due)
	}
}

func TestBatchRequiresUserAndLanguage(t *testing.T) {
	b, _ := newBusiness(t, 20)
	if _, err := b.Batch(context.Background(), userid.UserID{}, de, []string{"Mann"}, anchor, 5); err == nil {
		t.Fatal("expected an error for a zero user")
	}
	if _, err := b.Batch(context.Background(), userid.Local(), langcode.LangCode{}, []string{"Mann"}, anchor, 5); err == nil {
		t.Fatal("expected an error for a zero language")
	}
}
