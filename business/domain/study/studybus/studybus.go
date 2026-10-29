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
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jroedel/uebung/business/types/batchid"
	"github.com/jroedel/uebung/business/types/deckid"
	"github.com/jroedel/uebung/business/types/langcode"
	"github.com/jroedel/uebung/business/types/rating"
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

	// SaveFlush writes a whole flush — each card's new state and the review that
	// produced it, in order — and records batch as applied for user, as one atomic
	// unit. If user already has batch recorded it writes nothing and returns
	// ErrDuplicateFlush. A zero batch is applied but not recorded.
	//
	// The same reasoning as Save, one level up: a flush the client may resend is
	// only safe to resend if it landed entirely or not at all, and if its record of
	// having landed cannot be separated from the cards it moved.
	SaveFlush(ctx context.Context, user userid.UserID, batch batchid.BatchID, progress []Progress, reviews []Review) error

	// Confusions counts, for one learner's deck, how often each item drew each
	// answer, over every review that recorded one. Rows whose answer was never
	// reported are left out rather than grouped under the empty string: they are
	// reviews the log cannot characterise, and counting them as a distinct reply
	// would put a phantom column in every matrix built from this.
	//
	// Order is not promised; the caller arranges them.
	Confusions(ctx context.Context, user userid.UserID, lang langcode.LangCode, deck deckid.DeckID) ([]Confusion, error)
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

// ErrDuplicateFlush reports that a flush carried a batch id this learner has
// already had applied. Nothing was written: the answers it holds are already in
// the store, from the first time the flush arrived.
var ErrDuplicateFlush = errors.New("studybus: flush already applied")

// Grade applies a self-graded answer to one card at time now, advances its memory
// model through the scheduler, persists the result together with a record of the
// answer, and returns the updated Progress. An item the user has never seen starts
// from a fresh card, so grading a new item is how it first enters the store.
//
// What was answered and how long it took ride along on in and are written to the
// log untouched; neither takes part in scheduling, which is FSRS's business and
// depends on the rating alone.
func (b *Business) Grade(ctx context.Context, user userid.UserID, lang langcode.LangCode, deck deckid.DeckID, in GradeInput, now time.Time) (Progress, error) {
	if err := checkScope(user, lang, deck); err != nil {
		return Progress{}, err
	}

	if err := checkGrade(in); err != nil {
		return Progress{}, err
	}

	current, err := b.load(ctx, user, lang, deck, in.Item, now)
	if err != nil {
		return Progress{}, err
	}

	reviewed, rev := b.review(current, in, now)

	if err := b.store.Save(ctx, reviewed, rev); err != nil {
		return Progress{}, fmt.Errorf("studybus: saving card %q: %w", in.Item, err)
	}

	return reviewed, nil
}

// GradeFlush applies one batch's worth of answers as a single unit and returns how
// many it applied.
//
// Either every answer lands or none does. That is what makes a failed flush safe
// to send again: the client cannot see how far a half-applied flush got, so a
// retry would re-grade whatever had landed. And a flush that did land is
// recognised by its batch id — the second arrival of the same id is refused with
// ErrDuplicateFlush and writes nothing — which covers the case the client cannot
// tell apart from a failure, a response lost on the way back.
//
// A zero batch is a client from before batch ids. Its flush is still applied
// atomically, but is not remembered, so it cannot be recognised if repeated.
//
// Every answer is checked before any is applied, and answers are applied in
// order: a card answered twice in one flush is reviewed twice, the second time
// from the state the first left it in.
func (b *Business) GradeFlush(ctx context.Context, user userid.UserID, lang langcode.LangCode, deck deckid.DeckID, batch batchid.BatchID, ins []GradeInput, now time.Time) (int, error) {
	if err := checkScope(user, lang, deck); err != nil {
		return 0, err
	}

	for _, in := range ins {
		if err := checkGrade(in); err != nil {
			return 0, err
		}
	}

	cards := make(map[string]Progress, len(ins))
	progress := make([]Progress, 0, len(ins))
	reviews := make([]Review, 0, len(ins))

	for _, in := range ins {
		current, seen := cards[in.Item]
		if !seen {
			var err error
			if current, err = b.load(ctx, user, lang, deck, in.Item, now); err != nil {
				return 0, err
			}
		}

		reviewed, rev := b.review(current, in, now)
		cards[in.Item] = reviewed
		progress = append(progress, reviewed)
		reviews = append(reviews, rev)
	}

	if err := b.store.SaveFlush(ctx, user, batch, progress, reviews); err != nil {
		return 0, fmt.Errorf("studybus: saving flush %q: %w", batch, err)
	}

	return len(ins), nil
}

