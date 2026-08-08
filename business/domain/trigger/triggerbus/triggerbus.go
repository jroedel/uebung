// Package triggerbus is the Business layer for the case decks: the words that
// govern a grammatical case, and which case each one governs.
//
// It is to the trigger decks what vocabbus is to the noun deck — it owns what the
// cards are and knows nothing about when they should be shown, which belongs to
// the study domain. The split is what lets one scheduler serve every deck: study
// schedules opaque keys, and this package is one of the sources those keys are
// paired back with.
//
// Unlike vocabbus, a language here holds more than one deck — prepositions and
// verbs are separate decks of the same shape — so every call is keyed by deck as
// well as language. That is the only structural difference between the two, and
// it exists because the curriculum orders these decks against each other, while
// the noun deck stands alone.
package triggerbus

import (
	"context"
	"fmt"

	"github.com/jroedel/uebung/business/types/deckid"
	"github.com/jroedel/uebung/business/types/langcode"
)

// Storer is the port a trigger source implements. Read-only from the Business
// layer's point of view: these decks are authored data, not something the app
// edits at runtime, so there is no Save here.
type Storer interface {
	// Deck returns every trigger in one deck of a language, in the source's
	// curated order. It returns an error if the deck is unknown to the source or
	// its data fails to parse into valid strong types.
	Deck(ctx context.Context, lang langcode.LangCode, deck deckid.DeckID) ([]Trigger, error)
}

// Business is the trigger core. Thin today because the decks are static; it
// exists so the App layer depends on a Business port rather than reaching into a
// store, leaving room for filtering or sub-decks later without changing callers.
type Business struct {
	store Storer
}

// NewBusiness constructs the trigger Business over a deck source.
func NewBusiness(store Storer) *Business {
	return &Business{store: store}
}

// Deck returns the triggers in one deck.
func (b *Business) Deck(ctx context.Context, lang langcode.LangCode, deck deckid.DeckID) ([]Trigger, error) {
	if lang.IsZero() {
		return nil, fmt.Errorf("triggerbus: deck requested for the zero language")
	}

	if deck.IsZero() {
		return nil, fmt.Errorf("triggerbus: deck requested for the zero deck")
	}

	triggers, err := b.store.Deck(ctx, lang, deck)
	if err != nil {
		return nil, fmt.Errorf("triggerbus: loading deck %q for %q: %w", deck.String(), lang.String(), err)
	}

	return triggers, nil
}
