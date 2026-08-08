package seeddb_test

import (
	"strings"
	"testing"

	curriculumseed "github.com/jroedel/uebung/business/domain/curriculum/stores/seeddb"
	"github.com/jroedel/uebung/business/domain/trigger/stores/seeddb"
	"github.com/jroedel/uebung/business/types/deckid"
	"github.com/jroedel/uebung/business/types/drillkind"
	"github.com/jroedel/uebung/business/types/langcode"
)

// The authored decks must load cleanly. This is the test that fails when someone
// edits the JSON and mistypes a case or pastes a phrase onto the wrong row: the
// store validates every field, so loading it here is the whole check.
func TestAuthoredDecksLoad(t *testing.T) {
	store := seeddb.New()

	for _, id := range []deckid.DeckID{deckid.MustParse("de-praepositionen"), deckid.MustParse("de-verben-kasus")} {
		triggers, err := store.Deck(t.Context(), langcode.German, id)
		if err != nil {
			t.Fatalf("loading %q: %v", id, err)
		}

		if len(triggers) == 0 {
			t.Fatalf("deck %q loaded empty", id)
		}

		// The phrase/trigger agreement is the store's own load-time rule, so
		// loading without error already asserts it; repeating the rule here would
		// only couple this test to its details. What is worth checking is that
		// every card came back usable.
		for _, tr := range triggers {
			if tr.Case.IsZero() {
				t.Fatalf("deck %q trigger %q has no case", id, tr.Word)
			}
			if tr.Phrase == "" || tr.Gloss == "" || tr.Example == "" {
				t.Fatalf("deck %q trigger %q is missing content: %+v", id, tr.Word, tr)
			}
			if tr.Lang != langcode.German {
				t.Fatalf("deck %q trigger %q has language %v", id, tr.Word, tr.Lang)
			}
		}
	}
}

// A verb's example uses it conjugated — "helfen" appears as "helfe" in "Ich helfe
// meinem Bruder." The store therefore checks the *phrase* contains its trigger and
// never the example, and this test pins that distinction: a well-meant "examples
// must contain their trigger" rule would reject most of the verb deck.
func TestVerbExamplesNeedNotContainTheInfinitive(t *testing.T) {
	triggers, err := seeddb.New().Deck(t.Context(), langcode.German, deckid.MustParse("de-verben-kasus"))
	if err != nil {
		t.Fatalf("loading verbs: %v", err)
	}

	var conjugated int
	for _, tr := range triggers {
		if !strings.Contains(tr.Example, tr.Word) {
			conjugated++
		}
	}

	if conjugated == 0 {
		t.Fatal("expected at least one verb whose example conjugates it away from the infinitive")
	}
}

// The curriculum sizes a deck by counting the items in its catalog entry; this
// store turns those same items into cards. They are read from one file by two
// packages, so nothing but a test stops them drifting — and if they do, the shelf
// promises a number of cards the drill cannot deliver.
func TestCatalogSizeMatchesTheItemsActuallyLoaded(t *testing.T) {
	catalog, err := curriculumseed.New().Catalog(t.Context(), langcode.German)
	if err != nil {
		t.Fatalf("loading catalog: %v", err)
	}

	var checked int
	for _, d := range catalog {
		if d.Drill != drillkind.CaseThreeWay {
			continue
		}

		triggers, err := seeddb.New().Deck(t.Context(), langcode.German, d.ID)
		if err != nil {
			t.Fatalf("deck %q is in the catalog but has no items: %v", d.ID, err)
		}

		if d.Size != len(triggers) {
			t.Fatalf("deck %q: catalog says %d cards, store loads %d", d.ID, d.Size, len(triggers))
		}
		checked++
	}

	if checked == 0 {
		t.Fatal("no case-3way decks in the catalog; this test would pass vacuously")
	}
}

// Not every trigger is a single word. "es geht jemandem (gut)" names a
// construction whose slot the phrase fills in, so it is never a literal substring
// of its own example. The store accepts such a row on its first word instead —
// this pins that, because the obvious tightening of the rule silently drops the
// card and shortens the deck by one.
func TestPatternTriggersSurviveTheirPlaceholders(t *testing.T) {
	triggers, err := seeddb.New().Deck(t.Context(), langcode.German, deckid.MustParse("de-verben-kasus"))
	if err != nil {
		t.Fatalf("loading verbs: %v", err)
	}

	var found bool
	for _, tr := range triggers {
		if !strings.Contains(tr.Word, "jemandem") && !strings.Contains(tr.Word, "(") {
			continue
		}
		found = true

		if strings.Contains(tr.Phrase, tr.Word) {
			continue // a pattern that happens to appear verbatim is fine too.
		}

		head, _, _ := strings.Cut(tr.Word, " ")
		if !strings.Contains(tr.Phrase, head) {
			t.Fatalf("pattern %q: phrase %q does not even contain its first word", tr.Word, tr.Phrase)
		}
	}

	if !found {
		t.Skip("no pattern triggers in the authored deck; nothing to pin")
	}
}

// An unknown deck is an error rather than an empty deck. A drill handed nothing
// would show a learner an empty round and call it finished.
func TestUnknownDeckIsAnError(t *testing.T) {
	if _, err := seeddb.New().Deck(t.Context(), langcode.German, deckid.DerDieDas); err == nil {
		t.Fatal("expected an error for a deck with no trigger items")
	}
}
