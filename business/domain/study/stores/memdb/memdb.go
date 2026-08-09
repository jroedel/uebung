// Package memdb is an in-memory studybus.Storer.
//
// It exists for tests and for a throwaway session where nothing needs to outlive
// the process. It holds Business models directly rather than converting through a
// row type, because there is no serialization boundary to cross — the whole point
// is that nothing leaves memory. A real run uses sqlitedb instead.
package memdb

import (
	"cmp"
	"context"
	"slices"
	"sync"

	"github.com/jroedel/uebung/business/domain/study/studybus"
	"github.com/jroedel/uebung/business/types/deckid"
	"github.com/jroedel/uebung/business/types/langcode"
	"github.com/jroedel/uebung/business/types/userid"
)

// Store is a concurrency-safe in-memory card store keyed by
// (user, lang, deck, item), alongside the append-only review log that sqlitedb
// keeps in its second table.
type Store struct {
	mu      sync.RWMutex
	cards   map[string]studybus.Progress
	reviews []studybus.Review
}

// New constructs an empty in-memory store.
func New() *Store {
	return &Store{cards: make(map[string]studybus.Progress)}
}

// key builds the composite storage key. Newlines cannot appear in a UserID,
// LangCode, DeckID, or curated item key, so a newline separator is unambiguous.
func key(user userid.UserID, lang langcode.LangCode, deck deckid.DeckID, item string) string {
	return user.String() + "\n" + lang.String() + "\n" + deck.String() + "\n" + item
}

// List returns every card for a user in one deck.
func (s *Store) List(_ context.Context, user userid.UserID, lang langcode.LangCode, deck deckid.DeckID) ([]studybus.Progress, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var out []studybus.Progress
	for _, p := range s.cards {
		if p.User == user && p.Lang == lang && p.Deck == deck {
			out = append(out, p)
		}
	}

	return out, nil
}

// Get returns one card and whether it existed.
func (s *Store) Get(_ context.Context, user userid.UserID, lang langcode.LangCode, deck deckid.DeckID, item string) (studybus.Progress, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	p, ok := s.cards[key(user, lang, deck, item)]

	return p, ok, nil
}

// Save writes a card, replacing any existing one with the same key, and appends
// the review that produced it. Holding one lock across both is this store's
// version of sqlitedb's transaction: no reader can observe the card advanced
// without its review, or the other way round.
func (s *Store) Save(_ context.Context, p studybus.Progress, rev studybus.Review) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cards[key(p.User, p.Lang, p.Deck, p.Item)] = p
	s.reviews = append(s.reviews, rev)

	return nil
}

// Confusions tallies how often each item drew each answer, over the reviews of
// one learner's deck that recorded one.
//
// sqlitedb asks the database to group; here the log is a slice, so the tally is a
// map and the result is sorted before it is returned. The sort is not part of the
// port's promise — the caller arranges these — but an unsorted map walk would make
// this store's output vary run to run, and a test that passes four times in five
// is worse than no test.
func (s *Store) Confusions(_ context.Context, user userid.UserID, lang langcode.LangCode, deck deckid.DeckID) ([]studybus.Confusion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	counts := make(map[studybus.Confusion]int)
	for _, rev := range s.reviews {
		if rev.User != user || rev.Lang != lang || rev.Deck != deck || rev.Given == "" {
			continue
		}

		counts[studybus.Confusion{Item: rev.Item, Given: rev.Given}]++
	}

	out := make([]studybus.Confusion, 0, len(counts))
	for c, n := range counts {
		c.Count = n
		out = append(out, c)
	}

	slices.SortFunc(out, func(a, b studybus.Confusion) int {
		return cmp.Or(cmp.Compare(a.Item, b.Item), cmp.Compare(a.Given, b.Given))
	})

	return out, nil
}

// Reviews returns a copy of the log in the order it was written, so a test can
// assert on what was recorded without being able to alter it.
func (s *Store) Reviews() []studybus.Review {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return slices.Clone(s.reviews)
}
