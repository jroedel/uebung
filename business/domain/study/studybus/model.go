package studybus

import (
	"time"

	"github.com/jroedel/uebung/business/types/cardstate"
	"github.com/jroedel/uebung/business/types/langcode"
	"github.com/jroedel/uebung/business/types/userid"
)

// Progress is the persisted scheduling state of one card: a single learner's
// memory of one lemma in one language.
//
// Identity is the triple (User, Lang, Lemma). There is no synthetic id: a card
// is uniquely a given user's memory of a given noun, and the deck guarantees
// lemmas are unique within a language, so the natural key is both sufficient and
// the thing every lookup is actually phrased in terms of.
//
// Stability and Difficulty are the scheduler's latent scalars, carried here as
// plain float64 because that is what they are — this Business model deliberately
// does not embed foundation/fsrs.Card, so the scheduler stays an implementation
// detail the studybus core converts to and from at the point it schedules.
type Progress struct {
	User  userid.UserID
	Lang  langcode.LangCode
	Lemma string

	Stability  float64
	Difficulty float64
	State      cardstate.CardState
	Reps       int
	Lapses     int
	LastReview time.Time
	Due        time.Time
}

// IsNew reports whether this card has never been reviewed. A zero-value Progress
// returned for an unknown lemma is New, which is exactly how a fresh card should
// behave, so callers can treat "not found" and "new" identically.
func (p Progress) IsNew() bool { return p.Reps == 0 }
