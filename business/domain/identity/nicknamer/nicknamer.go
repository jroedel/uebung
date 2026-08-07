// Package nicknamer proposes display names for learners who would rather not
// think one up.
//
// A name is an adjective and a noun: "Blaue Eule", "Dunkler Hund", "Leuchtendes
// Pferd". The adjective is declined to agree with the noun's gender, which is
// the whole reason the noun list carries an article. Getting that wrong would be
// a peculiar thing to ship inside an app whose only subject is German
// grammatical gender, so the agreement is done properly rather than by picking a
// form that reads acceptably most of the time.
//
// The declension used is the strong nominative singular — the form a bare
// adjective takes with no article in front of it, which is what a name is:
//
//	der → -er    blauer Hund
//	die → -e     blaue Eule
//	das → -es    blaues Pferd
//
// Adjectives are stored as the stem the ending attaches to, not as the
// dictionary form. For most words those coincide ("blau" → "blauer"), but German
// contracts the ones ending in -el and -er ("dunkel" → "dunkler", never
// "dunkeler"), and "hoch" loses its ch ("hoch" → "hoher"). Storing "dunkl" and
// "hoh" keeps the irregularity in the data, where it can be checked by eye,
// instead of in a rules function that would have to guess which words it applies
// to.
package nicknamer

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jroedel/uebung/business/types/article"
	"github.com/jroedel/uebung/business/types/nickname"
)

// The word lists are embedded rather than read from disk: they are part of the
// program, and a deployment that forgot to copy a data file should fail to build
// rather than to start.
var (
	//go:embed adjectives_de.json
	adjectivesJSON []byte

	//go:embed nouns_de.json
	nounsJSON []byte
)

// Budgets that keep every name the generator can produce inside
// nickname.MaxLen, checked against the real lists in New.
//
// shortMax* bound the pool used for numbered names. A number is only reached
// for after several plain proposals have collided, and it needs room after the
// name, so those draws come from the shorter words. The pools stay large — most
// of the list is short — so a numbered name is still not a predictable one.
const (
	maxAdjectiveStem = 10
	maxNoun          = 11
	shortMaxStem     = 7
	shortMaxNoun     = 8

	// maxSuffix is the room reserved for " 999".
	maxSuffix = 4

	// plainAttempts is how many un-numbered names are proposed before falling
	// back to a numbered one. Three is enough that an ordinary learner never
	// sees a number: with ~94,000 pairs, three consecutive collisions needs the
	// site to be very large indeed.
	plainAttempts = 3

	// maxNumber bounds the numeric suffix, and maxSuffix reserves room for it.
	maxNumber = 999
)

// noun is one entry of the noun list: the word and the gender its adjective has
// to agree with.
type noun struct {
	Noun    string `json:"noun"`
	Article string `json:"article"`
}

// declinedNoun is a noun with its article already parsed, which is how the
// generator holds it after New has validated the list.
type declinedNoun struct {
	word   string
	gender article.Article
}

// Generator builds names from the embedded word lists.
type Generator struct {
	adjectives []string
	nouns      []declinedNoun

	// The short pools are index lists into the slices above, so a numbered name
	// costs a lookup rather than a scan.
	shortAdjectives []int
	shortNouns      []int

	randN func(n int) int
}

// Config tunes a Generator.
type Config struct {
	// RandN returns a number in [0, n). Injected so a test can pin the choice;
	// production leaves it nil and gets math/rand/v2.
	RandN func(n int) int
}

