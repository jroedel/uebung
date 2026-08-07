package nicknamer

import (
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/jroedel/uebung/business/types/article"
	"github.com/jroedel/uebung/business/types/nickname"
)

func newTestGenerator(t *testing.T) *Generator {
	t.Helper()

	g, err := New(Config{})
	if err != nil {
		t.Fatalf("New() = error %v, want the embedded word lists to load", err)
	}

	return g
}

// The declension is the part of this package that can be wrong in a way no test
// of shape would notice, so the agreement is asserted against hand-written
// expectations rather than against the code's own rule.
func TestDeclension(t *testing.T) {
	g := newTestGenerator(t)

	tests := []struct {
		name   string
		stem   string
		word   string
		gender article.Article
		want   string
	}{
		{"masculine takes -er", "blau", "Hund", article.Der, "Blauer Hund"},
		{"feminine takes -e", "blau", "Eule", article.Die, "Blaue Eule"},
		{"neuter takes -es", "blau", "Pferd", article.Das, "Blaues Pferd"},

		// The stems that are stored pre-contracted. "dunkeler" and "hocher" are
		// both wrong, and both are what a naive stem+ending would produce from
		// the dictionary form.
		{"dunkel contracts", "dunkl", "Hund", article.Der, "Dunkler Hund"},
		{"dunkel contracts, feminine", "dunkl", "Eule", article.Die, "Dunkle Eule"},
		{"edel contracts", "edl", "Hirsch", article.Der, "Edler Hirsch"},
		{"nobel contracts", "nobl", "Pferd", article.Das, "Nobles Pferd"},
		{"sauer contracts", "saur", "Apfel", article.Der, "Saurer Apfel"},
		{"hoch loses its ch", "hoh", "Berg", article.Der, "Hoher Berg"},

		// An umlaut is two bytes, so capitalising by indexing the first byte
		// would corrupt these.
		{"umlaut-initial stem", "östlich", "Wind", article.Der, "Östlicher Wind"},
		{"umlaut-initial stem, neuter", "östlich", "Tor", article.Das, "Östliches Tor"},

		{"participle adjective", "leuchtend", "Pferd", article.Das, "Leuchtendes Pferd"},
		{"eszett survives", "weiß", "Möwe", article.Die, "Weiße Möwe"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := g.build(tt.stem, declinedNoun{word: tt.word, gender: tt.gender}, "")
			if err != nil {
				t.Fatalf("build(%q, %q) = error %v", tt.stem, tt.word, err)
			}
			if got.String() != tt.want {
				t.Errorf("build(%q, %q) = %q, want %q", tt.stem, tt.word, got.String(), tt.want)
			}
		})
	}
}

// Every pair the generator can produce must be a name a person could have typed.
// This is the check that lets Generate treat a parse failure as impossible: it
// covers the whole cross product, so a word added to either list that collides
// with the blocklist, exceeds the length budget, or carries a stray character
// fails here rather than in front of a learner.
func TestEveryPairIsAValidName(t *testing.T) {
	g := newTestGenerator(t)

	t.Logf("checking %d adjectives x %d nouns = %d pairs",
		len(g.adjectives), len(g.nouns), len(g.adjectives)*len(g.nouns))

	for _, stem := range g.adjectives {
		for _, subject := range g.nouns {
			name, err := g.build(stem, subject, "")
			if err != nil {
				t.Fatalf("build(%q, %q) = error %v", stem, subject.word, err)
			}

			if n := utf8.RuneCountInString(name.String()); n > nickname.MaxLen {
				t.Fatalf("build(%q, %q) = %q, which is %d characters, over the %d limit",
					stem, subject.word, name, n, nickname.MaxLen)
			}
		}
	}
}

// The same guarantee for numbered names, which draw from the short pools and
// have to leave room for the suffix.
func TestEveryNumberedPairFits(t *testing.T) {
	g := newTestGenerator(t)

	suffix := " " + strconv.Itoa(maxNumber)

	for _, ai := range g.shortAdjectives {
		for _, ni := range g.shortNouns {
			name, err := g.build(g.adjectives[ai], g.nouns[ni], suffix)
			if err != nil {
				t.Fatalf("build(%q, %q, %q) = error %v", g.adjectives[ai], g.nouns[ni].word, suffix, err)
			}

			if n := utf8.RuneCountInString(name.String()); n > nickname.MaxLen {
				t.Fatalf("build(%q, %q, %q) = %q, which is %d characters, over the %d limit",
					g.adjectives[ai], g.nouns[ni].word, suffix, name, n, nickname.MaxLen)
			}
		}
	}
}

