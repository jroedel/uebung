package nickname_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/jroedel/uebung/business/types/nickname"
)

// Characters that look like ASCII but are not are written as escapes throughout,
// because the whole point of the cases using them is that they are invisible.

func TestParseNormalises(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"trims both ends", "  Blaue Eule  ", "Blaue Eule"},
		{"collapses internal runs", "Blaue    Eule", "Blaue Eule"},
		{"collapses tabs and newlines", "Blaue\t\nEule", "Blaue Eule"},
		{"turns a non-breaking space into an ordinary one", "Blaue Eule", "Blaue Eule"},
		{"leaves a clean name alone", "Dunkler Hund", "Dunkler Hund"},
		{"keeps hyphens", "Blau-Eule", "Blau-Eule"},
		{"keeps umlauts and eszett", "Grüße Möwe", "Grüße Möwe"},
		{"keeps digits inside a name", "Eule 88", "Eule 88"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := nickname.Parse(tt.in)
			if err != nil {
				t.Fatalf("Parse(%q) = error %v, want it to parse", tt.in, err)
			}
			if got.String() != tt.want {
				t.Errorf("Parse(%q) = %q, want %q", tt.in, got.String(), tt.want)
			}
		})
	}
}

func TestParseRejects(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want error
	}{
		{"empty", "", nickname.ErrInvalidNickname},
		{"whitespace only", "   ", nickname.ErrInvalidNickname},
		{"too short", "Ei", nickname.ErrInvalidNickname},
		{"too long", strings.Repeat("a", nickname.MaxLen+1), nickname.ErrInvalidNickname},
		{"leading hyphen", "-Eule", nickname.ErrInvalidNickname},
		{"trailing hyphen", "Eule-", nickname.ErrInvalidNickname},
		{"double hyphen", "Blaue--Eule", nickname.ErrInvalidNickname},
		{"space then hyphen", "Blaue -Eule", nickname.ErrInvalidNickname},
		{"digits only", "12345", nickname.ErrInvalidNickname},
		{"underscore", "Blaue_Eule", nickname.ErrInvalidNickname},
		{"at sign", "eule@example.com", nickname.ErrInvalidNickname},
		{"emoji", "Blaue Eule \U0001f989", nickname.ErrInvalidNickname},

		// The homoglyph cases are the reason the character set is an allowlist.
		// Each of these renders indistinguishably from an ASCII name in most
		// fonts, which on a public leaderboard is impersonation.
		{"cyrillic e", "Jеff", nickname.ErrInvalidNickname},
		{"greek capital eta", "Ηund", nickname.ErrInvalidNickname},
		{"fullwidth latin", "Ｅule", nickname.ErrInvalidNickname},
		{"zero-width joiner", "Blaue‍Eule", nickname.ErrInvalidNickname},
		{"right-to-left override", "Blaue‮Eule", nickname.ErrInvalidNickname},
		{"combining acute", "Éule", nickname.ErrInvalidNickname},

		{"reserved whole name", "Admin", nickname.ErrReservedNickname},
		{"reserved with separators", "A-D-M-I-N", nickname.ErrReservedNickname},
		{"reserved as one word of several", "Admin Helper", nickname.ErrReservedNickname},
		{"reserved site name", "Übung", nickname.ErrReservedNickname},
		{"reserved transliterated site name", "uebung", nickname.ErrReservedNickname},

		{"profanity", "Scheisse", nickname.ErrProfaneNickname},
		{"profanity via eszett", "Scheiße", nickname.ErrProfaneNickname},
		{"profanity spaced out", "f u c k", nickname.ErrProfaneNickname},
		{"profanity as one word of several", "Blaue Nazi Eule", nickname.ErrProfaneNickname},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := nickname.Parse(tt.in)
			if !errors.Is(err, tt.want) {
				t.Errorf("Parse(%q) = error %v, want %v", tt.in, err, tt.want)
			}
		})
	}
}

