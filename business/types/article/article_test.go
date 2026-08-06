package article

import (
	"errors"
	"testing"
)

func TestParseAcceptsTheThreeArticles(t *testing.T) {
	cases := map[string]Article{"der": Der, "die": Die, "das": Das}
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
	for _, in := range []string{"", "Der", "DIE", " das", "das ", "den", "ein", "the", "d"} {
		if _, err := Parse(in); !errors.Is(err, ErrInvalidArticle) {
			t.Fatalf("Parse(%q) err = %v, want ErrInvalidArticle", in, err)
		}
	}
}

func TestZeroValueIsInvalidAndDistinct(t *testing.T) {
	var a Article
	if !a.IsZero() {
		t.Fatal("zero Article should report IsZero")
	}
	if a == Der || a == Die || a == Das {
		t.Fatal("zero Article must not equal any real article")
	}
	if a.String() != "" {
		t.Fatalf("zero String() = %q, want empty", a.String())
	}
}

func TestMustParsePanicsOnBadInput(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("MustParse(\"nope\") should panic")
		}
	}()
	MustParse("nope")
}
