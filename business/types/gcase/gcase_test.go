package gcase

import (
	"errors"
	"testing"
)

func TestParseAcceptsTheThreeGovernedCases(t *testing.T) {
	cases := map[string]Case{"akkusativ": Akkusativ, "dativ": Dativ, "genitiv": Genitiv}
	for in, want := range cases {
		got, err := Parse(in)
		if err != nil {
			t.Fatalf("Parse(%q) errored: %v", in, err)
		}
		if got != want {
			t.Fatalf("Parse(%q) = %v, want %v", in, got, want)
		}
		if got.String() != in {
			t.Fatalf("String() = %q, want %q", got.String(), in)
		}
	}
}

func TestParseRejectsEverythingElse(t *testing.T) {
	// "nominativ" is in this list on purpose: it is a real German case and the one
	// a reader would most plausibly add, but nothing governs it, so a card could
	// never carry it as an answer. Accepting it would put a fourth button on a
	// three-way drill.
	for _, in := range []string{"", "nominativ", "Dativ", "AKKUSATIV", " dativ", "dativ ", "akk", "dat", "gen", "accusative"} {
		if _, err := Parse(in); !errors.Is(err, ErrInvalidCase) {
			t.Fatalf("Parse(%q) err = %v, want ErrInvalidCase", in, err)
		}
	}
}

func TestZeroValueIsInvalidAndDistinct(t *testing.T) {
	var c Case
	if !c.IsZero() {
		t.Fatal("zero Case should report IsZero")
	}
	if c == Akkusativ || c == Dativ || c == Genitiv {
		t.Fatal("zero Case must not equal any real case")
	}
	if c.String() != "" {
		t.Fatalf("zero String() = %q, want empty", c.String())
	}
}

func TestMustParsePanicsOnBadInput(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal(`MustParse("nope") should panic`)
		}
	}()
	MustParse("nope")
}
