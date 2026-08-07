package email_test

import (
	"strings"
	"testing"

	"github.com/jroedel/uebung/business/types/email"
)

// Normalisation is a correctness property, not tidiness: the address is the
// account, so if two spellings can both exist, one person ends up with two decks
// and a sign-in link that lands on an arbitrary one.
func TestParseNormalises(t *testing.T) {
	tests := map[string]string{
		"learner@example.com":   "learner@example.com",
		"Learner@Example.COM":   "learner@example.com",
		"  spaced@example.com ": "spaced@example.com",
		"MiXeD@ExAmPlE.co.uk":   "mixed@example.co.uk",
	}

	for in, want := range tests {
		got, err := email.Parse(in)
		if err != nil {
			t.Errorf("Parse(%q): %v", in, err)

			continue
		}
		if got.String() != want {
			t.Errorf("Parse(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseRejects(t *testing.T) {
	tests := map[string]string{
		"empty":            "",
		"no at":            "learner.example.com",
		"no local":         "@example.com",
		"no domain":        "learner@",
		"two ats":          "a@b@example.com",
		"no dot in domain": "learner@localhost",
		"leading dot":      "learner@.example.com",
		"trailing dot":     "learner@example.com.",
		"too long":         strings.Repeat("a", 250) + "@example.com",
		// A newline in an address is how a header injection reaches the mail
		// library: "victim@x.com\nBcc: everyone".
		"newline":         "learner@example.com\nBcc: victim@example.com",
		"carriage return": "learner@example.com\rBcc: victim@example.com",
		"tab":             "learner\t@example.com",
		"space inside":    "learner name@example.com",
	}

	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			if got, err := email.Parse(in); err == nil {
				t.Fatalf("Parse(%q) = %q, want an error", in, got)
			}
		})
	}
}

// Domain is what gets logged, so that operational logs do not accumulate a list
// of everyone who uses the site.
func TestDomain(t *testing.T) {
	if got := email.MustParse("Learner@Example.com").Domain(); got != "example.com" {
		t.Fatalf("Domain = %q, want example.com", got)
	}
}

func TestZeroValueIsInvalid(t *testing.T) {
	var e email.Email
	if !e.IsZero() || e.String() != "" {
		t.Fatal("the zero Email should be zero and empty")
	}
}
