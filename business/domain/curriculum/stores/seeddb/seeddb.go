// Package seeddb is a read-only curriculumbus.Storer backed by authored catalog
// files compiled into the binary.
//
// A language's catalog is assembled from several files rather than one, because
// the decks were not written together and are not maintained together: the noun
// deck's entry sits alone in articles_de.json, and the two trigger decks share
// triggers_de.json with the items they will be drilled from. Concatenating the
// files in a fixed order is what makes the catalog an ordered course rather than
// a set — the order here is the order a learner meets the decks in.
//
// This is the Storage layer for the curriculum: it holds primitive JSON rows and
// converts them into Business Decks, which is where a bad drill name, an unknown
// prerequisite or an intro that points at an answer the deck does not accept is
// caught. A catalog that would parse into an invalid strong type fails at load,
// where the offending deck can be named, rather than at the moment a learner
// opens it.
package seeddb

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jroedel/uebung/business/domain/curriculum/curriculumbus"
	"github.com/jroedel/uebung/business/seeddata"
	"github.com/jroedel/uebung/business/types/deckid"
	"github.com/jroedel/uebung/business/types/drillkind"
	"github.com/jroedel/uebung/business/types/langcode"
)

// files is the authored content, which lives a layer down in business/seeddata
// because triggers_de.json is read by the trigger domain's store as well as this
// one — the same file carries a deck's catalog entry and the items it is drilled
// from. See that package for why it is not embedded here.
var files = seeddata.Files

// catalogFile is the on-disk shape of one authored file: a language and the decks
// it contributes to that language's catalog.
type catalogFile struct {
	Language    string    `json:"language"`
	Description string    `json:"description"`
	Decks       []deckRow `json:"decks"`
}

// deckRow is one catalog entry as authored: primitives only, no strong types.
//
// Items is not converted into anything. It is declared so the drill's material can
// live beside the catalog entry that describes it — which is how the trigger decks
// are written — and so that counting it gives the deck's size without a second
// source of truth. The items themselves get a domain when the drill that asks them
// is built.
type deckRow struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	Subtitle     string    `json:"subtitle"`
	Drill        string    `json:"drill"`
	Prerequisite string    `json:"prerequisite"`
	Answers      []string  `json:"answers"`
	Intro        introRow  `json:"intro"`
	Items        []itemRow `json:"items"`
}

// itemRow is one drill item. Only its presence matters here; the fields are
// declared so that a typo in the authored data still fails the strict decode
// rather than being silently dropped.
type itemRow struct {
	Trigger   string `json:"trigger"`
	Case      string `json:"case"`
	Gloss     string `json:"gloss"`
	Phrase    string `json:"phrase"`
	Example   string `json:"example"`
	ExampleEn string `json:"example_en"`
	Note      string `json:"note"`
}

type introRow struct {
	Heading string          `json:"heading"`
	Body    []string        `json:"body"`
	Groups  []introGroupRow `json:"groups"`
	Closing string          `json:"closing"`
}

type introGroupRow struct {
	Answer  string `json:"answer"`
	Label   string `json:"label"`
	Members string `json:"members"`
	Hook    string `json:"hook"`
}

// sourceFiles maps a language to the files whose decks make up its catalog, in
// the order a learner should meet them. Adding a deck is adding a file and one
// entry here, or appending to a file already listed.
var sourceFiles = map[string][]string{
	langcode.German.String(): {"articles_de.json", "triggers_de.json"},
}

// Store is the embedded catalog source. It is stateless; the data lives in the
// binary, so a single value is safe to share.
type Store struct{}

// New constructs the embedded catalog store.
func New() Store { return Store{} }

// Catalog implements curriculumbus.Storer. It concatenates the language's files
// in their listed order and converts every deck, failing the whole load if any
// entry is malformed — half a course is worse than a loud start-up failure.
func (Store) Catalog(_ context.Context, lang langcode.LangCode) ([]curriculumbus.Deck, error) {
	names, ok := sourceFiles[lang.String()]
	if !ok {
		return nil, fmt.Errorf("seeddb: no catalog for language %q", lang.String())
	}

	var decks []curriculumbus.Deck

	// seen doubles as the duplicate check and as the set a prerequisite must
	// already be in — which is what makes the catalog a chain that can only point
	// backwards, so no cycle and no forward reference can be authored.
	seen := map[deckid.DeckID]bool{}

	for _, name := range names {
		raw, err := files.ReadFile(name)
		if err != nil {
			return nil, fmt.Errorf("seeddb: reading %s: %w", name, err)
		}

		var cf catalogFile
		if err := json.Unmarshal(raw, &cf); err != nil {
			return nil, fmt.Errorf("seeddb: parsing %s: %w", name, err)
		}

		if cf.Language != lang.String() {
			return nil, fmt.Errorf("seeddb: %s declares language %q but is listed under %q",
				name, cf.Language, lang.String())
		}

		for i, row := range cf.Decks {
			d, err := toBusDeck(row, lang, seen)
			if err != nil {
				return nil, fmt.Errorf("seeddb: %s deck %d (%q): %w", name, i, row.ID, err)
			}

			seen[d.ID] = true
			decks = append(decks, d)
		}
	}

	if len(decks) == 0 {
		return nil, fmt.Errorf("seeddb: the %q catalog is empty", lang.String())
	}

	return decks, nil
}

