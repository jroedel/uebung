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
	At     time.Time
}
