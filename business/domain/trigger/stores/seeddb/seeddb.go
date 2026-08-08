// Package seeddb is a read-only triggerbus.Storer backed by the authored deck
// files compiled into the binary.
//
// It reads the same file the curriculum's store reads, and takes the opposite
// half of it: the curriculum wants each deck's entry — its title, its gate, its
// introduction — and counts the items only to size the deck, while this store
// wants the items themselves and uses the entry only to find them. Both halves
// are authored together because a deck and its material are edited together, and
// the bytes live in business/seeddata so neither store owns the other's data.
//
// This is the Storage layer for triggers: it holds primitive JSON rows and
// converts them into Business Triggers via toBusTrigger, which is where a
// misspelled case or a phrase missing its own trigger is caught. A deck that
// would parse into an invalid strong type fails at load, where the offending row
// can be named, rather than at the moment a learner is shown it.
package seeddb

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jroedel/uebung/business/domain/trigger/triggerbus"
	"github.com/jroedel/uebung/business/seeddata"
	"github.com/jroedel/uebung/business/types/deckid"
	"github.com/jroedel/uebung/business/types/gcase"
	"github.com/jroedel/uebung/business/types/langcode"
)

// files is the authored content. See business/seeddata for why it is not embedded
// in this package.
var files = seeddata.Files

// deckFile is the on-disk shape of one authored file. Only the fields this store
// needs are declared — the catalog's own fields (title, intro, gate) are the
// curriculum store's business, and a strict decode here would reject them.
type deckFile struct {
	Language string    `json:"language"`
	Decks    []deckRow `json:"decks"`
}

// deckRow is one deck's entry, reduced to what locating its items requires.
type deckRow struct {
	ID    string    `json:"id"`
	Items []itemRow `json:"items"`
}

// itemRow is one drill item as authored: primitives only, no strong types.
type itemRow struct {
	Trigger   string `json:"trigger"`
	Case      string `json:"case"`
	Gloss     string `json:"gloss"`
	Phrase    string `json:"phrase"`
	Example   string `json:"example"`
	ExampleEn string `json:"example_en"`
	Note      string `json:"note"`
}

// sourceFiles maps a language to the files that may hold its trigger decks.
// A deck is found by searching them in order, so adding a deck is adding it to a
// listed file, or adding a file here.
var sourceFiles = map[string][]string{
	langcode.German.String(): {"triggers_de.json"},
}

// Store is the embedded trigger source. It is stateless; the data lives in the
// binary, so a single value is safe to share.
type Store struct{}

// New constructs the embedded trigger store.
func New() Store { return Store{} }

// Deck implements triggerbus.Storer. It finds the named deck among the language's
// files and converts every item, failing the whole load if any row is malformed —
// a deck that silently dropped its bad rows would teach a learner a shorter course
// than the one that was authored, and never say so.
func (Store) Deck(_ context.Context, lang langcode.LangCode, deck deckid.DeckID) ([]triggerbus.Trigger, error) {
	names, ok := sourceFiles[lang.String()]
	if !ok {
		return nil, fmt.Errorf("seeddb: no trigger decks for language %q", lang.String())
	}

	for _, name := range names {
		raw, err := files.ReadFile(name)
		if err != nil {
			return nil, fmt.Errorf("seeddb: reading %s: %w", name, err)
		}

		var df deckFile
		if err := json.Unmarshal(raw, &df); err != nil {
			return nil, fmt.Errorf("seeddb: parsing %s: %w", name, err)
		}

		for _, row := range df.Decks {
			if row.ID != deck.String() {
				continue
			}

			return toBusTriggers(row, name, lang)
		}
	}

	return nil, fmt.Errorf("seeddb: no trigger deck %q for language %q", deck.String(), lang.String())
}

