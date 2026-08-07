// Package memdb is an in-memory studybus.Storer.
//
// It exists for tests and for a throwaway session where nothing needs to outlive
// the process. It holds Business models directly rather than converting through a
// row type, because there is no serialization boundary to cross — the whole point
// is that nothing leaves memory. A real run uses sqlitedb instead.
package memdb

import (
	"context"
	"sync"

	"github.com/jroedel/uebung/business/domain/study/studybus"
	"github.com/jroedel/uebung/business/types/langcode"
	"github.com/jroedel/uebung/business/types/userid"
)

// Store is a concurrency-safe in-memory card store keyed by (user, lang, lemma).
type Store struct {
	mu    sync.RWMutex
	cards map[string]studybus.Progress
}

// New constructs an empty in-memory store.
func New() *Store {
	return &Store{cards: make(map[string]studybus.Progress)}
}

// key builds the composite storage key. Newlines cannot appear in a UserID,
// LangCode, or curated lemma, so a newline separator is unambiguous.
func key(user userid.UserID, lang langcode.LangCode, lemma string) string {
	return user.String() + "\n" + lang.String() + "\n" + lemma
}

// List returns every card for a user and language.
func (s *Store) List(_ context.Context, user userid.UserID, lang langcode.LangCode) ([]studybus.Progress, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var out []studybus.Progress
	for _, p := range s.cards {
		if p.User == user && p.Lang == lang {
			out = append(out, p)
		}
	}

	return out, nil
}

// Get returns one card and whether it existed.
func (s *Store) Get(_ context.Context, user userid.UserID, lang langcode.LangCode, lemma string) (studybus.Progress, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	p, ok := s.cards[key(user, lang, lemma)]

	return p, ok, nil
}

// Save writes a card, replacing any existing one with the same key.
func (s *Store) Save(_ context.Context, p studybus.Progress) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cards[key(p.User, p.Lang, p.Lemma)] = p

	return nil
}
