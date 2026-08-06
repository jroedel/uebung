// Package vocabbus is the Business layer for the noun deck: the set of nouns a
// learner is drilling, keyed by language.
//
// It owns the concept of "the deck" and nothing about scheduling — which nouns
// exist and what their correct gender is lives here; when each noun should next
// be shown lives in the study domain. The two are kept apart so a second module
// (a different language, or plurals instead of gender) is a new deck behind the
// same port, not a change to the scheduler.
package vocabbus

import (
	"context"
	"fmt"

	"github.com/jroedel/uebung/business/types/langcode"
)

// Storer is the port a deck source implements. A source is read-only from the
// Business layer's point of view: decks are authored data, not something the app
// mutates at runtime, so there is no Save here.
type Storer interface {
	// Deck returns every noun for a language, in the source's curated order
	// (roughly most-frequent first). It returns an error if the language is
	// unknown to the source or its data fails to parse into valid strong types.
	Deck(ctx context.Context, lang langcode.LangCode) ([]Noun, error)
}

// Business is the vocab core. It is a thin pass-through today because the deck is
// static; it exists so the app depends on a Business port rather than reaching
// into a store, leaving room for filtering, sub-decks, or difficulty tiers later
// without changing callers.
type Business struct {
	store Storer
}

// NewBusiness constructs the vocab Business over a deck source.
func NewBusiness(store Storer) *Business {
	return &Business{store: store}
}

// Deck returns the nouns for a language.
func (b *Business) Deck(ctx context.Context, lang langcode.LangCode) ([]Noun, error) {
	if lang.IsZero() {
		return nil, fmt.Errorf("vocabbus: deck requested for the zero language")
	}

	nouns, err := b.store.Deck(ctx, lang)
	if err != nil {
		return nil, fmt.Errorf("vocabbus: loading deck for %q: %w", lang.String(), err)
	}

	return nouns, nil
}