// checkScope refuses a grade that does not say whose card, in which language and
// which deck, it is.
func checkScope(user userid.UserID, lang langcode.LangCode, deck deckid.DeckID) error {
	if user.IsZero() || lang.IsZero() || deck.IsZero() {
		return fmt.Errorf("studybus: grade requires a user, language and deck")
	}

	return nil
}

// checkGrade refuses an answer the scheduler or the log could not use.
func checkGrade(in GradeInput) error {
	switch {
	case in.Item == "":
		return fmt.Errorf("studybus: grade requires an item")
	case !in.Rating.Valid():
		return fmt.Errorf("studybus: invalid rating")
	case !in.Role.Valid():
		return fmt.Errorf("studybus: invalid role answer")

	// A negative duration is a broken clock or a client subtracting the wrong way
	// round, and either way it is not an answer time. Refusing it here keeps the
	// log arithmetic-safe for everything that will later average it.
	case in.Answered < 0:
		return fmt.Errorf("studybus: answer time cannot be negative")
	}

	return nil
}

// load returns the stored card for item, or a fresh one due now if the learner
// has never reviewed it.
func (b *Business) load(ctx context.Context, user userid.UserID, lang langcode.LangCode, deck deckid.DeckID, item string, now time.Time) (Progress, error) {
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

	return current, nil
}

// review advances one card through the scheduler for one answer, and builds the
// log entry that records it. It writes nothing.
func (b *Business) review(current Progress, in GradeInput, now time.Time) (Progress, Review) {
	applied := ratingFor(current, in.Rating)

	reviewed := fromFSRSCard(current, b.sched.Review(toFSRSCard(current), now, toFSRSRating(applied)))

	rev := Review{
		User:     current.User,
		Lang:     current.Lang,
		Deck:     current.Deck,
		Item:     in.Item,
		Rating:   applied,
		Role:     in.Role,
		Given:    in.Given,
		Answered: in.Answered,
		At:       now,
	}

	return reviewed, rev
}

// ratingFor is the rating a review is scheduled and logged with, which on a card's
// first review is at most Good.
//
// Every drill is a three-way forced choice, so a learner who has never seen a card
// still answers it correctly one time in three, and a guess can be fast. Easy on a
// first review starts the card at FSRS's Easy stability (w3, about 16 days at 0.9
// retention), so a lucky swipe would send a card nobody has learned away for two
// weeks. Speed is a fair signal of confidence on a card the learner has met before;
// on first sight it says nothing about memory, so Good (3 days) is the ceiling.
//
// The log records the capped rating because it is the one the card's state was
// computed from; how fast the answer came is kept in Answered regardless.
func ratingFor(current Progress, r rating.Rating) rating.Rating {
	if current.IsNew() {
		return min(r, rating.Good)
	}

	return r
}

// Confusions reports how often each of a learner's cards in one deck drew each
// answer, for every review that recorded one.
//
// It is a pass-through by design. The counting is the store's — it is a GROUP BY
// over a log that only grows, and pulling every row into Go to tally it would
// scale with how much someone has studied rather than with how much there is to
// say. What this domain adds is the guarantee that the question is asked in terms
// of a learner, a language and a deck, like every other question here.
func (b *Business) Confusions(ctx context.Context, user userid.UserID, lang langcode.LangCode, deck deckid.DeckID) ([]Confusion, error) {
	if user.IsZero() || lang.IsZero() || deck.IsZero() {
		return nil, fmt.Errorf("studybus: confusions requires a user, language, and deck")
	}

	out, err := b.store.Confusions(ctx, user, lang, deck)
	if err != nil {
		return nil, fmt.Errorf("studybus: reading the answer log for deck %q: %w", deck, err)
	}

	return out, nil
}

// Progress returns all of a user's scheduling records for one deck, for summaries
// and progress displays.
func (b *Business) Progress(ctx context.Context, user userid.UserID, lang langcode.LangCode, deck deckid.DeckID) ([]Progress, error) {
	if user.IsZero() || lang.IsZero() || deck.IsZero() {
		return nil, fmt.Errorf("studybus: progress requires a user, a language, and a deck")
	}

	return b.store.List(ctx, user, lang, deck)
}