// toBusDeck converts a primitive catalog row into a validated Business Deck. This
// is the Storage → Business boundary: every string is parsed into its strong type
// here, and any failure is returned rather than defaulted.
//
// earlier is the set of decks already in the catalog, used to reject a
// prerequisite that has not been met yet at load time.
func toBusDeck(row deckRow, lang langcode.LangCode, earlier map[deckid.DeckID]bool) (curriculumbus.Deck, error) {
	id, err := deckid.Parse(row.ID)
	if err != nil {
		return curriculumbus.Deck{}, fmt.Errorf("id: %w", err)
	}

	if earlier[id] {
		return curriculumbus.Deck{}, fmt.Errorf("id %q appears twice in the catalog", id.String())
	}

	if row.Title == "" {
		return curriculumbus.Deck{}, fmt.Errorf("empty title")
	}

	if row.Subtitle == "" {
		return curriculumbus.Deck{}, fmt.Errorf("empty subtitle")
	}

	drill, err := drillkind.Parse(row.Drill)
	if err != nil {
		return curriculumbus.Deck{}, fmt.Errorf("drill: %w", err)
	}

	answers, err := toBusAnswers(row.Answers)
	if err != nil {
		return curriculumbus.Deck{}, err
	}

	// An empty string is how a file says "nothing gates this deck", which is
	// different from a malformed id and has to stay different.
	var prereq deckid.DeckID
	if row.Prerequisite != "" {
		prereq, err = deckid.Parse(row.Prerequisite)
		if err != nil {
			return curriculumbus.Deck{}, fmt.Errorf("prerequisite: %w", err)
		}

		if !earlier[prereq] {
			return curriculumbus.Deck{}, fmt.Errorf(
				"prerequisite %q does not appear earlier in the catalog", prereq.String())
		}
	}

	intro, err := toBusIntro(row.Intro, answers)
	if err != nil {
		return curriculumbus.Deck{}, fmt.Errorf("intro: %w", err)
	}

	return curriculumbus.Deck{
		ID:           id,
		Lang:         lang,
		Title:        row.Title,
		Subtitle:     row.Subtitle,
		Drill:        drill,
		Answers:      answers,
		Prerequisite: prereq,
		Intro:        intro,
		Size:         len(row.Items),
	}, nil
}

// toBusAnswers validates the deck's reply set. Fewer than two answers is not a
// drill, and a repeated answer would put two identical buttons in front of a
// learner.
func toBusAnswers(raw []string) ([]string, error) {
	if len(raw) < 2 {
		return nil, fmt.Errorf("answers: a deck needs at least two, got %d", len(raw))
	}

	seen := make(map[string]bool, len(raw))
	answers := make([]string, 0, len(raw))
	for _, a := range raw {
		if a == "" {
			return nil, fmt.Errorf("answers: empty answer")
		}
		if seen[a] {
			return nil, fmt.Errorf("answers: %q listed twice", a)
		}

		seen[a] = true
		answers = append(answers, a)
	}

	return answers, nil
}

// toBusIntro validates the deck's introduction against the answers it accepts, so
// a group promising to explain an answer the deck never asks for is caught here
// rather than rendering as a block nobody can act on.
func toBusIntro(row introRow, answers []string) (curriculumbus.Intro, error) {
	if row.Heading == "" {
		return curriculumbus.Intro{}, fmt.Errorf("empty heading")
	}

	if len(row.Body) == 0 {
		return curriculumbus.Intro{}, fmt.Errorf("no body paragraphs")
	}

	for i, p := range row.Body {
		if p == "" {
			return curriculumbus.Intro{}, fmt.Errorf("body paragraph %d is empty", i)
		}
	}

	if row.Closing == "" {
		return curriculumbus.Intro{}, fmt.Errorf("empty closing")
	}

	valid := make(map[string]bool, len(answers))
	for _, a := range answers {
		valid[a] = true
	}

	groups := make([]curriculumbus.IntroGroup, 0, len(row.Groups))
	for i, g := range row.Groups {
		switch {
		case g.Label == "":
			return curriculumbus.Intro{}, fmt.Errorf("group %d: empty label", i)
		case g.Members == "":
			return curriculumbus.Intro{}, fmt.Errorf("group %d (%q): empty members", i, g.Label)
		case g.Hook == "":
			return curriculumbus.Intro{}, fmt.Errorf("group %d (%q): empty hook", i, g.Label)
		case g.Answer != "" && !valid[g.Answer]:
			return curriculumbus.Intro{}, fmt.Errorf(
				"group %d (%q): answer %q is not one the deck accepts", i, g.Label, g.Answer)
		}

		groups = append(groups, curriculumbus.IntroGroup{
			Answer:  g.Answer,
			Label:   g.Label,
			Members: g.Members,
			Hook:    g.Hook,
		})
	}

	return curriculumbus.Intro{
		Heading: row.Heading,
		Body:    row.Body,
		Groups:  groups,
		Closing: row.Closing,
	}, nil
}
