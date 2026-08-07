package deckid_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/jroedel/uebung/business/types/deckid"
)

func TestParseAcceptsSlugs(t *testing.T) {
	valid := []string{
		"a",
		"de",
		"der-die-das",
		"de-preps-akk-dat",
		"module2",
		"a1-b2-c3",
		strings.Repeat("a", 64),
	}

	for _, s := range valid {
		got, err := deckid.Parse(s)
		if err != nil {
			t.Errorf("Parse(%q) errored: %v", s, err)
			continue
		}
		if got.String() != s {
			t.Errorf("Parse(%q).String() = %q, want %q", s, got.String(), s)
		}
		if got.IsZero() {
			t.Errorf("Parse(%q) reported as the zero value", s)
		}
	}
}

func TestParseRejectsAnythingElse(t *testing.T) {
	// Each of these would either need escaping in a URL, or would read in a deck
	// list as the same name as something else.
	invalid := map[string]string{
		"empty":            "",
		"too long":         strings.Repeat("a", 65),
		"uppercase":        "Der-Die-Das",
		"underscore":       "der_die_das",
		"space":            "der die das",
		"leading hyphen":   "-nouns",
		"trailing hyphen":  "nouns-",
		"doubled hyphen":   "der--die",
		"only a hyphen":    "-",
		"slash":            "de/nouns",
		"dot":              "de.nouns",
		"umlaut":           "übung",
		"percent encoding": "de%20nouns",
	}

	for name, s := range invalid {
		got, err := deckid.Parse(s)
		if err == nil {
			t.Errorf("%s: Parse(%q) was accepted, want a rejection", name, s)
			continue
		}
		if !errors.Is(err, deckid.ErrInvalidDeckID) {
			t.Errorf("%s: Parse(%q) returned %v, want ErrInvalidDeckID", name, s, err)
		}
		if !got.IsZero() {
			t.Errorf("%s: Parse(%q) returned %q alongside its error", name, s, got)
		}
	}
}

// The zero value must not pass for a real deck: a struct that forgot to set its
// deck has to fail a check rather than land in some default and mix two learners'
// material together.
func TestZeroValueIsUnusable(t *testing.T) {
	var d deckid.DeckID

	if !d.IsZero() {
		t.Error("the zero DeckID does not report as zero")
	}
	if d.String() != "" {
		t.Errorf("the zero DeckID stringifies as %q, want an empty string", d.String())
	}
}

// The German gender deck is the deck every pre-decks study record is backfilled
// to, so its slug is written into a live database by the migration. Changing it
// silently orphans every existing card.
func TestDerDieDasIsStable(t *testing.T) {
	if got := deckid.DerDieDas.String(); got != "der-die-das" {
		t.Errorf("DerDieDas = %q, want %q -- every migrated row points at the old value", got, "der-die-das")
	}
}

func TestMustParsePanicsOnBadInput(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("MustParse did not panic on invalid input")
		}
	}()

	deckid.MustParse("Not A Slug")
}
