package drillkind_test

import (
	"errors"
	"testing"

	"github.com/jroedel/uebung/business/types/drillkind"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want drillkind.DrillKind
		ok   bool
	}{
		{"article", "article-3way", drillkind.ArticleThreeWay, true},
		{"case", "case-3way", drillkind.CaseThreeWay, true},
		{"empty", "", drillkind.DrillKind{}, false},
		{"unknown", "typing", drillkind.DrillKind{}, false},
		{"wrong case", "Article-3Way", drillkind.DrillKind{}, false},
		{"padded", " case-3way", drillkind.DrillKind{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := drillkind.Parse(tt.in)

			switch {
			case tt.ok && err != nil:
				t.Fatalf("Parse(%q): %v", tt.in, err)
			case !tt.ok && !errors.Is(err, drillkind.ErrInvalidDrillKind):
				t.Fatalf("Parse(%q) error = %v, want ErrInvalidDrillKind", tt.in, err)
			}

			if got != tt.want {
				t.Errorf("Parse(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// The zero value has to be unusable, because a deck file that omits its drill
// parses into one. Failing there is the whole point of not putting a real drill
// at zero.
func TestZeroValueIsUnusable(t *testing.T) {
	var d drillkind.DrillKind

	if !d.IsZero() {
		t.Error("the zero value does not report IsZero")
	}
	if d.String() != "" {
		t.Errorf("the zero value stringifies to %q, want empty", d.String())
	}
	if d == drillkind.ArticleThreeWay || d == drillkind.CaseThreeWay {
		t.Error("the zero value equals a real drill")
	}
}

// The slugs are written into deck files and read by the browser client, so they
// are part of two contracts and cannot be renamed casually.
func TestSlugsAreStable(t *testing.T) {
	if got := drillkind.ArticleThreeWay.String(); got != "article-3way" {
		t.Errorf("ArticleThreeWay = %q, want %q", got, "article-3way")
	}
	if got := drillkind.CaseThreeWay.String(); got != "case-3way" {
		t.Errorf("CaseThreeWay = %q, want %q", got, "case-3way")
	}
}
