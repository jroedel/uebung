// Package studybus is the Business layer for scheduling: it decides which cards a
// learner should see now and updates a card's memory model when they grade it.
//
// It is deliberately ignorant of what a card means. It schedules items — opaque
// string keys, scoped by a deck — for a user and a language, and never imports the
// vocab domain; pairing a scheduled item back with the noun, preposition or
// sentence it stands for is the App layer's job, per the rule that domains are
// composed one layer up. That keeps a new deck a matter of feeding different items
// through the same scheduler.
package studybus

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/jroedel/uebung/business/types/deckid"
	"github.com/jroedel/uebung/business/types/langcode"
	"github.com/jroedel/uebung/business/types/rating"
	"github.com/jroedel/uebung/business/types/roleanswer"
	"github.com/jroedel/uebung/business/types/userid"
	"github.com/jroedel/uebung/foundation/fsrs"
)

// Storer is the persistence port for scheduling records. It is phrased entirely
// in the (user, lang, deck, item) key so any backend — SQLite today, Postgres or a
// memory map for tests — implements the same three operations.
type Storer interface {
	// List returns every Progress a user has in one deck. Order is not promised;
	// the caller sorts.
	List(ctx context.Context, user userid.UserID, lang langcode.LangCode, deck deckid.DeckID) ([]Progress, error)

	// Get returns the Progress for one card. The bool is false when the user has
	// never reviewed that item; err is reserved for real storage failures, so a
	// missing card is not an error.
	Get(ctx context.Context, user userid.UserID, lang langcode.LangCode, deck deckid.DeckID, item string) (Progress, bool, error)

	// Save writes a card's new scheduling state and appends the review that
	// produced it, as one atomic unit.
	//
	// The two travel together on purpose. Every Save in this domain is the result
	// of a review, and the review log is only trustworthy as a history of the deck
	// if it cannot fall out of step with the deck — two separate calls would let a
	// crash land the card's new state with no record of the answer that caused it,
	// or a scored review of a card that never advanced. An implementation must
	// apply both or neither.
	Save(ctx context.Context, p Progress, rev Review) error
}

// Business is the study core: a Storer, the FSRS scheduler it grades against, and
// the policy caps for a study session.
type Business struct {
	store Storer
	sched fsrs.Scheduler

	// maxNewPerBatch bounds how many never-seen cards a single batch introduces,
	// so a fresh deck does not dump all ~200 items on a learner at once. Due
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

// Batch selects the items a learner should study now, in the order to show them,
// drawn from the supplied deck items.
//
// The deck's items are passed in as plain strings (the App layer read them from
// the deck's own domain) so studybus stays free of any content dependency.
// Selection is the standard spaced-repetition policy: every card whose Due has
// arrived, most overdue first, followed by up to maxNewPerBatch never-seen cards
// in deck order, with the whole batch capped at limit. Reviews are prioritised
// over new material because retention depends on not skipping them.
func (b *Business) Batch(ctx context.Context, user userid.UserID, lang langcode.LangCode, deck deckid.DeckID, deckItems []string, now time.Time, limit int) ([]string, error) {
	if user.IsZero() || lang.IsZero() || deck.IsZero() {
		return nil, fmt.Errorf("studybus: batch requires a user, a language, and a deck")
	}

	if limit <= 0 {
		limit = 20
	}

	saved, err := b.store.List(ctx, user, lang, deck)
	if err != nil {
		return nil, fmt.Errorf("studybus: listing progress: %w", err)
	}

	progress := make(map[string]Progress, len(saved))
	for _, p := range saved {
		progress[p.Item] = p
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
		out = append(out, p.Item)
	}

	// New cards: deck items with no progress yet, in deck order, capped.
	newAdded := 0
	for _, item := range deckItems {
		if len(out) == limit || newAdded == b.maxNewPerBatch {
			break
		}
		if _, seen := progress[item]; seen {
			continue
		}
		out = append(out, item)
		newAdded++
	}

	return out, nil
}

// Grade applies a self-graded rating to one card at time now, advances its memory
// model through the scheduler, persists the result together with a record of the
// answer, and returns the updated Progress. An item the user has never seen starts
// from a fresh card, so grading a new item is how it first enters the store.
//
// role reports how the learner did on the participant-role half of a two-part
// card. Decks that do not ask pass roleanswer.None.
func (b *Business) Grade(ctx context.Context, user userid.UserID, lang langcode.LangCode, deck deckid.DeckID, item string, r rating.Rating, role roleanswer.RoleAnswer, now time.Time) (Progress, error) {
	if user.IsZero() || lang.IsZero() || deck.IsZero() || item == "" {
		return Progress{}, fmt.Errorf("studybus: grade requires a user, language, deck, and item")
	}

	if !r.Valid() {
		return Progress{}, fmt.Errorf("studybus: invalid rating")
	}

	if !role.Valid() {
		return Progress{}, fmt.Errorf("studybus: invalid role answer")
	}

	current, found, err := b.store.Get(ctx, user, lang, deck, item)
	if err != nil {
		return Progress{}, fmt.Errorf("studybus: loading card %q: %w", item, err)
	}

	if !found {
		current = Progress{
			User: user,
			Lang: lang,
			Deck: deck,
			Item: item,
			Due:  now,
		}
	}

	reviewed := fromFSRSCard(current, b.sched.Review(toFSRSCard(current), now, toFSRSRating(r)))

	rev := Review{
		User:   user,
		Lang:   lang,
		Deck:   deck,
		Item:   item,
		Rating: r,
		Role:   role,
		At:     now,
	}

	if err := b.store.Save(ctx, reviewed, rev); err != nil {
		return Progress{}, fmt.Errorf("studybus: saving card %q: %w", item, err)
	}

	return reviewed, nil
}

// Progress returns all of a user's scheduling records for one deck, for summaries
// and progress displays.
func (b *Business) Progress(ctx context.Context, user userid.UserID, lang langcode.LangCode, deck deckid.DeckID) ([]Progress, error) {
	if user.IsZero() || lang.IsZero() || deck.IsZero() {
		return nil, fmt.Errorf("studybus: progress requires a user, a language, and a deck")
	}

	return b.store.List(ctx, user, lang, deck)
}