// New loads and validates the word lists.
//
// The validation is not ceremony. These lists are hand-written and will be added
// to by hand, and the failure they invite is a word that pushes some pairs over
// nickname.MaxLen — which would show up as a rejected name for a fraction of
// learners and be invisible until then. Checking at construction turns that into
// a startup error naming the word.
func New(cfg Config) (*Generator, error) {
	var stems []string
	if err := json.Unmarshal(adjectivesJSON, &stems); err != nil {
		return nil, fmt.Errorf("nicknamer: reading adjectives: %w", err)
	}

	var entries []noun
	if err := json.Unmarshal(nounsJSON, &entries); err != nil {
		return nil, fmt.Errorf("nicknamer: reading nouns: %w", err)
	}

	if len(stems) == 0 || len(entries) == 0 {
		return nil, errors.New("nicknamer: both word lists must be non-empty")
	}

	g := Generator{
		adjectives: stems,
		nouns:      make([]declinedNoun, 0, len(entries)),
		randN:      cfg.RandN,
	}

	if g.randN == nil {
		g.randN = rand.IntN
	}

	for i, stem := range stems {
		n := utf8.RuneCountInString(stem)
		switch {
		case n == 0:
			return nil, fmt.Errorf("nicknamer: adjective %d is empty", i)
		case n > maxAdjectiveStem:
			return nil, fmt.Errorf("nicknamer: adjective %q is %d characters, over the %d budget", stem, n, maxAdjectiveStem)
		}

		if n <= shortMaxStem {
			g.shortAdjectives = append(g.shortAdjectives, i)
		}
	}

	for _, e := range entries {
		gender, err := article.Parse(e.Article)
		if err != nil {
			return nil, fmt.Errorf("nicknamer: noun %q: %w", e.Noun, err)
		}

		n := utf8.RuneCountInString(e.Noun)
		switch {
		case n == 0:
			return nil, errors.New("nicknamer: a noun entry is empty")
		case n > maxNoun:
			return nil, fmt.Errorf("nicknamer: noun %q is %d characters, over the %d budget", e.Noun, n, maxNoun)
		}

		if n <= shortMaxNoun {
			g.shortNouns = append(g.shortNouns, len(g.nouns))
		}

		g.nouns = append(g.nouns, declinedNoun{word: e.Noun, gender: gender})
	}

	if len(g.shortAdjectives) == 0 || len(g.shortNouns) == 0 {
		return nil, errors.New("nicknamer: no short words left for numbered names")
	}

	// The budgets have to actually add up. Stated as a check rather than a
	// comment so that raising nickname.MaxLen, or one of the constants above,
	// cannot quietly break the guarantee that every produced name is valid.
	if longest := maxAdjectiveStem + len("es") + len(" ") + maxNoun; longest > nickname.MaxLen {
		return nil, fmt.Errorf("nicknamer: longest possible name is %d characters, over the %d limit", longest, nickname.MaxLen)
	}
	if longest := shortMaxStem + len("es") + len(" ") + shortMaxNoun + maxSuffix; longest > nickname.MaxLen {
		return nil, fmt.Errorf("nicknamer: longest possible numbered name is %d characters, over the %d limit", longest, nickname.MaxLen)
	}

	return &g, nil
}

// Generate proposes a name.
//
// attempt is how many previous proposals were already taken. The first few are
// plain pairs; after that a number is added, because repeated collisions mean
// the pair space is not helping and a learner waiting on a name should get one.
//
// The returned error is for a name the word lists should never have been able to
// produce — one caught by nickname's screening or character rules. It cannot
// happen with the lists as they stand, which the package test asserts across the
// whole cross product, and it is returned rather than ignored so that a future
// addition to the lists surfaces as a failure instead of as a name nobody can be
// given.
func (g *Generator) Generate(attempt int) (nickname.Nickname, error) {
	if attempt < plainAttempts {
		return g.build(g.pickAdjective(), g.pickNoun(), "")
	}

	adjective := g.adjectives[g.shortAdjectives[g.randN(len(g.shortAdjectives))]]
	subject := g.nouns[g.shortNouns[g.randN(len(g.shortNouns))]]

	// From 2, because the unnumbered name is conceptually the first one.
	suffix := " " + strconv.Itoa(2+g.randN(maxNumber-1))

	return g.build(adjective, subject, suffix)
}

// build assembles one name and parses it, so nothing leaves this package that
// would not have been accepted had a person typed it.
func (g *Generator) build(stem string, subject declinedNoun, suffix string) (nickname.Nickname, error) {
	var b strings.Builder
	b.WriteString(capitalize(stem))
	b.WriteString(endingFor(subject.gender))
	b.WriteString(" ")
	b.WriteString(subject.word)
	b.WriteString(suffix)

	name, err := nickname.Parse(b.String())
	if err != nil {
		return nickname.Nickname{}, fmt.Errorf("nicknamer: generated %q: %w", b.String(), err)
	}

	return name, nil
}

func (g *Generator) pickAdjective() string {
	return g.adjectives[g.randN(len(g.adjectives))]
}

func (g *Generator) pickNoun() declinedNoun {
	return g.nouns[g.randN(len(g.nouns))]
}

// endingFor returns the strong nominative singular ending for a gender.
func endingFor(gender article.Article) string {
	switch gender {
	case article.Der:
		return "er"
	case article.Die:
		return "e"
	case article.Das:
		return "es"
	default:
		// Unreachable: New parses every article before a noun is kept.
		return "e"
	}
}

// capitalize upper-cases the first rune. It decodes rather than indexing because
// several stems begin with an umlaut ("östlich"), and s[0] would split one in
// half.
func capitalize(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError {
		return s
	}

	return string(unicode.ToUpper(r)) + s[size:]
}