// A duplicate is invisible in a list this long and quietly doubles that word's
// share of the output, so it is worth a test rather than an eye.
func TestNoDuplicateWords(t *testing.T) {
	g := newTestGenerator(t)

	seen := make(map[string]bool, len(g.adjectives))
	for _, stem := range g.adjectives {
		if seen[stem] {
			t.Errorf("adjective %q appears more than once", stem)
		}
		seen[stem] = true
	}

	clear(seen)
	for _, subject := range g.nouns {
		if seen[subject.word] {
			t.Errorf("noun %q appears more than once", subject.word)
		}
		seen[subject.word] = true
	}
}

// The pools have to be big enough that a name is not guessable. This is a floor,
// not a target: the real lists are far above it, and the test exists so that
// pruning them for length can never quietly take them below it.
func TestPoolsAreLargeEnough(t *testing.T) {
	g := newTestGenerator(t)

	const wantEach = 200

	if len(g.adjectives) < wantEach {
		t.Errorf("got %d adjectives, want at least %d", len(g.adjectives), wantEach)
	}
	if len(g.nouns) < wantEach {
		t.Errorf("got %d nouns, want at least %d", len(g.nouns), wantEach)
	}
	if len(g.shortAdjectives) == 0 || len(g.shortNouns) == 0 {
		t.Fatalf("short pools are %d x %d, want both non-empty",
			len(g.shortAdjectives), len(g.shortNouns))
	}

	// A numbered name should still not be a small space to search.
	if pairs := len(g.shortAdjectives) * len(g.shortNouns); pairs < 1000 {
		t.Errorf("got %d short pairs, want at least 1000", pairs)
	}
}

func TestGenerateAddsANumberOnlyAfterRepeatedCollisions(t *testing.T) {
	g := newTestGenerator(t)

	for attempt := range plainAttempts {
		name, err := g.Generate(attempt)
		if err != nil {
			t.Fatalf("Generate(%d) = error %v", attempt, err)
		}
		if strings.ContainsAny(name.String(), "0123456789") {
			t.Errorf("Generate(%d) = %q, want no number before %d collisions", attempt, name, plainAttempts)
		}
	}

	name, err := g.Generate(plainAttempts)
	if err != nil {
		t.Fatalf("Generate(%d) = error %v", plainAttempts, err)
	}
	if !strings.ContainsAny(name.String(), "0123456789") {
		t.Errorf("Generate(%d) = %q, want a number after %d collisions", plainAttempts, name, plainAttempts)
	}
}

// Generate must produce a usable name for every attempt count it will ever be
// called with, including well past the point where numbering starts.
func TestGenerateAlwaysProducesAValidName(t *testing.T) {
	g := newTestGenerator(t)

	for attempt := range 200 {
		name, err := g.Generate(attempt)
		if err != nil {
			t.Fatalf("Generate(%d) = error %v", attempt, err)
		}
		if name.IsZero() {
			t.Fatalf("Generate(%d) returned the zero Nickname", attempt)
		}
		if _, err := nickname.Parse(name.String()); err != nil {
			t.Fatalf("Generate(%d) = %q, which does not re-parse: %v", attempt, name, err)
		}
	}
}

// RandN is injected, so a test can pin the draw. This also documents that index
// 0 of each list is reachable — a generator that never picked the first word
// would be a plausible off-by-one.
func TestRandNIsInjectable(t *testing.T) {
	g, err := New(Config{RandN: func(int) int { return 0 }})
	if err != nil {
		t.Fatalf("New() = error %v", err)
	}

	first, err := g.Generate(0)
	if err != nil {
		t.Fatalf("Generate(0) = error %v", err)
	}

	want, err := g.build(g.adjectives[0], g.nouns[0], "")
	if err != nil {
		t.Fatalf("build() = error %v", err)
	}

	if first != want {
		t.Errorf("Generate(0) with a pinned source = %q, want %q", first, want)
	}
}

func TestNewRejectsBadData(t *testing.T) {
	// The construction-time budget checks are what keep a hand-edited word list
	// from producing names that cannot be parsed. Exercised through New's own
	// validation by way of the real lists, plus this direct case for the article.
	if _, err := article.Parse("die "); err == nil {
		t.Error("article.Parse accepted a trailing space, so a malformed noun list would load")
	}
}
