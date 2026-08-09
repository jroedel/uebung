package studybus_test

import (
	"context"
	"testing"
	"time"

	"github.com/jroedel/uebung/business/domain/study/stores/memdb"
	"github.com/jroedel/uebung/business/domain/study/studybus"
	"github.com/jroedel/uebung/business/types/deckid"
	"github.com/jroedel/uebung/business/types/langcode"
	"github.com/jroedel/uebung/business/types/rating"
	"github.com/jroedel/uebung/business/types/roleanswer"
	"github.com/jroedel/uebung/business/types/userid"
	"github.com/jroedel/uebung/foundation/fsrs"
)

var (
	anchor = time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	de     = langcode.German
	nouns  = deckid.DerDieDas
)

func newBusiness(t *testing.T, maxNew int) (*studybus.Business, *memdb.Store, userid.UserID) {
	t.Helper()
	cfg := studybus.Config{FSRS: fsrs.Default(), MaxNewPerBatch: maxNew}
	store := memdb.New()

	return studybus.NewBusiness(store, cfg), store, userid.Local()
}

func TestGradeNewItemCreatesReviewedCard(t *testing.T) {
	b, _, user := newBusiness(t, 20)
	ctx := context.Background()

	got, err := b.Grade(ctx, user, de, nouns, studybus.GradeInput{Item: "Haus", Rating: rating.Good, Role: roleanswer.None}, anchor)
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
	all, err := b.Progress(ctx, user, de, nouns)
	if err != nil {
		t.Fatalf("Progress: %v", err)
	}
	if len(all) != 1 || all[0].Item != "Haus" {
		t.Fatalf("stored progress = %+v, want one Haus card", all)
	}
	if all[0].Deck != nouns {
		t.Fatalf("stored deck = %q, want %q", all[0].Deck, nouns)
	}
}

// Every grade must leave a row in the log. This is what points, streaks and the
// leaderboard will be computed from, and unlike Progress it is never overwritten,
// so a card graded twice must produce two entries and not one.
func TestGradeAppendsToTheReviewLog(t *testing.T) {
	b, store, user := newBusiness(t, 20)
	ctx := context.Background()

	if _, err := b.Grade(ctx, user, de, nouns, studybus.GradeInput{Item: "Haus", Rating: rating.Good, Role: roleanswer.None}, anchor); err != nil {
		t.Fatalf("first Grade: %v", err)
	}

	later := anchor.AddDate(0, 0, 3)
	if _, err := b.Grade(ctx, user, de, nouns, studybus.GradeInput{Item: "Haus", Rating: rating.Again, Role: roleanswer.None}, later); err != nil {
		t.Fatalf("second Grade: %v", err)
	}

	log := store.Reviews()
	if len(log) != 2 {
		t.Fatalf("review log holds %d entries, want 2 -- the log is a history, not a running total", len(log))
	}

	if log[0].Rating != rating.Good || log[1].Rating != rating.Again {
		t.Errorf("logged ratings = %v/%v, want good/again", log[0].Rating, log[1].Rating)
	}
	if !log[0].At.Equal(anchor) || !log[1].At.Equal(later) {
		t.Errorf("logged times = %v/%v, want %v/%v", log[0].At, log[1].At, anchor, later)
	}
	if log[0].Item != "Haus" || log[0].Deck != nouns || log[0].Lang != de || log[0].User != user {
		t.Errorf("logged identity = %+v, want the graded card's own", log[0])
	}
	if log[0].Role != roleanswer.None {
		t.Errorf("logged role = %v, want none for a deck that does not ask", log[0].Role)
	}
}

// The role half of a two-part card is recorded next to the grade, so a concept
// failure can later be told apart from a form failure.
func TestGradeRecordsTheRoleAnswer(t *testing.T) {
	b, store, user := newBusiness(t, 20)

	roles := deckid.MustParse("de-roles-basic")
	if _, err := b.Grade(context.Background(), user, de, roles, studybus.GradeInput{Item: "s0001", Rating: rating.Again, Role: roleanswer.Incorrect}, anchor); err != nil {
		t.Fatalf("Grade: %v", err)
	}

	log := store.Reviews()
	if len(log) != 1 {
		t.Fatalf("review log holds %d entries, want 1", len(log))
	}
	if log[0].Role != roleanswer.Incorrect {
		t.Errorf("logged role = %v, want incorrect", log[0].Role)
	}
}

