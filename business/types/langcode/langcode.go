// Package langcode is the strong type for the language a deck teaches, held as a
// lowercase ISO 639-1 two-letter code (for example "de" for German).
//
// The app ships with one language today, but the request that started it was
// explicit that more come later. Making the language a validated type from the
// first commit means the deck, the study records, and the API all already carry
// it — adding French is new data under an existing key, not a schema migration
// that threads a new column through every layer.
package langcode

import "errors"

// ErrInvalidLangCode is returned by Parse when the input is not two ASCII
// lowercase letters.
var ErrInvalidLangCode = errors.New("langcode: must be two lowercase ASCII letters")

// LangCode is a validated ISO 639-1 language code. The zero value is invalid.
type LangCode struct {
	value string
}

// German is the language this module's deck teaches, offered as a ready value so
// seed data and wiring need not parse a literal.
var German = LangCode{value: "de"}

// Parse validates s as two lowercase ASCII letters. It does not check the code
// against the full ISO register — the point is a cheap shape guarantee, not a
// membership test — but it rejects the shapes that would corrupt a URL path or a
// storage key.
func Parse(s string) (LangCode, error) {
	if len(s) != 2 {
		return LangCode{}, ErrInvalidLangCode
	}

	for _, r := range s {
		if r < 'a' || r > 'z' {
			return LangCode{}, ErrInvalidLangCode
		}
	}

	return LangCode{value: s}, nil
}

// MustParse parses s and panics on failure; for tests and known-good constants.
func MustParse(s string) LangCode {
	lc, err := Parse(s)
	if err != nil {
		panic(err)
	}

	return lc
}

// String returns the two-letter code, or "" for the zero value.
func (lc LangCode) String() string { return lc.value }

// IsZero reports whether lc is the unset zero value.
func (lc LangCode) IsZero() bool { return lc == LangCode{} }
