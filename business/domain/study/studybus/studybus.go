// Package studybus is the Business layer for scheduling: it decides which cards a
// learner should see now and updates a card's memory model when they grade it.
//
// It is deliberately ignorant of what a card means. It schedules lemmas — opaque
// string keys — for a user and a language, and never imports the vocab domain;
// pairing a scheduled lemma back with its noun and correct gender is the App
// layer's job, per the rule that domains are composed one layer up. That keeps a
// future module (plurals, verbs, a second language) a matter of feeding
// different lemmas through the same scheduler.
package studybus

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/jroedel/uebung/business/types/langcode"
	"github.com/jroedel/uebung/business/types/rating"
	"github.com/jroedel/uebung/business/types/userid"
	"github.com/jroedel/uebung/foundation/fsrs"
)

// Storer is the persistence port for scheduling records. It is phrased entirely
// in the (user, lang, lemma) key so any backend — SQLite today, Postgres or a
// memory map for tests — implements the same three operations.
type Storer interface {
	// List returns every Progress a user has for a language. Order is not
	// promised; the caller sorts.
	List(ctx context.Context, user userid.UserID, lang langcode.LangCode) ([]Progress, error)

	// Get returns the Progress for one card. The bool is false when the user has
	// never reviewed that lemma; err is reserved for real storage failures, so a
	// missing card is not an error.
	Get(ctx context.Context, user userid.UserID, lang langcode.LangCode, lemma string) (Progress, bool, error)

	// Save writes a Progress, creating or replacing it by its (user, lang, lemma)
	// key.
	Save(ctx context.Context, p Progress) error
}

// Business is the study core: a Storer, the FSRS scheduler it grades against, and
// the policy caps for a study session.
type Business struct {
	store Storer
	sched fsrs.Scheduler

	// maxNewPerBatch bounds how many never-seen cards a single batch introduces,
	// so a fresh deck does not dump all ~200 nouns on a learner at once. Due
	// reviews are never capped — falling behind on reviews is how retention is
	// lost, so those always come first and in full.
	maxNewPerBatch int
}

// Config tunes a Business. Zero values fall back to sensible defaults in
// NewBusiness, so a caller can pass Config{} and get a working scheduler.
type Config struct {
	FSRS           fsrs.Params
	MaxNewPerBatch int
}

// NewBusiness constructs the study core over a store and a configuration.
func NewBusiness(store Storer, cfg Config) *Business {
	maxNew := cfg.MaxNewPerBatch
	if maxNew <= 0 {
		maxNew = 20
	}

	return &Business{
		store:          store,
		sched:          fsrs.NewScheduler(cfg.FSRS),
		maxNewPerBatch: maxNew,
	}
}

// Batch selects the lemmas a learner should study now, in the order to show
// them, drawn from the supplied deck lemmas.
//
// The deck is passed in as plain strings (the App layer read it from the vocab
// domain) so studybus stays free of any vocab dependency. Selection is the
// standard spaced-repetition policy: every card whose Due has arrived, most
// overdue first, followed by up to maxNewPerBatch never-seen cards in deck
// order, with the whole batch capped at limit. Reviews are prioritised over new
// material because retention depends on not skipping them.
func (b *Business) Batch(ctx context.Context, user userid.UserID, lang langcode.LangCode, deckLemmas []string, now time.Time, limit int) ([]string, error) {
	if user.IsZero() || lang.IsZero() {
		return nil, fmt.Errorf("studybus: batch requires a user and a language")
	}

	if limit <= 0 {
		limit = 20
	}

	saved, err := b.store.List(ctx, user, lang)
	if err != nil {
		return nil, fmt.Errorf("studybus: listing progress: %w", err)
	}

	progress := make(map[string]Progress, len(saved))
	for _, p := range saved {
		progress[p.Lemma] = p
	}

	// Due reviews: seen cards whose next showing has arrived, soonest-due first.
	var due []Progress
	for _, p := range saved {
		if !p.IsNew() && !p.Due.After(now) {
			due = append(due, p)
		}
	}
	slices.SortFunc(due, func(a, c Progress) int { return a.Due.Compare(c.Due) })

	out := make([]string, 0, limit)
	for _, p := range due {
		if len(out) == limit {
			return out, nil
		}
		out = append(out, p.Lemma)
	}

	// New cards: deck lemmas with no progress yet, in deck order, capped.
	newAdded := 0
	for _, lemma := range deckLemmas {
		if len(out) == limit || newAdded == b.maxNewPerBatch {
			break
		}
		if _, seen := progress[lemma]; seen {
			continue
		}
		out = append(out, lemma)
		newAdded++
	}

	return out, nil
}

// Grade applies a self-graded rating to one card at time now, advances its memory
// model through the scheduler, persists the result, and returns the updated
// Progress. A lemma the user has never seen starts from a fresh card, so grading
// a new lemma is how it first enters the store.
func (b *Business) Grade(ctx context.Context, user userid.UserID, lang langcode.LangCode, lemma string, r rating.Rating, now time.Time) (Progress, error) {
	if user.IsZero() || lang.IsZero() || lemma == "" {
		return Progress{}, fmt.Errorf("studybus: grade requires a user, language, and lemma")
	}

	if !r.Valid() {
		return Progress{}, fmt.Errorf("studybus: invalid rating")
	}

	current, found, err := b.store.Get(ctx, user, lang, lemma)
	if err != nil {
		return Progress{}, fmt.Errorf("studybus: loading card %q: %w", lemma, err)
	}

	if !found {
		current = Progress{
			User:  user,
			Lang:  lang,
			Lemma: lemma,
			Due:   now,
		}
	}

	reviewed := fromFSRSCard(current, b.sched.Review(toFSRSCard(current), now, toFSRSRating(r)))

	if err := b.store.Save(ctx, reviewed); err != nil {
		return Progress{}, fmt.Errorf("studybus: saving card %q: %w", lemma, err)
	}

	return reviewed, nil
}

// Progress returns all of a user's scheduling records for a language, for
// summaries and progress displays.
func (b *Business) Progress(ctx context.Context, user userid.UserID, lang langcode.LangCode) ([]Progress, error) {
	if user.IsZero() || lang.IsZero() {
		return nil, fmt.Errorf("studybus: progress requires a user and a language")
	}

	return b.store.List(ctx, user, lang)
}