// Two decks may legitimately use the same item key -- "mit" is a preposition in
// one deck and could be anything in another. The deck is part of the key, so they
// must be two independent cards with two independent schedules.
func TestDecksDoNotShareCards(t *testing.T) {
	b, _, user := newBusiness(t, 20)
	ctx := context.Background()

	preps := deckid.MustParse("de-preps")

	if _, err := b.Grade(ctx, user, de, nouns, studybus.GradeInput{Item: "mit", Rating: rating.Good, Role: roleanswer.None}, anchor); err != nil {
		t.Fatalf("Grade in the noun deck: %v", err)
	}

	inPreps, err := b.Progress(ctx, user, de, preps)
	if err != nil {
		t.Fatalf("Progress: %v", err)
	}
	if len(inPreps) != 0 {
		t.Fatalf("grading in one deck put %d card(s) in another: %+v", len(inPreps), inPreps)
	}

	// And a batch of the second deck must still offer the item as new.
	batch, err := b.Batch(ctx, user, de, preps, []string{"mit"}, anchor, 10)
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}
	if len(batch) != 1 || batch[0] != "mit" {
		t.Fatalf("batch = %v, want the untouched %q from the other deck", batch, "mit")
	}
}

func TestGradeRejectsInvalidRating(t *testing.T) {
	b, _, user := newBusiness(t, 20)
	if _, err := b.Grade(context.Background(), user, de, nouns, studybus.GradeInput{Item: "Haus", Rating: rating.Rating(0), Role: roleanswer.None}, anchor); err == nil {
		t.Fatal("expected an error for an invalid rating")
	}
}

func TestGradeRejectsInvalidRoleAnswer(t *testing.T) {
	b, _, user := newBusiness(t, 20)
	if _, err := b.Grade(context.Background(), user, de, nouns, studybus.GradeInput{Item: "Haus", Rating: rating.Good, Role: roleanswer.RoleAnswer(9)}, anchor); err == nil {
		t.Fatal("expected an error for an invalid role answer")
	}
}

func TestGradeRequiresADeck(t *testing.T) {
	b, _, user := newBusiness(t, 20)
	if _, err := b.Grade(context.Background(), user, de, deckid.DeckID{}, studybus.GradeInput{Item: "Haus", Rating: rating.Good, Role: roleanswer.None}, anchor); err == nil {
		t.Fatal("expected an error for a zero deck")
	}
}

func TestBatchIntroducesNewCardsCappedAndInOrder(t *testing.T) {
	b, _, user := newBusiness(t, 3)
	deck := []string{"Mann", "Zeit", "Frau", "Tag", "Leben"}

	batch, err := b.Batch(context.Background(), user, de, nouns, deck, anchor, 10)
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}
	if len(batch) != 3 {
		t.Fatalf("batch size = %d, want 3 (maxNew cap)", len(batch))
	}
	want := []string{"Mann", "Zeit", "Frau"}
	for i, item := range want {
		if batch[i] != item {
			t.Fatalf("batch[%d] = %q, want %q (deck order)", i, batch[i], item)
		}
	}
}

func TestBatchRespectsOverallLimit(t *testing.T) {
	b, _, user := newBusiness(t, 20)
	deck := []string{"Mann", "Zeit", "Frau", "Tag", "Leben"}

	batch, err := b.Batch(context.Background(), user, de, nouns, deck, anchor, 2)
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}
	if len(batch) != 2 {
		t.Fatalf("batch size = %d, want 2 (limit)", len(batch))
	}
}

func TestBatchPrioritisesDueReviewsOverNewCards(t *testing.T) {
	b, _, user := newBusiness(t, 20)
	ctx := context.Background()
	deck := []string{"Mann", "Zeit", "Frau", "Tag", "Leben"}

	// Seed a graded card, then make it due by asking for a batch well after its Due.
	graded, err := b.Grade(ctx, user, de, nouns, studybus.GradeInput{Item: "Tag", Rating: rating.Good, Role: roleanswer.None}, anchor)
	if err != nil {
		t.Fatalf("Grade: %v", err)
	}
	after := graded.Due.AddDate(0, 0, 1) // one day past due

	batch, err := b.Batch(ctx, user, de, nouns, deck, after, 3)
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
	b, _, user := newBusiness(t, 20)
	ctx := context.Background()

	again, err := b.Grade(ctx, user, de, nouns, studybus.GradeInput{Item: "Angst", Rating: rating.Again, Role: roleanswer.None}, anchor)
	if err != nil {
		t.Fatalf("Grade again: %v", err)
	}
	good, err := b.Grade(ctx, user, de, nouns, studybus.GradeInput{Item: "Freund", Rating: rating.Good, Role: roleanswer.None}, anchor)
	if err != nil {
		t.Fatalf("Grade good: %v", err)
	}
	if !again.Due.Before(good.Due) {
		t.Fatalf("Again Due %v should be sooner than Good Due %v", again.Due, good.Due)
	}
}

