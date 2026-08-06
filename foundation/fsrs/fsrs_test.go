package fsrs

import (
	"math"
	"testing"
	"time"
)

// anchor is a fixed reference time so tests never depend on the wall clock.
var anchor = time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

func TestNewCardIsDueImmediately(t *testing.T) {
	c := NewCard(anchor)
	if c.State != New {
		t.Fatalf("State = %v, want New", c.State)
	}
	if !c.Due.Equal(anchor) {
		t.Fatalf("Due = %v, want %v", c.Due, anchor)
	}
	if c.Reps != 0 {
		t.Fatalf("Reps = %d, want 0", c.Reps)
	}
}

func TestRatingAndStateValidity(t *testing.T) {
	if Rating(0).Valid() {
		t.Fatal("Rating(0) must be invalid so an unset rating is caught")
	}
	for r := Again; r <= Easy; r++ {
		if !r.Valid() {
			t.Fatalf("Rating %d should be valid", r)
		}
	}
	if Rating(5).Valid() {
		t.Fatal("Rating(5) must be invalid")
	}
}

func TestInvalidRatingIsANoOp(t *testing.T) {
	s := NewScheduler(Default())
	c := NewCard(anchor)
	got := s.Review(c, anchor, Rating(0))
	if got != c {
		t.Fatal("an invalid rating must leave the card unchanged")
	}
}

func TestFirstReviewFitsInitialScalars(t *testing.T) {
	s := NewScheduler(Default())
	c := NewCard(anchor)

	got := s.Review(c, anchor, Good)
	if got.Reps != 1 {
		t.Fatalf("Reps = %d, want 1", got.Reps)
	}
	if got.State != Review {
		t.Fatalf("State = %v, want Review", got.State)
	}
	// Initial stability for Good is w[2] in the default weights.
	if math.Abs(got.Stability-DefaultWeights[2]) > 1e-9 {
		t.Fatalf("Stability = %v, want %v", got.Stability, DefaultWeights[2])
	}
	if got.Difficulty < 1 || got.Difficulty > 10 {
		t.Fatalf("Difficulty = %v, out of [1,10]", got.Difficulty)
	}
	if !got.Due.After(anchor) {
		t.Fatalf("Due = %v, want after anchor", got.Due)
	}
}

// Harder first answers must produce higher difficulty and shorter first
// intervals than easier ones — the whole point of the four-point scale.
func TestFirstReviewMonotonicInRating(t *testing.T) {
	s := NewScheduler(Default())
	base := NewCard(anchor)

	again := s.Review(base, anchor, Again)
	hard := s.Review(base, anchor, Hard)
	good := s.Review(base, anchor, Good)
	easy := s.Review(base, anchor, Easy)

	if !(again.Difficulty >= hard.Difficulty &&
		hard.Difficulty >= good.Difficulty &&
		good.Difficulty >= easy.Difficulty) {
		t.Fatalf("difficulty not monotone: again=%v hard=%v good=%v easy=%v",
			again.Difficulty, hard.Difficulty, good.Difficulty, easy.Difficulty)
	}

	if !(again.Stability <= hard.Stability &&
		hard.Stability <= good.Stability &&
		good.Stability <= easy.Stability) {
		t.Fatalf("stability not monotone: again=%v hard=%v good=%v easy=%v",
			again.Stability, hard.Stability, good.Stability, easy.Stability)
	}
}

func TestAgainOnFirstReviewRelearns(t *testing.T) {
	s := NewScheduler(Default())
	got := s.Review(NewCard(anchor), anchor, Again)
	if got.State != Relearning {
		t.Fatalf("State = %v, want Relearning", got.State)
	}
	// Relearning shows the card again in minutes, well within the same day.
	if got.Due.Sub(anchor) > time.Hour {
		t.Fatalf("relearning Due = %v, want within an hour", got.Due.Sub(anchor))
	}
}

