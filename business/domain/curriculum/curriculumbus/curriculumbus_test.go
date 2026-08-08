package curriculumbus_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jroedel/uebung/business/domain/curriculum/curriculumbus"
	"github.com/jroedel/uebung/business/types/deckid"
	"github.com/jroedel/uebung/business/types/drillkind"
	"github.com/jroedel/uebung/business/types/langcode"
)

var (
	first  = deckid.MustParse("first-deck")
	second = deckid.MustParse("second-deck")
	third  = deckid.MustParse("third-deck")
)

// fakeStore is a catalog held in memory, so the unlock rule can be tested without
// an authored file standing in the way of the case being described.
type fakeStore struct {
	decks []curriculumbus.Deck
	err   error
}

func (s fakeStore) Catalog(context.Context, langcode.LangCode) ([]curriculumbus.Deck, error) {
	if s.err != nil {
		return nil, s.err
	}

	return s.decks, nil
}

func course() fakeStore {
	return fakeStore{decks: []curriculumbus.Deck{
		{ID: first, Lang: langcode.German, Title: "First", Drill: drillkind.ArticleThreeWay},
		{ID: second, Lang: langcode.German, Title: "Second", Drill: drillkind.CaseThreeWay, Prerequisite: first},
		{ID: third, Lang: langcode.German, Title: "Third", Drill: drillkind.CaseThreeWay, Prerequisite: second},
	}}
}

func mustBusiness(t *testing.T, store curriculumbus.Storer, cfg curriculumbus.Config) *curriculumbus.Business {
	t.Helper()

	b, err := curriculumbus.NewBusiness(store, cfg)
	if err != nil {
		t.Fatalf("NewBusiness: %v", err)
	}

	return b
}

// A gate that never opens and a gate that is always open are both ways of
// shipping no curriculum at all, so a fraction outside (0, 1] is refused at
// construction rather than clamped into something that looks like it worked.
func TestNewBusinessRejectsAnUnusableFraction(t *testing.T) {
	for _, f := range []float64{-0.1, 1.5} {
		if _, err := curriculumbus.NewBusiness(course(), curriculumbus.Config{UnlockFraction: f}); err == nil {
			t.Errorf("NewBusiness accepted an unlock fraction of %v", f)
		}
	}

	if _, err := curriculumbus.NewBusiness(course(), curriculumbus.Config{UnlockFraction: 1}); err != nil {
		t.Errorf("NewBusiness rejected a fraction of 1: %v", err)
	}
}

func TestCatalogRefusesTheZeroLanguage(t *testing.T) {
	b := mustBusiness(t, course(), curriculumbus.Config{})

	if _, err := b.Catalog(t.Context(), langcode.LangCode{}); err == nil {
		t.Fatal("Catalog accepted the zero language")
	}
}

func TestCatalogPreservesTheSourceOrder(t *testing.T) {
	b := mustBusiness(t, course(), curriculumbus.Config{})

	got, err := b.Catalog(t.Context(), langcode.German)
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}

	want := []deckid.DeckID{first, second, third}
	if len(got) != len(want) {
		t.Fatalf("Catalog returned %d decks, want %d", len(got), len(want))
	}

	for i, id := range want {
		if got[i].ID != id {
			t.Errorf("deck %d is %q, want %q", i, got[i].ID, id)
		}
	}
}

func TestDeckNamesTheMissingDeck(t *testing.T) {
	b := mustBusiness(t, course(), curriculumbus.Config{})

	d, err := b.Deck(t.Context(), langcode.German, second)
	if err != nil {
		t.Fatalf("Deck: %v", err)
	}
	if d.Title != "Second" {
		t.Errorf("Deck returned %q, want %q", d.Title, "Second")
	}

	_, err = b.Deck(t.Context(), langcode.German, deckid.MustParse("no-such-deck"))
	if err == nil {
		t.Fatal("Deck found a deck that is not in the catalog")
	}
}

func TestCatalogWrapsAStoreFailure(t *testing.T) {
	boom := errors.New("catalog unreadable")
	b := mustBusiness(t, fakeStore{err: boom}, curriculumbus.Config{})

	_, err := b.Catalog(t.Context(), langcode.German)
	if !errors.Is(err, boom) {
		t.Fatalf("Catalog error = %v, want it to wrap %v", err, boom)
	}
}

