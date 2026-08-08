// Package curriculumbus is the Business layer for the catalog: which decks exist
// for a language, what order they come in, what each one introduces itself with,
// and which one has to come first.
//
// It is the domain that turns "a pile of decks" into a course. The study domain
// schedules opaque items and has no idea a deck is harder than the one before it;
// vocab and the trigger seeds hold material and no idea anything is gated. The
// ordering, the introductions and the unlock rule live here and nowhere else.
//
// It knows nothing about a learner. Everything about a given person's progress
// arrives as a Coverage value the App layer assembles from the study domain, so
// this package stays a pure statement of the course and its rules — which is what
// makes the unlock rule testable without a database and changeable without one.
package curriculumbus

import (
	"context"
	"fmt"
	"math"

	"github.com/jroedel/uebung/business/types/deckid"
	"github.com/jroedel/uebung/business/types/langcode"
)

// defaultUnlockFraction is how much of a prerequisite deck must have been worked
// through before the deck behind it opens.
//
// Four fifths, because the tail of any deck is its rarest material and waiting
// for all of it would hold a ready learner behind a handful of words they will
// meet twice a year. It is a policy number with no theory behind it, which is why
// it is a knob on Config rather than a constant anyone has to believe in.
const defaultUnlockFraction = 0.8

// Storer is the port a catalog source implements. Read-only from the Business
// layer's point of view: a course is authored, not something the app edits at
// runtime, so there is no Save here.
type Storer interface {
	// Catalog returns every deck for a language in the order they should be
	// offered, which is the order the source curates them in. It returns an error
	// if the language is unknown or its catalog fails to parse into valid strong
	// types.
	Catalog(ctx context.Context, lang langcode.LangCode) ([]Deck, error)
}

// Config is the curriculum's policy.
type Config struct {
	// UnlockFraction is the share of a prerequisite deck that must be seen before
	// the deck it gates opens, in (0, 1]. Zero takes the default.
	UnlockFraction float64
}

// Business is the curriculum core.
type Business struct {
	store          Storer
	unlockFraction float64
}

// NewBusiness constructs the curriculum Business over a catalog source. It
// returns an error rather than clamping an out-of-range unlock fraction: a
// misconfigured gate either locks every deck forever or opens all of them, and
// both are worth failing at start-up for.
func NewBusiness(store Storer, cfg Config) (*Business, error) {
	fraction := cfg.UnlockFraction
	if fraction == 0 {
		fraction = defaultUnlockFraction
	}

	if fraction < 0 || fraction > 1 {
		return nil, fmt.Errorf("curriculumbus: unlock fraction %v is not in (0, 1]", fraction)
	}

	return &Business{store: store, unlockFraction: fraction}, nil
}

// Catalog returns every deck for a language, in the order they should be offered.
func (b *Business) Catalog(ctx context.Context, lang langcode.LangCode) ([]Deck, error) {
	if lang.IsZero() {
		return nil, fmt.Errorf("curriculumbus: catalog requested for the zero language")
	}

	decks, err := b.store.Catalog(ctx, lang)
	if err != nil {
		return nil, fmt.Errorf("curriculumbus: loading catalog for %q: %w", lang.String(), err)
	}

	return decks, nil
}

// Deck returns one deck from a language's catalog.
func (b *Business) Deck(ctx context.Context, lang langcode.LangCode, id deckid.DeckID) (Deck, error) {
	decks, err := b.Catalog(ctx, lang)
	if err != nil {
		return Deck{}, err
	}

	for _, d := range decks {
		if d.ID == id {
			return d, nil
		}
	}

	return Deck{}, fmt.Errorf("curriculumbus: no deck %q in the %q catalog", id.String(), lang.String())
}

// Gate decides whether a deck is open, given how far the learner has got through
// its prerequisite.
//
// The caller passes the prerequisite's coverage because this package holds no
// learner state; passing the zero Coverage for a deck with no prerequisite is
// correct and is what an ungated deck should be asked with.
//
// A prerequisite with no items is treated as met. That is the case where a deck
// was gated behind one whose material has not been written yet, and leaving a
// learner stuck behind an empty deck would be a worse answer than opening it.
func (b *Business) Gate(d Deck, prereq Coverage) Gate {
	if d.Prerequisite.IsZero() {
		return Gate{Met: true}
	}

	need := int(math.Ceil(b.unlockFraction * float64(prereq.Size)))

	return Gate{
		Requires: d.Prerequisite,
		Met:      prereq.Seen >= need,
		Seen:     prereq.Seen,
		Need:     need,
	}
}
