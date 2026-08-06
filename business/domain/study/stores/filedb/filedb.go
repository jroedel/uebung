// Package filedb is a stdlib-only studybus.Storer that persists cards as JSON on
// disk.
//
// It is the default store: it needs no driver, no server, and no build tag, so
// the app runs and is fully testable in any environment — including one with no
// network to fetch a database driver. The trade-off is that it rewrites the whole
// file on every Save, which is fine for a single learner's few hundred cards and
// explicitly not meant to scale to many concurrent users.
//
// The SQLite store the project is heading toward is a drop-in for this package:
// it implements the same studybus.Storer, so swapping it in is a one-line change
// in main where the store is constructed, with no change to the business or app
// layers. This file is deliberately the only place that knows cards live in a
// JSON document.
//
// Rows here are the Storage edge: dbProgress holds primitives and strings only —
// CardState is stored as its text name, never as an enum int whose meaning
// depends on iota order — and toBus/toDB are the converters across the
// Storage↔Business boundary, with toBusProgress returning an error when stored
// text fails to parse back into a strong type.
package filedb

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jroedel/uebung/business/domain/study/studybus"
	"github.com/jroedel/uebung/business/types/cardstate"
	"github.com/jroedel/uebung/business/types/langcode"
	"github.com/jroedel/uebung/business/types/userid"
)

// dbProgress is one card as persisted: primitives and strings only, no strong
// types. This is what JSON sees.
type dbProgress struct {
	User       string    `json:"user"`
	Lang       string    `json:"lang"`
	Lemma      string    `json:"lemma"`
	Stability  float64   `json:"stability"`
	Difficulty float64   `json:"difficulty"`
	State      string    `json:"state"`
	Reps       int       `json:"reps"`
	Lapses     int       `json:"lapses"`
	LastReview time.Time `json:"last_review"`
	Due        time.Time `json:"due"`
}

// document is the on-disk file shape: a version tag plus the flat card list. The
// version exists so a future format change can be detected rather than guessed.
type document struct {
	Version int          `json:"version"`
	Cards   []dbProgress `json:"cards"`
}

const currentVersion = 1

// Store is a concurrency-safe JSON-file card store. It keeps the cards in memory
// and treats the file as the durable copy, rewriting it under lock on each Save.
type Store struct {
	path string

	mu    sync.Mutex
	cards map[string]dbProgress
}

// key builds the composite storage key from strong-typed identity.
func key(user userid.UserID, lang langcode.LangCode, lemma string) string {
	return user.String() + "\n" + lang.String() + "\n" + lemma
}

// dbKey builds the same key from an already-stored row.
func dbKey(r dbProgress) string {
	return r.User + "\n" + r.Lang + "\n" + r.Lemma
}

// Open loads the store at path, creating an empty one if the file does not yet
// exist. It fails if the file exists but cannot be read or parsed, so a corrupt
// save is surfaced rather than silently discarded.
func Open(path string) (*Store, error) {
	s := &Store{path: path, cards: make(map[string]dbProgress)}

	raw, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		return s, nil
	case err != nil:
		return nil, fmt.Errorf("filedb: reading %s: %w", path, err)
	}

	var doc document
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("filedb: parsing %s: %w", path, err)
	}

	for _, r := range doc.Cards {
		s.cards[dbKey(r)] = r
	}

	return s, nil
}

// List returns every card for a user and language, converted to Business models.
func (s *Store) List(_ context.Context, user userid.UserID, lang langcode.LangCode) ([]studybus.Progress, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []studybus.Progress
	for _, r := range s.cards {
		if r.User != user.String() || r.Lang != lang.String() {
			continue
		}

		p, err := toBusProgress(r)
		if err != nil {
			return nil, fmt.Errorf("filedb: card %q: %w", r.Lemma, err)
		}

		out = append(out, p)
	}

	return out, nil
}

// Get returns one card and whether it existed.
func (s *Store) Get(_ context.Context, user userid.UserID, lang langcode.LangCode, lemma string) (studybus.Progress, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.cards[key(user, lang, lemma)]
	if !ok {
		return studybus.Progress{}, false, nil
	}

	p, err := toBusProgress(r)
	if err != nil {
		return studybus.Progress{}, false, fmt.Errorf("filedb: card %q: %w", lemma, err)
	}

	return p, true, nil
}

// Save converts the card to a row, stores it, and rewrites the file atomically.
func (s *Store) Save(_ context.Context, p studybus.Progress) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cards[key(p.User, p.Lang, p.Lemma)] = toDBProgress(p)

	return s.flushLocked()
}

// flushLocked writes the whole store to disk via a temp file and rename, so a
// crash mid-write cannot leave a half-written document in place. The caller holds
// s.mu.
func (s *Store) flushLocked() error {
	doc := document{Version: currentVersion, Cards: make([]dbProgress, 0, len(s.cards))}
	for _, r := range s.cards {
		doc.Cards = append(doc.Cards, r)
	}

	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("filedb: encoding: %w", err)
	}

	if dir := filepath.Dir(s.path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("filedb: creating %s: %w", dir, err)
		}
	}

	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return fmt.Errorf("filedb: writing temp file: %w", err)
	}

	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("filedb: replacing %s: %w", s.path, err)
	}

	return nil
}

// toDBProgress flattens a Business Progress to a storage row, turning strong
// types into their string forms.
func toDBProgress(p studybus.Progress) dbProgress {
	return dbProgress{
		User:       p.User.String(),
		Lang:       p.Lang.String(),
		Lemma:      p.Lemma,
		Stability:  p.Stability,
		Difficulty: p.Difficulty,
		State:      p.State.String(),
		Reps:       p.Reps,
		Lapses:     p.Lapses,
		LastReview: p.LastReview,
		Due:        p.Due,
	}
}

// toBusProgress parses a storage row back into a Business Progress, validating
// every stored string into its strong type and returning an error if the stored
// data has been corrupted into something unparseable.
func toBusProgress(r dbProgress) (studybus.Progress, error) {
	user, err := userid.Parse(r.User)
	if err != nil {
		return studybus.Progress{}, fmt.Errorf("user: %w", err)
	}

	lang, err := langcode.Parse(r.Lang)
	if err != nil {
		return studybus.Progress{}, fmt.Errorf("lang: %w", err)
	}

	state, err := cardstate.Parse(r.State)
	if err != nil {
		return studybus.Progress{}, fmt.Errorf("state: %w", err)
	}

	if r.Lemma == "" {
		return studybus.Progress{}, fmt.Errorf("empty lemma")
	}

	return studybus.Progress{
		User:       user,
		Lang:       lang,
		Lemma:      r.Lemma,
		Stability:  r.Stability,
		Difficulty: r.Difficulty,
		State:      state,
		Reps:       r.Reps,
		Lapses:     r.Lapses,
		LastReview: r.LastReview,
		Due:        r.Due,
	}, nil
}
