// Package drillkind is the strong type for how a deck asks its question — the
// shape of the interaction, not the material it is made of.
//
// A deck has two independent axes: what it teaches (prepositions, noun gender)
// and how it asks (three buttons, a typed answer, pick-the-role-then-the-form).
// Keeping the second one a named type means a client can decide whether it is
// able to render a deck at all, without knowing anything about that deck's
// subject. It is also what tells the App layer which converter a graded answer
// belongs to once more than one drill exists.
//
// Held as a slug for the same reasons deckid is: it travels in JSON to a browser
// and is read in a database shell, and "case-3way" answers what a row is where an
// integer does not.
package drillkind

import "errors"

// ErrInvalidDrillKind is returned by Parse for an unrecognised drill name.
var ErrInvalidDrillKind = errors.New("drillkind: must be one of article-3way, case-3way")

// DrillKind is a validated drill shape. The zero value is invalid: a deck that
// forgot to declare how it asks should fail to load rather than quietly become
// whichever drill happens to sit at zero.
type DrillKind struct {
	value string
}

var (
	// ArticleThreeWay is the original noun drill: one word, three buttons, one of
	// der/die/das.
	ArticleThreeWay = DrillKind{value: "article-3way"}

	// CaseThreeWay is the trigger drill: one preposition or verb, three buttons,
	// one of accusative/dative/genitive. Same shape as ArticleThreeWay and
	// deliberately so — a learner who can already work the noun deck needs no new
	// interaction to start the second one.
	CaseThreeWay = DrillKind{value: "case-3way"}
)

// Parse validates s as a known drill name.
func Parse(s string) (DrillKind, error) {
	switch s {
	case ArticleThreeWay.value:
		return ArticleThreeWay, nil
	case CaseThreeWay.value:
		return CaseThreeWay, nil
	default:
		return DrillKind{}, ErrInvalidDrillKind
	}
}

// MustParse parses s and panics on failure; for tests and known-good constants.
func MustParse(s string) DrillKind {
	d, err := Parse(s)
	if err != nil {
		panic(err)
	}

	return d
}

// String returns the drill's slug, or "" for the zero value.
func (d DrillKind) String() string { return d.value }

// IsZero reports whether d is the unset zero value.
func (d DrillKind) IsZero() bool { return d == DrillKind{} }
