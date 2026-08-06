// Package fsrs implements the Free Spaced Repetition Scheduler (FSRS-5) as a
// self-contained, dependency-free scheduler.
//
// FSRS models a memory as two latent scalars — stability and difficulty — and
// derives, from the time elapsed since the last review, how likely the memory
// is to be recalled right now (its retrievability). A review reports how the
// recall actually went, and the model updates both scalars and picks the next
// due date so that recall probability falls back to a requested target
// (DesiredRetention) exactly when the card next comes up.
//
// Why FSRS and not SM-2: SM-2 conflates "how hard is this item" with "how long
// until I forget it" into one ease number and steps intervals by a fixed
// multiplier. FSRS separates the two, fits a forgetting curve, and schedules
// against a retention target you choose. For a learner practising der/die/das
// on hundreds of nouns, that means fewer reviews for the same retention and no
// "ease hell" where a couple of lapses wreck an item forever.
//
// This package is foundation: it is pure arithmetic over float64 and time.Time,
// it imports nothing outside the standard library, and it holds no business
// vocabulary. The study domain wraps Rating and Card into its own strong types
// at the boundary rather than letting these leak upward — see business/domain/
// study. The math and the default weights follow the open FSRS-5 specification.
package fsrs

import (
	"math"
	"time"
)

// decay and factor define the forgetting curve
//
//	R(t) = (1 + factor * t/S) ^ decay
//
// where S is stability in days and t is elapsed days. factor is chosen so that
// R equals 0.9 exactly when t == S: (1 + 19/81)^(-0.5) = (100/81)^(-0.5) = 0.9.
// Stability is therefore, by definition, the number of days until recall
// probability decays to 90%.
const (
	decay  = -0.5
	factor = 19.0 / 81.0
)

// Bounds the model enforces on its latent scalars and on any interval it emits.
// Difficulty is a 1..10 scale by construction; stability is kept off zero so the
// forgetting curve never divides by it.
const (
	minDifficulty = 1.0
	maxDifficulty = 10.0
	minStability  = 0.01
)

// DefaultWeights are the 19 published FSRS-5 default parameters. They are the
// population-average fit; a per-user optimiser can replace them later without
// any other code changing, which is why Params carries them as data rather than
// baking them into the functions below.
var DefaultWeights = Weights{
	0.40255, 1.18385, 3.173, 15.69105, 7.1949, 0.5345, 1.4604,
	0.0046, 1.54575, 0.1192, 1.01925, 1.9395, 0.11, 0.29605,
	2.2698, 0.2315, 2.9898, 0.51655, 0.6621,
}

// Weights is the FSRS-5 parameter vector. It is a fixed-size array so a
// malformed parameter set is a compile-time impossibility rather than a runtime
// length check.
type Weights [19]float64

// Params configures a Scheduler: the fitted Weights plus the two policy knobs a
// caller actually tunes.
type Params struct {
	Weights Weights

	// DesiredRetention is the recall probability the scheduler aims for at the
	// moment a card next falls due, in (0,1). Higher means shorter intervals and
	// more reviews; 0.9 is the conventional default.
	DesiredRetention float64

	// MaximumInterval caps how far out a card can be scheduled, in days. It stops
	// a very stable card from disappearing for years.
	MaximumInterval int
}

// Default returns the standard configuration: published weights, 90% retention,
// and a 100-year interval cap.
func Default() Params {
	return Params{
		Weights:          DefaultWeights,
		DesiredRetention: 0.9,
		MaximumInterval:  36500,
	}
}

// Rating is how a single review went, on the four-point FSRS scale. The zero
// value is intentionally invalid so a forgotten assignment is caught rather than
// silently meaning Again.
type Rating uint8

const (
	_     Rating = iota // 0 is reserved: no valid review is "unset".
	Again               // total failure to recall.
	Hard                // recalled, but with serious effort or hesitation.
	Good                // recalled correctly with normal effort.
	Easy                // recalled effortlessly.
)

// Valid reports whether r is one of the four defined ratings.
func (r Rating) Valid() bool { return r >= Again && r <= Easy }