func TestGate(t *testing.T) {
	b := mustBusiness(t, course(), curriculumbus.Config{UnlockFraction: 0.8})

	ungated := curriculumbus.Deck{ID: first}
	gated := curriculumbus.Deck{ID: second, Prerequisite: first}

	tests := []struct {
		name     string
		deck     curriculumbus.Deck
		prereq   curriculumbus.Coverage
		wantMet  bool
		wantNeed int
	}{
		{
			name:    "nothing gates the first deck",
			deck:    ungated,
			prereq:  curriculumbus.Coverage{},
			wantMet: true,
		},
		{
			name:     "one short",
			deck:     gated,
			prereq:   curriculumbus.Coverage{Size: 10, Seen: 7},
			wantMet:  false,
			wantNeed: 8,
		},
		{
			name:     "exactly enough",
			deck:     gated,
			prereq:   curriculumbus.Coverage{Size: 10, Seen: 8},
			wantMet:  true,
			wantNeed: 8,
		},
		{
			// 0.8 * 213 is 170.4, and 170 of 213 is not four fifths of it. Rounding
			// the requirement down would open the deck early, so the ceiling is what
			// the rule uses.
			name:     "the requirement rounds up",
			deck:     gated,
			prereq:   curriculumbus.Coverage{Size: 213, Seen: 170},
			wantMet:  false,
			wantNeed: 171,
		},
		{
			// A deck gated behind one whose material has not been written yet must
			// not strand the learner: nothing to cover is covered.
			name:     "an empty prerequisite is met",
			deck:     gated,
			prereq:   curriculumbus.Coverage{Size: 0, Seen: 0},
			wantMet:  true,
			wantNeed: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := b.Gate(tt.deck, tt.prereq)

			if got.Met != tt.wantMet {
				t.Errorf("Met = %v, want %v", got.Met, tt.wantMet)
			}
			if got.Need != tt.wantNeed {
				t.Errorf("Need = %d, want %d", got.Need, tt.wantNeed)
			}
			if tt.deck.Prerequisite.IsZero() && !got.Requires.IsZero() {
				t.Errorf("Requires = %q for an ungated deck, want the zero deck", got.Requires)
			}
			if !tt.deck.Prerequisite.IsZero() && got.Seen != tt.prereq.Seen {
				t.Errorf("Seen = %d, want %d", got.Seen, tt.prereq.Seen)
			}
		})
	}
}

// The property the whole rule exists to have: coverage only ever grows, so a gate
// that has opened can never close again. A retention-based rule would fail this
// the first time a learner lapsed a card, which is why it is not the rule.
func TestAnOpenGateNeverCloses(t *testing.T) {
	b := mustBusiness(t, course(), curriculumbus.Config{UnlockFraction: 0.8})
	gated := curriculumbus.Deck{ID: second, Prerequisite: first}

	const size = 50

	opened := -1
	for seen := range size + 1 {
		met := b.Gate(gated, curriculumbus.Coverage{Size: size, Seen: seen}).Met

		switch {
		case met && opened < 0:
			opened = seen
		case !met && opened >= 0:
			t.Fatalf("the gate opened at %d seen and closed again at %d", opened, seen)
		}
	}

	if opened != 40 {
		t.Errorf("the gate opened at %d of %d seen, want 40", opened, size)
	}
}

// A fraction of 0 is "unset", not "open everything": the default has to apply, or
// a Config someone left blank would silently unlock the whole course.
func TestZeroFractionTakesTheDefault(t *testing.T) {
	b := mustBusiness(t, course(), curriculumbus.Config{})
	gated := curriculumbus.Deck{ID: second, Prerequisite: first}

	if got := b.Gate(gated, curriculumbus.Coverage{Size: 100, Seen: 0}); got.Met {
		t.Error("a deck unlocked with nothing seen; the zero fraction did not take the default")
	}

	if got := b.Gate(gated, curriculumbus.Coverage{Size: 100, Seen: 79}).Need; got != 80 {
		t.Errorf("Need = %d with the default fraction, want 80", got)
	}
}