// The blocklist is only worth having if it does not also reject ordinary German.
// Every name here contains a substring of some blocked entry and must survive,
// which is what whole-word matching buys.
func TestParseAcceptsInnocentNames(t *testing.T) {
	names := []string{
		"Blaues Ass",   // "das Ass" is German for ace; "ass" is not blocked.
		"Grünes Kraut", // a herb, not the English slur.
		"Assistent",    // contains "ass".
		"Klasse Katze", // contains "ass".
		"Massimo",      // contains "ass".
		"Scunthorpe",   // the canonical false positive.
		"Eule 88",      // a birth year, not a code.
	}

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			if _, err := nickname.Parse(name); err != nil {
				t.Errorf("Parse(%q) = error %v, want it to parse", name, err)
			}
		})
	}
}

// The counter-example to the test above: an impersonation attempt dressed up as
// an ordinary two-word name still has to fail.
func TestParseRejectsReservedInsideAName(t *testing.T) {
	if _, err := nickname.Parse("Administrator Hut"); !errors.Is(err, nickname.ErrReservedNickname) {
		t.Errorf(`Parse("Administrator Hut") = error %v, want %v`, err, nickname.ErrReservedNickname)
	}
}

func TestFoldCollides(t *testing.T) {
	// Names that must be treated as one, because a reader scanning a leaderboard
	// column could not tell them apart.
	groups := [][]string{
		{"Blaue Eule", "blaue eule", "BLAUE EULE", "blaue-eule", "BlaueEule", "  Blaue   Eule "},
		{"Grüße", "GRÜSSE", "grüsse"},
		{"Weiße Möwe", "weisse möwe", "Weisse-Möwe"},
	}

	for _, group := range groups {
		t.Run(group[0], func(t *testing.T) {
			want := nickname.MustParse(group[0]).Fold()
			for _, variant := range group[1:] {
				got, err := nickname.Parse(variant)
				if err != nil {
					t.Fatalf("Parse(%q) = error %v", variant, err)
				}
				if got.Fold() != want {
					t.Errorf("Parse(%q).Fold() = %q, want %q", variant, got.Fold(), want)
				}
			}
		})
	}
}

// Umlauts must NOT fold to their base vowels: "Grüne" and "Grune" are different
// German words, and collapsing them would hand out a false collision every time
// two unrelated people picked ordinary names.
func TestFoldKeepsUmlautsDistinct(t *testing.T) {
	pairs := [][2]string{
		{"Grüne Eule", "Grune Eule"},
		{"Möwe Nord", "Mowe Nord"},
		{"Bär Blau", "Bar Blau"},
	}

	for _, pair := range pairs {
		t.Run(pair[0], func(t *testing.T) {
			a := nickname.MustParse(pair[0])
			b := nickname.MustParse(pair[1])
			if a.Fold() == b.Fold() {
				t.Errorf("Fold(%q) == Fold(%q) = %q, want them distinct", pair[0], pair[1], a.Fold())
			}
		})
	}
}

func TestZeroValue(t *testing.T) {
	var n nickname.Nickname

	if !n.IsZero() {
		t.Error("the zero Nickname reports IsZero() = false")
	}
	if n.String() != "" {
		t.Errorf("the zero Nickname stringifies to %q, want the empty string", n.String())
	}

	if nickname.MustParse("Blaue Eule").IsZero() {
		t.Error("a parsed Nickname reports IsZero() = true")
	}
}

// Length is counted in runes, so an umlaut costs one character rather than the
// two bytes it occupies. A German-learning app that gave "Grüße" less room than
// "Gruss" would have it exactly backwards.
func TestLengthCountsRunesNotBytes(t *testing.T) {
	long := strings.Repeat("ü", nickname.MaxLen-1) + "a" // 24 runes, 47 bytes.

	if len(long) <= nickname.MaxLen {
		t.Fatalf("test input is %d bytes, expected it to exceed %d so the case is meaningful", len(long), nickname.MaxLen)
	}
	if _, err := nickname.Parse(long); err != nil {
		t.Errorf("Parse(%q) = error %v, want a %d-rune name to be accepted", long, err, nickname.MaxLen)
	}
}