// toBusTriggers converts every item in a deck, naming the file and row of the
// first failure so a bad character in authored data is a one-line fix rather than
// a hunt.
func toBusTriggers(row deckRow, filename string, lang langcode.LangCode) ([]triggerbus.Trigger, error) {
	// A deck entry that exists but carries no items is a deck whose material has
	// not been written. Returning it empty would leave the drill to discover that
	// with no cards and nothing to say, so it fails here where the deck is named.
	if len(row.Items) == 0 {
		return nil, fmt.Errorf("seeddb: deck %q in %s has no items", row.ID, filename)
	}

	out := make([]triggerbus.Trigger, 0, len(row.Items))

	// seen is the duplicate check. The trigger is the card's key within its deck,
	// so two rows sharing one would be two cards the scheduler cannot tell apart:
	// grading either would move both, and the deck would quietly be one card
	// shorter than its own count claims.
	seen := make(map[string]bool, len(row.Items))

	for i, item := range row.Items {
		t, err := toBusTrigger(item, lang)
		if err != nil {
			return nil, fmt.Errorf("seeddb: %s deck %q row %d (%q): %w", filename, row.ID, i, item.Trigger, err)
		}

		if seen[t.Word] {
			return nil, fmt.Errorf("seeddb: %s deck %q row %d: duplicate trigger %q", filename, row.ID, i, t.Word)
		}
		seen[t.Word] = true

		out = append(out, t)
	}

	return out, nil
}

// toBusTrigger converts one primitive row into a validated Business Trigger. This
// is the Storage → Business boundary: every string is parsed into its strong type
// here, and any failure is returned rather than defaulted.
func toBusTrigger(row itemRow, lang langcode.LangCode) (triggerbus.Trigger, error) {
	if row.Trigger == "" {
		return triggerbus.Trigger{}, fmt.Errorf("empty trigger")
	}

	c, err := gcase.Parse(row.Case)
	if err != nil {
		return triggerbus.Trigger{}, fmt.Errorf("case: %w", err)
	}

	if row.Gloss == "" {
		return triggerbus.Trigger{}, fmt.Errorf("empty gloss")
	}

	if row.Phrase == "" {
		return triggerbus.Trigger{}, fmt.Errorf("empty phrase")
	}

	// The phrase has to be about the trigger it is filed under. A row whose phrase
	// belongs to its neighbour is the likeliest way a hand-edited deck goes wrong,
	// and it is invisible afterwards: the card still renders, still grades, and
	// teaches the wrong pairing.
	//
	// Only the phrase is checked, never the example. A verb's example conjugates it
	// — "helfen" appears as "helfe" in "Ich helfe meinem Bruder" — so requiring the
	// example to contain its trigger would reject most of the verb deck.
	if !phraseDemonstrates(row.Phrase, row.Trigger) {
		return triggerbus.Trigger{}, fmt.Errorf("phrase %q does not demonstrate the trigger", row.Phrase)
	}

	if row.Example == "" {
		return triggerbus.Trigger{}, fmt.Errorf("empty example")
	}

	if row.ExampleEn == "" {
		return triggerbus.Trigger{}, fmt.Errorf("empty example translation")
	}

	return triggerbus.Trigger{
		Word:      row.Trigger,
		Case:      c,
		Gloss:     row.Gloss,
		Phrase:    row.Phrase,
		Example:   row.Example,
		ExampleEn: row.ExampleEn,
		Note:      row.Note,
		Lang:      lang,
	}, nil
}

// placeholders are the slot-fillers an authored trigger uses when it names a
// construction rather than a single word — "es geht jemandem (gut)". The slot is
// filled differently in every phrase, so such a trigger is never a literal
// substring of the phrase that demonstrates it.
var placeholders = []string{"(", "jemandem", "jemanden", "jemandes", "etwas"}

// phraseDemonstrates reports whether a phrase is plausibly an example of its
// trigger.
//
// A plain trigger must appear in its phrase verbatim, which is the check that
// catches a phrase pasted onto the wrong row. A trigger written as a pattern
// cannot be matched that way — its placeholder is filled in by the phrase — so
// the requirement falls back to its first word, which is the part that stays
// fixed. That is a weaker check, and it is deliberately confined to the rows that
// cannot satisfy the stronger one.
func phraseDemonstrates(phrase, trigger string) bool {
	if strings.Contains(phrase, trigger) {
		return true
	}

	isPattern := false
	for _, p := range placeholders {
		if strings.Contains(trigger, p) {
			isPattern = true
			break
		}
	}

	if !isPattern {
		return false
	}

	head, _, _ := strings.Cut(trigger, " ")

	return head != "" && strings.Contains(phrase, head)
}