func TestBatchRequiresUserLanguageAndDeck(t *testing.T) {
	b, _, _ := newBusiness(t, 20)
	ctx := context.Background()

	if _, err := b.Batch(ctx, userid.UserID{}, de, nouns, []string{"Mann"}, anchor, 5); err == nil {
		t.Fatal("expected an error for a zero user")
	}
	if _, err := b.Batch(ctx, userid.Local(), langcode.LangCode{}, nouns, []string{"Mann"}, anchor, 5); err == nil {
		t.Fatal("expected an error for a zero language")
	}
	if _, err := b.Batch(ctx, userid.Local(), de, deckid.DeckID{}, []string{"Mann"}, anchor, 5); err == nil {
		t.Fatal("expected an error for a zero deck")
	}
}

// The answer a learner gave is what an error profile is built from, so it has to
// reach the log intact. A rating alone can only count mistakes.
func TestGradeRecordsTheAnswerAndItsTiming(t *testing.T) {
	ctx := t.Context()
	b, store, user := newBusiness(t, 20)

	in := studybus.GradeInput{
		Item:     "Haus",
		Rating:   rating.Again,
		Role:     roleanswer.None,
		Given:    "der",
		Answered: 2500 * time.Millisecond,
	}

	if _, err := b.Grade(ctx, user, de, nouns, in, anchor); err != nil {
		t.Fatalf("Grade: %v", err)
	}

	log := store.Reviews()
	if len(log) != 1 {
		t.Fatalf("logged reviews: got %d, want 1", len(log))
	}

	if log[0].Given != "der" {
		t.Errorf("given: got %q, want %q", log[0].Given, "der")
	}
	if log[0].Answered != 2500*time.Millisecond {
		t.Errorf("answered: got %v, want 2.5s", log[0].Answered)
	}
}

// Neither new field takes part in scheduling. FSRS sees the rating and nothing
// else, so two identical grades must advance a card identically however fast they
// were answered -- otherwise the diagnostic would be quietly changing the course.
func TestAnswerTimingDoesNotAffectScheduling(t *testing.T) {
	ctx := t.Context()

	graded := func(in studybus.GradeInput) studybus.Progress {
		t.Helper()

		b, _, user := newBusiness(t, 20)
		p, err := b.Grade(ctx, user, de, nouns, in, anchor)
		if err != nil {
			t.Fatalf("Grade: %v", err)
		}

		return p
	}

	quick := graded(studybus.GradeInput{Item: "Haus", Rating: rating.Good, Given: "das", Answered: 300 * time.Millisecond})
	slow := graded(studybus.GradeInput{Item: "Haus", Rating: rating.Good, Given: "das", Answered: 9 * time.Second})

	if !quick.Due.Equal(slow.Due) || quick.Stability != slow.Stability {
		t.Errorf("timing changed the schedule: quick due=%v stability=%v, slow due=%v stability=%v",
			quick.Due, quick.Stability, slow.Due, slow.Stability)
	}
}

// A negative duration is a clock that went backwards or a subtraction the wrong
// way round. It is not an answer time, and letting one into an append-only log
// would skew every average taken over it afterwards.
func TestGradeRejectsANegativeAnswerTime(t *testing.T) {
	b, _, user := newBusiness(t, 20)

	in := studybus.GradeInput{
		Item:     "Haus",
		Rating:   rating.Good,
		Answered: -time.Second,
	}

	if _, err := b.Grade(t.Context(), user, de, nouns, in, anchor); err == nil {
		t.Fatal("a negative answer time was accepted")
	}
}

// Confusions is scoped like every other question in this domain. Asking without a
// deck is a caller mistake, not an empty result.
func TestConfusionsRequiresAScope(t *testing.T) {
	b, _, user := newBusiness(t, 20)

	if _, err := b.Confusions(t.Context(), user, de, deckid.DeckID{}); err == nil {
		t.Fatal("Confusions accepted a zero deck")
	}
}
