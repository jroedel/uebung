package batchid_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/jroedel/uebung/business/types/batchid"
)

func TestParseAcceptsWhatABrowserMints(t *testing.T) {
	for _, s := range []string{
		"3b241101-e2bb-4255-8caf-4136c566a962", // crypto.randomUUID()
		"A_b-9",
		strings.Repeat("x", 64),
	} {
		b, err := batchid.Parse(s)
		if err != nil {
			t.Errorf("Parse(%q): %v", s, err)
			continue
		}
		if b.String() != s || b.IsZero() {
			t.Errorf("Parse(%q) = %q (zero=%v)", s, b, b.IsZero())
		}
	}
}

func TestParseRejectsAnythingElse(t *testing.T) {
	for _, s := range []string{
		"",
		strings.Repeat("x", 65),
		"has space",
		"semi;colon",
		"new\nline",
		"ü",
	} {
		if _, err := batchid.Parse(s); !errors.Is(err, batchid.ErrInvalidBatchID) {
			t.Errorf("Parse(%q) err = %v, want ErrInvalidBatchID", s, err)
		}
	}
}

func TestZeroValueIsUnset(t *testing.T) {
	var b batchid.BatchID
	if !b.IsZero() || b.String() != "" {
		t.Fatalf("zero BatchID = %q, IsZero=%v", b, b.IsZero())
	}
}
