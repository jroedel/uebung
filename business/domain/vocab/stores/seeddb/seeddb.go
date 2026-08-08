// Package seeddb is a read-only vocabbus.Storer backed by curated noun lists
// compiled into the binary.
//
// The decks are authored JSON embedded with go:embed, so the program ships its
// vocabulary with no external file to deploy or database to seed. This is the
// Storage layer for vocab: it holds primitive JSON rows and converts them into
// the Business layer's strong Noun via toBusNoun, which is where a malformed
// gender or a bad language code is caught — a deck that would parse into an
// invalid strong type fails loudly at load, not silently at review time.
package seeddb

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jroedel/uebung/business/domain/vocab/vocabbus"
	"github.com/jroedel/uebung/business/seeddata"
	"github.com/jroedel/uebung/business/types/article"
	"github.com/jroedel/uebung/business/types/langcode"
)

// files is the authored content, which lives a layer down in business/seeddata
// alongside the other decks' files so that all course material sits in one place.
var files = seeddata.Files

// nounRow is one raw deck entry as it appears in the embedded JSON: primitives
// only, no strong types. It is the Storage-edge representation.
type nounRow struct {
	Noun      string `json:"noun"`
	Article   string `json:"article"`
	Gloss     string `json:"gloss"`
	Example   string `json:"example"`
	ExampleEn string `json:"example_en"`
}

// deckFile is the on-disk shape of one language's embedded deck.
type deckFile struct {
	Language string    `json:"language"`
	Module   string    `json:"module"`
	Nouns    []nounRow `json:"nouns"`
}

// sourceFiles maps a language code to the embedded file that holds its deck.
// Adding a language is adding a file and one entry here.
var sourceFiles = map[string]string{
	langcode.German.String(): "nouns_de.json",
}

// Store is the embedded deck source. It is stateless; the data lives in the
// binary, so a single value is safe to share.
type Store struct{}

// New constructs the embedded deck store.
func New() Store { return Store{} }

// Deck implements vocabbus.Storer. It loads the language's embedded file and
// converts every row into a validated Noun, failing the whole load if any row is
// malformed — a partial deck would silently teach the wrong genders.
func (Store) Deck(_ context.Context, lang langcode.LangCode) ([]vocabbus.Noun, error) {
	filename, ok := sourceFiles[lang.String()]
	if !ok {
		return nil, fmt.Errorf("seeddb: no deck for language %q", lang.String())
	}

	raw, err := files.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("seeddb: reading %s: %w", filename, err)
	}

	var df deckFile
	if err := json.Unmarshal(raw, &df); err != nil {
		return nil, fmt.Errorf("seeddb: parsing %s: %w", filename, err)
	}

	nouns := make([]vocabbus.Noun, 0, len(df.Nouns))
	for i, row := range df.Nouns {
		n, err := toBusNoun(row, lang)
		if err != nil {
			return nil, fmt.Errorf("seeddb: %s row %d (%q): %w", filename, i, row.Noun, err)
		}

		nouns = append(nouns, n)
	}

	return nouns, nil
}

// toBusNoun converts a primitive deck row into a validated Business Noun. This is
// the Storage → Business boundary: every string is parsed into its strong type
// here, and any failure is returned rather than defaulted.
func toBusNoun(row nounRow, lang langcode.LangCode) (vocabbus.Noun, error) {
	if row.Noun == "" {
		return vocabbus.Noun{}, fmt.Errorf("empty noun")
	}

	art, err := article.Parse(row.Article)
	if err != nil {
		return vocabbus.Noun{}, fmt.Errorf("article: %w", err)
	}

	if row.Gloss == "" {
		return vocabbus.Noun{}, fmt.Errorf("empty gloss")
	}

	if row.Example == "" {
		return vocabbus.Noun{}, fmt.Errorf("empty example")
	}

	// An example that does not contain its own noun is useless for the review it
	// exists to serve, and is the likeliest way a hand-edited deck goes wrong —
	// a sentence pasted onto the wrong row. Catch it at load rather than showing
	// a learner a sentence about something else.
	if !strings.Contains(row.Example, row.Noun) {
		return vocabbus.Noun{}, fmt.Errorf("example %q does not contain the noun", row.Example)
	}

	if row.ExampleEn == "" {
		return vocabbus.Noun{}, fmt.Errorf("empty example translation")
	}

	return vocabbus.Noun{
		Lemma:     row.Noun,
		Article:   art,
		Gloss:     row.Gloss,
		Example:   row.Example,
		ExampleEn: row.ExampleEn,
		Lang:      lang,
	}, nil
}
