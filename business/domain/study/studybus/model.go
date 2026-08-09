package studybus

import (
	"time"

	"github.com/jroedel/uebung/business/types/cardstate"
	"github.com/jroedel/uebung/business/types/deckid"
	"github.com/jroedel/uebung/business/types/langcode"
	"github.com/jroedel/uebung/business/types/rating"
	"github.com/jroedel/uebung/business/types/roleanswer"
	"github.com/jroedel/uebung/business/types/userid"
)

// Progress is the persisted scheduling state of one card: a single learner's
// memory of one item in one deck of one language.
//
// Identity is the quadruple (User, Lang, Deck, Item). There is no synthetic id: a
// card is uniquely a given user's memory of a given item, and a deck guarantees
// its item keys are unique within itself, so the natural key is both sufficient
// and the thing every lookup is actually phrased in terms of.
//
// Item is deliberately an opaque string rather than a lemma. It was a lemma when
// there was one deck of nouns; a deck of prepositions keys on "mit" and a deck of
// tagged sentences keys on a sentence id, and the scheduler has never cared which
// — it schedules keys. Deck is what keeps those namespaces apart, so "mit" as a
// preposition and "mit" as anything else are two cards and not one.
//
// Stability and Difficulty are the scheduler's latent scalars, carried here as
// plain float64 because that is what they are — this Business model deliberately
// does not embed foundation/fsrs.Card, so the scheduler stays an implementation
// detail the studybus core converts to and from at the point it schedules.
type Progress struct {
	User userid.UserID
	Lang langcode.LangCode
	Deck deckid.DeckID
	Item string

	Stability  float64
	Difficulty float64
	State      cardstate.CardState
	Reps       int
	Lapses     int
	LastReview time.Time
	Due        time.Time
}

// IsNew reports whether this card has never been reviewed. A zero-value Progress
// returned for an unknown item is New, which is exactly how a fresh card should
// behave, so callers can treat "not found" and "new" identically.
func (p Progress) IsNew() bool { return p.Reps == 0 }

// Review is one graded answer, recorded at the moment it happened.
//
// Progress is a running total that each review overwrites: it says where a card
// stands now, and nothing about how it got there. Review is the opposite — an
// append-only fact that is never updated, so the history stays intact no matter
// how the scheduler later revises the card.
//
// It exists because points, streaks, daily counts and a leaderboard are all
// questions about *when* someone studied, and Progress cannot answer any of them:
// it holds one timestamp, the most recent. Keeping the events instead of a
// running score also means the scoring formula can change without orphaning the
// history, which a counter column could never offer.
//
// Role is None for every deck that does not ask for a participant role, which is
// all of them today; see business/types/roleanswer for what it is reserved for.
type Review struct {
	User userid.UserID
	Lang langcode.LangCode
	Deck deckid.DeckID
	Item string

	Rating rating.Rating
	Role   roleanswer.RoleAnswer

	// Given is the answer the learner actually gave, as the deck writes it —
	// "der", "dativ" — and it is the difference between knowing that someone got
	// a card wrong and knowing *how*.
	//
	// Rating cannot answer that. Every miss is Again whatever was swiped, so a log
	// of ratings can count mistakes and can never characterise them: it cannot say
	// that this learner turns feminines masculine far more often than the reverse,
	// or that they reach for the dative under time pressure. That is the one thing
	// a learner cannot discover by practising, and it is exactly the diagnostic
	// roleanswer's package comment argues for on the two-part cards — the same
	// reasoning applies to every deck that offers a choice.
	//
	// It is an opaque string here for the reason Item is: the study domain
	// schedules keys and grades answers without knowing what kind of thing either
	// is. What makes it trustworthy is that the App layer validates it against the
	// deck's own declared answers before it ever arrives, so the log cannot hold a
	// reply the deck does not offer.
	//
	// Empty means "not reported": a browser holding a client from before this
	// field existed sends every grade without it, and a deck may yet be drilled in
	// a way that has no discrete answer to record.
	//
	// The *expected* answer is deliberately not stored beside it. That is a fact
	// about the deck rather than about the learner, it is already known wherever
	// the deck's material is loaded, and copying it into the log would freeze a
	// mistake in the authored data into every row written before it was fixed.
	Given string

	// Answered is how long the card was on screen before the learner committed,
	// and it is the metric this course's audience can actually move. Someone
	// refreshing German after classes does not become *more* correct on "der
	// Mann" — they were already correct. They become faster, and the crossing from
	// deliberate recall to automatic retrieval is what the practice is for.
	//
	// The client already measures it to choose between Easy, Good and Hard, then
	// discards the number. Keeping it means the speed a learner is gaining is
	// reportable, and that the two thresholds doing the bucketing stop being
	// constants nobody can check against real answers.
	//
	// Zero means "not reported" rather than an instantaneous answer, which is not
	// a reachable value: a client that measured a real answer never rounds it to
	// nothing, and only one that never measured sends none.
	Answered time.Duration

	At time.Time
}

// GradeInput is one answer as it arrives to be graded: which card, how it went,
// and the two facts about the answer itself worth keeping.
//
// It is a struct rather than five more parameters on Grade because three of the
// fields are strings and two of those — Item and Given — are interchangeable at a
// call site without the compiler noticing. Filing a grade against the card the
// learner answered *with* rather than the one they answered is the kind of bug
// that reads correctly, tests green on a symmetric fixture, and quietly poisons
// the log. Named fields make it unwritable.
//
// The identity of the learner and the deck stays on Grade's own parameters: those
// are the same triple every method in this domain is phrased in, and folding them
// in here would make each call restate what the caller already scoped.
type GradeInput struct {
	Item   string
	Rating rating.Rating

	// Role is how the learner did on the participant-role half of a two-part card.
	// Decks that do not ask leave it None.
	Role roleanswer.RoleAnswer

	// Given and Answered carry through to the Review unchanged; see there for what
	// each is for and why the zero value of each means "not reported".
	Given    string
	Answered time.Duration
}

// Confusion is how often one card drew one answer: the raw material of an error
// profile, counted by the store rather than assembled in memory.
//
// It names the item and not the answer that was expected, because this domain
// does not know what any item's correct answer is — it grades what it is told and
// schedules keys. Pairing Item back with its expected answer is the App layer's
// job, and it is the same pairing a batch already does. That is what lets a
// confusion matrix be built without the study domain learning what a noun is.
//
// Correct answers are included. A matrix showing only mistakes cannot say whether
// four die-for-der slips happened over ten feminine cards or four hundred, and
// the diagonal is what turns a count into a rate.
type Confusion struct {
	Item  string
	Given string
	Count int
}
