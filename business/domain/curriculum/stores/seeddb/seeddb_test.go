package seeddb

import (
	"context"
	"strings"
	"testing"

	"github.com/jroedel/uebung/business/types/deckid"
	"github.com/jroedel/uebung/business/types/drillkind"
	"github.com/jroedel/uebung/business/types/langcode"
)

// The embedded catalog must load, name the decks the rest of the program expects,
// and present them as a chain. Every check here is on authored data, so a failure
// means a file was edited into a state the program cannot use — which is the whole
// reason the conversion validates rather than defaults.
func TestGermanCatalogLoads(t *testing.T) {
	decks, err := New().Catalog(context.Background(), langcode.German)
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}

	if len(decks) < 2 {
		t.Fatalf("the catalog holds %d deck(s); the course needs more than one", len(decks))
	}

	// The noun deck has to come first and has to be ungated: it is the deck every
	// pre-existing study record was migrated into, and gating it would lock a
	// learner out of their own history.
	if decks[0].ID != deckid.DerDieDas {
		t.Errorf("the first deck is %q, want %q", decks[0].ID, deckid.DerDieDas)
	}
	if !decks[0].Prerequisite.IsZero() {
		t.Errorf("the first deck requires %q; nothing may gate it", decks[0].Prerequisite)
	}
	if decks[0].Drill != drillkind.ArticleThreeWay {
		t.Errorf("the noun deck's drill is %q, want %q", decks[0].Drill, drillkind.ArticleThreeWay)
	}

	seen := map[deckid.DeckID]bool{}
	for i, d := range decks {
		if seen[d.ID] {
			t.Errorf("deck %d: %q appears twice", i, d.ID)
		}
		seen[d.ID] = true

		if d.Lang != langcode.German {
			t.Errorf("deck %q carries language %q, want %q", d.ID, d.Lang, langcode.German)
		}
		if d.Drill.IsZero() {
			t.Errorf("deck %q has no drill", d.ID)
		}
		if len(d.Answers) < 2 {
			t.Errorf("deck %q offers %d answer(s)", d.ID, len(d.Answers))
		}

		// The chain has to point backwards, or the picker would show a deck gated
		// behind one a learner has not been offered yet.
		if !d.Prerequisite.IsZero() && !seen[d.Prerequisite] {
			t.Errorf("deck %q requires %q, which does not appear before it", d.ID, d.Prerequisite)
		}
	}
}

// The intro is the deck's whole reason for existing before the drill does, so an
// empty one is a broken deck rather than a cosmetic gap.
func TestEveryDeckIntroducesItself(t *testing.T) {
	decks, err := New().Catalog(context.Background(), langcode.German)
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}

	for _, d := range decks {
		if d.Intro.Heading == "" {
			t.Errorf("deck %q has no intro heading", d.ID)
		}
		if len(d.Intro.Body) == 0 {
			t.Errorf("deck %q has no intro body", d.ID)
		}
		if d.Intro.Closing == "" {
			t.Errorf("deck %q has no intro closing", d.ID)
		}
		if len(d.Intro.Groups) == 0 {
			t.Errorf("deck %q lays out no groups", d.ID)
		}

		answers := map[string]bool{}
		for _, a := range d.Answers {
			answers[a] = true
		}

		for _, g := range d.Intro.Groups {
			if g.Answer != "" && !answers[g.Answer] {
				t.Errorf("deck %q: intro group %q explains answer %q, which the deck never asks for",
					d.ID, g.Label, g.Answer)
			}
		}
	}
}

// The noun deck's items live in the vocab domain, so its catalog entry reports no
// size and the App layer resolves one. Every other deck carries its own items and
// must count them, or the unlock rule would be dividing by a size of zero and open
// the next deck immediately.
func TestOnlyTheNounDeckDefersItsSize(t *testing.T) {
	decks, err := New().Catalog(context.Background(), langcode.German)
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}

	for _, d := range decks {
		size := d.Size

		switch {
		case d.ID == deckid.DerDieDas && size != 0:
			t.Errorf("the noun deck reports size %d; its items belong to the vocab domain", size)
		case d.ID != deckid.DerDieDas && size == 0:
			t.Errorf("deck %q carries no items, so nothing can be drilled from it", d.ID)
		}
	}
}

func TestCatalogRejectsAnUnknownLanguage(t *testing.T) {
	_, err := New().Catalog(context.Background(), langcode.MustParse("xx"))
	if err == nil {
		t.Fatal("Catalog returned a course for a language with no files")
	}
}

// --- conversion ------------------------------------------------------------
//
// These exercise the Storage → Business boundary directly. The authored files are
// valid by construction, so the only way to see what happens to a bad one is to
// hand the converter the row an editing mistake would produce.

