package studybus

import (
	"github.com/jroedel/uebung/business/types/cardstate"
	"github.com/jroedel/uebung/business/types/rating"
	"github.com/jroedel/uebung/foundation/fsrs"
)

// This file is the foundation boundary. foundation/fsrs is a leaf the Business
// layer may import, but per the layering rules an fsrs type must never appear on
// a stored Business model, so every crossing goes through a named converter
// here — Progress carries plain scalars and strong types, fsrs.Card carries the
// scheduler's own, and these functions translate between them. Rating and
// CardState are mapped explicitly rather than by shared integer values so a
// reordering of either enum can never silently remap a grade or a state.

// toFSRSCard projects a Progress onto the scheduler's card. Identity fields
// (user, lang, lemma) are dropped: the scheduler does not know or need them.
func toFSRSCard(p Progress) fsrs.Card {
	return fsrs.Card{
		Stability:  p.Stability,
		Difficulty: p.Difficulty,
		State:      toFSRSState(p.State),
		Reps:       p.Reps,
		Lapses:     p.Lapses,
		LastReview: p.LastReview,
		Due:        p.Due,
	}
}

// fromFSRSCard folds a scheduled fsrs.Card back onto the Progress it came from,
// preserving that Progress's identity fields and overwriting only the scheduling
// scalars the review produced.
func fromFSRSCard(base Progress, c fsrs.Card) Progress {
	base.Stability = c.Stability
	base.Difficulty = c.Difficulty
	base.State = fromFSRSState(c.State)
	base.Reps = c.Reps
	base.Lapses = c.Lapses
	base.LastReview = c.LastReview
	base.Due = c.Due

	return base
}

// toFSRSRating maps a self-graded rating onto the scheduler's rating. An invalid
// rating maps to the scheduler's zero, which Review treats as a no-op — the App
// layer validates first, so this is a belt-and-braces default, not the primary
// guard.
func toFSRSRating(r rating.Rating) fsrs.Rating {
	switch r {
	case rating.Again:
		return fsrs.Again
	case rating.Hard:
		return fsrs.Hard
	case rating.Good:
		return fsrs.Good
	case rating.Easy:
		return fsrs.Easy
	default:
		return 0
	}
}

// toFSRSState maps a stored CardState onto the scheduler's State.
func toFSRSState(cs cardstate.CardState) fsrs.State {
	switch cs {
	case cardstate.Learning:
		return fsrs.Learning
	case cardstate.Review:
		return fsrs.Review
	case cardstate.Relearning:
		return fsrs.Relearning
	default:
		return fsrs.New
	}
}

// fromFSRSState maps a scheduler State back onto the stored CardState.
func fromFSRSState(s fsrs.State) cardstate.CardState {
	switch s {
	case fsrs.Learning:
		return cardstate.Learning
	case fsrs.Review:
		return cardstate.Review
	case fsrs.Relearning:
		return cardstate.Relearning
	default:
		return cardstate.New
	}
}