// State is where a card sits in its lifecycle. It changes what a review means:
// a New card gets an initial fit, a Review card gets a recall/forget update.
type State uint8

const (
	New        State = iota // never reviewed.
	Learning                // in the initial short-term steps.
	Review                  // graduated to long-term scheduling.
	Relearning              // lapsed from Review and being rebuilt.
)

// Card is the full scheduling state of one item. It is a plain value: copy it,
// store its fields, reconstruct it. Stability and Difficulty are meaningless
// until the card has left New (Reps > 0).
type Card struct {
	Stability  float64   // days until retrievability decays to 0.9.
	Difficulty float64   // 1..10; intrinsic hardness of the item.
	State      State     // lifecycle position.
	Reps       int       // total reviews applied.
	Lapses     int       // times the card was rated Again after graduating.
	LastReview time.Time // when the most recent review happened; zero for New.
	Due        time.Time // when the card should next be shown.
}

// NewCard returns a card that has never been reviewed and is due immediately at
// now, so a fresh deck presents in insertion order the first time through.
func NewCard(now time.Time) Card {
	return Card{State: New, Due: now}
}

// Scheduler applies FSRS transitions. It is stateless and safe to share across
// goroutines: every method takes the card and returns a new one.
type Scheduler struct {
	p Params
}

// NewScheduler builds a Scheduler from params, repairing only the two policy knobs if a
// caller left them at an unusable zero value. The weights are taken as given —
// an optimiser is trusted to supply a coherent vector.
func NewScheduler(p Params) Scheduler {
	if p.DesiredRetention <= 0 || p.DesiredRetention >= 1 {
		p.DesiredRetention = 0.9
	}

	if p.MaximumInterval <= 0 {
		p.MaximumInterval = 36500
	}

	return Scheduler{p: p}
}

// Retrievability returns the modelled probability, in [0,1], that the card is
// recalled at time now given how long it has been since its last review. A New
// card has no memory to recall, so this reports 0.
func (s Scheduler) Retrievability(c Card, now time.Time) float64 {
	if c.Reps == 0 || c.Stability < minStability {
		return 0
	}

	elapsed := daysBetween(c.LastReview, now)
	if elapsed < 0 {
		elapsed = 0
	}

	return math.Pow(1+factor*elapsed/c.Stability, decay)
}

// Review applies a rating at time now and returns the updated card, including
// its next Due date. It routes on the card's current State: a first review fits
// initial stability and difficulty, while a later one updates them from the
// recall/forget formulas and the retrievability at review time.
func (s Scheduler) Review(c Card, now time.Time, r Rating) Card {
	if !r.Valid() {
		return c
	}

	next := c
	next.Reps = c.Reps + 1
	next.LastReview = now

	if c.Reps == 0 {
		next.Difficulty = s.initDifficulty(r)
		next.Stability = s.initStability(r)
		next.State, next.Due = s.graduate(next.Stability, r, now)

		return next
	}

	retr := s.Retrievability(c, now)
	next.Difficulty = s.nextDifficulty(c.Difficulty, r)

	sameDay := daysBetween(c.LastReview, now) < 1
	switch {
	case sameDay:
		// A same-day repeat is a learning step, not a memory-interval update:
		// almost no time has passed, so the long-term formulas would barely move.
		// FSRS uses a dedicated short-term update here instead.
		next.Stability = s.shortTermStability(c.Stability, r)
	case r == Again:
		next.Stability = s.forgetStability(next.Difficulty, c.Stability, retr)
		next.Lapses = c.Lapses + 1
	default:
		next.Stability = s.recallStability(next.Difficulty, c.Stability, retr, r)
	}

	next.State, next.Due = s.graduate(next.Stability, r, now)

	return next
}

// IntervalDays is the number of days from a review until retrievability is
// projected to fall to DesiredRetention, given stability. It inverts the
// forgetting curve and clamps to [1, MaximumInterval].
func (s Scheduler) IntervalDays(stability float64) int {
	raw := stability / factor * (math.Pow(s.p.DesiredRetention, 1/decay) - 1)
	rounded := int(math.Round(raw))

	return clampInt(rounded, 1, s.p.MaximumInterval)
}