func validRow() deckRow {
	return deckRow{
		ID:       "a-deck",
		Title:    "A deck",
		Subtitle: "Something to learn",
		Drill:    "case-3way",
		Answers:  []string{"akkusativ", "dativ"},
		Intro: introRow{
			Heading: "The idea",
			Body:    []string{"A paragraph."},
			Groups:  []introGroupRow{{Answer: "dativ", Label: "Dative", Members: "mit, nach", Hook: "Chant them."}},
			Closing: "When stuck, guess dative.",
		},
		Items: []itemRow{{Trigger: "mit", Case: "dativ"}},
	}
}

func TestToBusDeckRejectsBadRows(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*deckRow)
		earlier map[deckid.DeckID]bool
		wantIn  string
	}{
		{
			name:   "unparseable id",
			mutate: func(r *deckRow) { r.ID = "A Deck" },
			wantIn: "id",
		},
		{
			name:   "empty title",
			mutate: func(r *deckRow) { r.Title = "" },
			wantIn: "title",
		},
		{
			name:   "unknown drill",
			mutate: func(r *deckRow) { r.Drill = "typing" },
			wantIn: "drill",
		},
		{
			name:   "one answer is not a drill",
			mutate: func(r *deckRow) { r.Answers = []string{"dativ"} },
			wantIn: "answers",
		},
		{
			name:   "duplicate answer",
			mutate: func(r *deckRow) { r.Answers = []string{"dativ", "dativ"} },
			wantIn: "listed twice",
		},
		{
			// The one that matters most: it is how a deck would be authored behind a
			// prerequisite that does not exist, or behind itself.
			name:   "prerequisite that has not been seen",
			mutate: func(r *deckRow) { r.Prerequisite = "some-other-deck" },
			wantIn: "does not appear earlier",
		},
		{
			name:   "prerequisite that is not a slug",
			mutate: func(r *deckRow) { r.Prerequisite = "Some Deck" },
			wantIn: "prerequisite",
		},
		{
			name:   "intro group promising an answer the deck never asks for",
			mutate: func(r *deckRow) { r.Intro.Groups[0].Answer = "genitiv" },
			wantIn: "not one the deck accepts",
		},
		{
			name:   "intro with no body",
			mutate: func(r *deckRow) { r.Intro.Body = nil },
			wantIn: "body",
		},
		{
			name:   "intro group with no hook",
			mutate: func(r *deckRow) { r.Intro.Groups[0].Hook = "" },
			wantIn: "hook",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			row := validRow()
			tt.mutate(&row)

			_, err := toBusDeck(row, langcode.German, map[deckid.DeckID]bool{})
			if err == nil {
				t.Fatalf("toBusDeck accepted %s", tt.name)
			}
			if !strings.Contains(err.Error(), tt.wantIn) {
				t.Errorf("error %q does not mention %q", err, tt.wantIn)
			}
		})
	}
}

func TestToBusDeckAcceptsAValidRow(t *testing.T) {
	row := validRow()
	row.Prerequisite = "der-die-das"

	d, err := toBusDeck(row, langcode.German, map[deckid.DeckID]bool{deckid.DerDieDas: true})
	if err != nil {
		t.Fatalf("toBusDeck: %v", err)
	}

	if d.ID.String() != "a-deck" {
		t.Errorf("ID = %q, want %q", d.ID, "a-deck")
	}
	if d.Prerequisite != deckid.DerDieDas {
		t.Errorf("Prerequisite = %q, want %q", d.Prerequisite, deckid.DerDieDas)
	}
	if d.Drill != drillkind.CaseThreeWay {
		t.Errorf("Drill = %q, want %q", d.Drill, drillkind.CaseThreeWay)
	}
	if d.Size != 1 {
		t.Errorf("Size = %d, want 1", d.Size)
	}
	if d.Lang != langcode.German {
		t.Errorf("Lang = %q, want %q", d.Lang, langcode.German)
	}
}

// A deck with no prerequisite is the normal case for the first deck in a course,
// and the empty string is how a file says so. It must not be confused with a
// malformed id.
func TestAnEmptyPrerequisiteMeansUngated(t *testing.T) {
	row := validRow()
	row.Prerequisite = ""

	d, err := toBusDeck(row, langcode.German, map[deckid.DeckID]bool{})
	if err != nil {
		t.Fatalf("toBusDeck: %v", err)
	}

	if !d.Prerequisite.IsZero() {
		t.Errorf("Prerequisite = %q, want the zero deck", d.Prerequisite)
	}
}
