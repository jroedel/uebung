// Package gcase is the strong type for the German grammatical case a word
// governs: akkusativ, dativ, or genitiv.
//
// It is the answer the trigger decks ask for, exactly as article.Article is the
// answer the noun deck asks for, and it earns a type for the same reasons: a case
// that exists only as a bare string can be misspelled, abbreviated ("akk", "dat"),
// or capitalised inconsistently between the seed data and the drill that reads it,
// and every one of those is a silent wrong answer rather than a loud parse error.
//
// Nominative is deliberately absent. These decks drill what a preposition or verb
// *governs*, and nothing governs the nominative — it is the case a subject is in,
// not one a trigger assigns. A fourth value would be an answer no card could ever
// have and a button no learner should ever be offered.
package gcase

import "errors"

// ErrInvalidCase is returned by Parse for anything that is not one of the three
// governed cases.
var ErrInvalidCase = errors.New("gcase: must be one of akkusativ, dativ, genitiv")

// Case is a validated German grammatical case. The zero value is invalid and
// never compares equal to a parsed one, so a forgotten assignment surfaces rather
// than masquerading as a real answer.
type Case struct {
	value string
}

// The three governed cases, exported so seed data and package-level code can name
// them without parsing string literals.
var (
	Akkusativ = Case{value: "akkusativ"}
	Dativ     = Case{value: "dativ"}
	Genitiv   = Case{value: "genitiv"}
)

// Parse validates s as a governed case. It accepts exactly the three lowercase
// full names — case-sensitive and untrimmed, matching article.Parse, because
// these values come from curated data and validated API fields rather than free
// typing, and accepting " Dativ\n" would admit malformed data as leniency.
func Parse(s string) (Case, error) {
	switch s {
	case "akkusativ":
		return Akkusativ, nil
	case "dativ":
		return Dativ, nil
	case "genitiv":
		return Genitiv, nil
	default:
		return Case{}, ErrInvalidCase
	}
}

// MustParse parses s and panics on failure. For tests and package-level values
// built from known-good constants — never for request-derived data.
func MustParse(s string) Case {
	c, err := Parse(s)
	if err != nil {
		panic(err)
	}

	return c
}

// String returns the case's lowercase name, or the empty string for the zero
// value.
func (c Case) String() string { return c.value }

// IsZero reports whether c is the unset zero value.
func (c Case) IsZero() bool { return c == Case{} }