// graduate decides the post-review State and Due time. A lapse (Again) drops a
// graduated card into Relearning and shows it again promptly; anything else
// schedules it out by the computed interval and marks it Review.
func (s Scheduler) graduate(stability float64, r Rating, now time.Time) (State, time.Time) {
	if r == Again {
		return Relearning, now.Add(10 * time.Minute)
	}

	return Review, now.AddDate(0, 0, s.IntervalDays(stability))
}

// --- FSRS-5 formulas -------------------------------------------------------
//
// Each helper is one line of the specification, named for what it computes. The
// weight indices are the published ones; they are opaque by nature, so the names
// carry the meaning the numbers cannot.

func (s Scheduler) initStability(r Rating) float64 {
	return math.Max(s.p.Weights[r-1], minStability)
}

func (s Scheduler) initDifficulty(r Rating) float64 {
	d := s.p.Weights[4] - math.Exp(s.p.Weights[5]*float64(r-1)) + 1

	return clampFloat(d, minDifficulty, maxDifficulty)
}

// nextDifficulty nudges difficulty by the rating (harder ratings raise it),
// damps the nudge near the ceiling so a hard item cannot rocket to 10, then
// mean-reverts toward the difficulty an Easy first answer would have implied so
// difficulty drifts back to the population centre over time.
func (s Scheduler) nextDifficulty(d float64, r Rating) float64 {
	deltaD := -s.p.Weights[6] * float64(int(r)-3)
	damped := d + deltaD*(10-d)/9
	reverted := s.p.Weights[7]*s.initDifficulty(Easy) + (1-s.p.Weights[7])*damped

	return clampFloat(reverted, minDifficulty, maxDifficulty)
}

// recallStability grows stability after a successful recall. The gain shrinks as
// difficulty, current stability, and retrievability rise — you learn least from
// reviewing something you already know cold — with a penalty for Hard and a
// bonus for Easy.
func (s Scheduler) recallStability(d, stability, retr float64, r Rating) float64 {
	hardPenalty := 1.0
	if r == Hard {
		hardPenalty = s.p.Weights[15]
	}

	easyBonus := 1.0
	if r == Easy {
		easyBonus = s.p.Weights[16]
	}

	growth := math.Exp(s.p.Weights[8]) *
		(11 - d) *
		math.Pow(stability, -s.p.Weights[9]) *
		(math.Exp((1-retr)*s.p.Weights[10]) - 1) *
		hardPenalty *
		easyBonus

	return math.Max(stability*(1+growth), minStability)
}

// forgetStability computes the (lower) post-lapse stability. A harder item and a
// higher pre-lapse stability retain a little more; the longer it had been since
// review, the more the lapse costs.
func (s Scheduler) forgetStability(d, stability, retr float64) float64 {
	sf := s.p.Weights[11] *
		math.Pow(d, -s.p.Weights[12]) *
		(math.Pow(stability+1, s.p.Weights[13]) - 1) *
		math.Exp((1-retr)*s.p.Weights[14])

	// A lapse must never make a memory more stable than a success would have; the
	// post-forget stability is capped at the pre-lapse value.
	return clampFloat(sf, minStability, stability)
}

// shortTermStability handles same-day repeats (learning steps) with the FSRS-5
// short-term update, an exponential step in the rating rather than a curve fit.
func (s Scheduler) shortTermStability(stability float64, r Rating) float64 {
	return math.Max(stability*math.Exp(s.p.Weights[17]*(float64(int(r)-3)+s.p.Weights[18])), minStability)
}

// --- small helpers ---------------------------------------------------------

// daysBetween is the elapsed time from a to b expressed in fractional days.
func daysBetween(a, b time.Time) float64 {
	return b.Sub(a).Hours() / 24
}

func clampFloat(v, lo, hi float64) float64 {
	return math.Min(math.Max(v, lo), hi)
}

func clampInt(v, lo, hi int) int {
	return min(max(v, lo), hi)
}