// Retrievability must be 1.0 at the instant of review (t=0) and decay toward 0
// as time passes, and equal exactly 0.9 one stability-length later.
func TestRetrievabilityDecaysAndHits90AtStability(t *testing.T) {
	s := NewScheduler(Default())
	c := s.Review(NewCard(anchor), anchor, Good)

	atReview := s.Retrievability(c, c.LastReview)
	if math.Abs(atReview-1.0) > 1e-9 {
		t.Fatalf("R at review = %v, want 1.0", atReview)
	}

	oneStabilityLater := c.LastReview.Add(time.Duration(c.Stability*24) * time.Hour)
	rAtS := s.Retrievability(c, oneStabilityLater)
	if math.Abs(rAtS-0.9) > 1e-3 {
		t.Fatalf("R at t=S = %v, want ~0.9", rAtS)
	}

	later := s.Retrievability(c, oneStabilityLater.AddDate(0, 0, 30))
	if later >= rAtS {
		t.Fatalf("R should keep decaying: %v then %v", rAtS, later)
	}

	if got := s.Retrievability(NewCard(anchor), anchor); got != 0 {
		t.Fatalf("R of a New card = %v, want 0", got)
	}
}

// A successful long-interval review must raise stability (memory strengthens),
// and a lapse must lower it (memory weakens but is not wiped to zero).
func TestRecallGrowsAndLapseShrinksStability(t *testing.T) {
	s := NewScheduler(Default())
	c := s.Review(NewCard(anchor), anchor, Good)

	// Come back after a real interval so the long-term formulas engage.
	future := c.Due
	recalled := s.Review(c, future, Good)
	if recalled.Stability <= c.Stability {
		t.Fatalf("recall stability %v did not grow past %v", recalled.Stability, c.Stability)
	}

	lapsed := s.Review(c, future, Again)
	if lapsed.Stability >= c.Stability {
		t.Fatalf("lapse stability %v did not shrink below %v", lapsed.Stability, c.Stability)
	}
	if lapsed.Stability <= 0 {
		t.Fatalf("lapse stability %v must stay positive", lapsed.Stability)
	}
	if lapsed.Lapses != 1 {
		t.Fatalf("Lapses = %d, want 1", lapsed.Lapses)
	}
}

// Requesting higher retention must never produce a longer interval for the same
// stability — you review more often to remember more.
func TestHigherRetentionMeansShorterInterval(t *testing.T) {
	high := NewScheduler(Params{Weights: DefaultWeights, DesiredRetention: 0.95, MaximumInterval: 36500})
	low := NewScheduler(Params{Weights: DefaultWeights, DesiredRetention: 0.80, MaximumInterval: 36500})

	const stability = 20.0
	if high.IntervalDays(stability) > low.IntervalDays(stability) {
		t.Fatalf("95%% retention interval %d > 80%% interval %d",
			high.IntervalDays(stability), low.IntervalDays(stability))
	}
}

func TestIntervalRespectsBounds(t *testing.T) {
	s := NewScheduler(Params{Weights: DefaultWeights, DesiredRetention: 0.9, MaximumInterval: 7})
	if got := s.IntervalDays(0.0001); got < 1 {
		t.Fatalf("interval %d below floor of 1", got)
	}
	if got := s.IntervalDays(1e6); got != 7 {
		t.Fatalf("interval %d not capped at MaximumInterval 7", got)
	}
}

// A full study arc should keep difficulty inside [1,10] no matter the ratings.
func TestDifficultyStaysBounded(t *testing.T) {
	s := NewScheduler(Default())
	c := NewCard(anchor)
	now := anchor
	ratings := []Rating{Again, Again, Hard, Good, Easy, Again, Good, Good, Hard, Easy}
	for _, r := range ratings {
		c = s.Review(c, now, r)
		if c.Difficulty < 1 || c.Difficulty > 10 {
			t.Fatalf("Difficulty %v escaped [1,10] after rating %d", c.Difficulty, r)
		}
		now = c.Due
	}
}
