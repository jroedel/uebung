package seeddb

import (
	"context"
	"strings"
	"testing"

	"github.com/jroedel/uebung/business/types/article"
	"github.com/jroedel/uebung/business/types/langcode"
)

// The embedded deck must load, be non-trivial, carry no duplicate lemmas, and
// have a valid gender on every card. This is the guard that a data edit can't
// ship a broken deck.
func TestGermanDeckLoadsAndIsWellFormed(t *testing.T) {
	deck, err := New().Deck(context.Background(), langcode.German)
	if err != nil {
		t.Fatalf("loading German deck: %v", err)
	}

	if len(deck) < 200 {
		t.Fatalf("deck has %d nouns, expected the top ~200", len(deck))
	}

	seen := make(map[string]bool, len(deck))
	for _, n := range deck {
		if n.Lemma == "" {
			t.Fatal("a noun has an empty lemma")
		}
		if n.Article.IsZero() {
			t.Fatalf("%q has no article", n.Lemma)
		}
		if n.Gloss == "" {
			t.Fatalf("%q has no gloss", n.Lemma)
		}
		if n.Example == "" {
			t.Fatalf("%q has no example sentence", n.Lemma)
		}
		if n.ExampleEn == "" {
			t.Fatalf("%q has no example translation", n.Lemma)
		}
		// The example exists to show the learner this noun. A sentence that does
		// not contain it is the likeliest hand-edit mistake, and the one a
		// learner would notice immediately.
		if !strings.Contains(n.Example, n.Lemma) {
			t.Fatalf("%q example %q does not contain the noun", n.Lemma, n.Example)
		}
		if n.Lang != langcode.German {
			t.Fatalf("%q tagged with wrong language %q", n.Lemma, n.Lang)
		}
		if seen[n.Lemma] {
			t.Fatalf("duplicate lemma %q", n.Lemma)
		}
		seen[n.Lemma] = true
	}
}

// A spot-check of genders that are easy to get wrong pins the data against
// silent corruption (e.g. das Mädchen, das Herz, der Name).
func TestKnownTrickyGenders(t *testing.T) {
	deck, err := New().Deck(context.Background(), langcode.German)
	if err != nil {
		t.Fatalf("loading deck: %v", err)
	}

	byLemma := make(map[string]article.Article, len(deck))
	for _, n := range deck {
		byLemma[n.Lemma] = n.Article
	}

	want := map[string]article.Article{
		"Mädchen": article.Das,
		"Herz":    article.Das,
		"Name":    article.Der,
		"Zeit":    article.Die,
		"Auge":    article.Das,
		"Junge":   article.Der,
	}
	for lemma, art := range want {
		got, ok := byLemma[lemma]
		if !ok {
			t.Fatalf("expected %q in the deck", lemma)
		}
		if got != art {
			t.Fatalf("%q gender = %q, want %q", lemma, got, art)
		}
	}
}

// toBusNoun is the guard that stops a bad hand-edit reaching a learner, so the
// rejections themselves are worth pinning: a row that parses but is wrong is
// exactly what the deck's "corrections welcome" invitation risks.
func TestMalformedRowsAreRejected(t *testing.T) {
	good := nounRow{Noun: "Haus", Article: "das", Gloss: "house",
		Example: "Das Haus steht leer.", ExampleEn: "The house is empty."}

	if _, err := toBusNoun(good, langcode.German); err != nil {
		t.Fatalf("a well-formed row was rejected: %v", err)
	}

	tests := map[string]func(nounRow) nounRow{
		"empty example":            func(r nounRow) nounRow { r.Example = ""; return r },
		"empty translation":        func(r nounRow) nounRow { r.ExampleEn = ""; return r },
		"example missing the noun": func(r nounRow) nounRow { r.Example = "Es steht leer."; return r },
	}

	for name, break_ := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := toBusNoun(break_(good), langcode.German); err == nil {
				t.Fatal("expected the row to be rejected")
			}
		})
	}
}

func TestUnknownLanguageFails(t *testing.T) {
	if _, err := New().Deck(context.Background(), langcode.MustParse("fr")); err == nil {
		t.Fatal("expected an error for a language with no embedded deck")
	}
}
