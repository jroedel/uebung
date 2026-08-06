// Package article is the strong type for a German noun's grammatical gender,
// named by the definite article it takes: der (masculine), die (feminine), or
// das (neuter).
//
// Gender is the entire answer a learner gives in this module, so it earns a type
// of its own rather than living as a bare string. A string "der" can be
// mistyped, lower/upper-cased inconsistently, or confused with the plural "die";
// an Article cannot exist unless it parsed cleanly to one of exactly three
// values, and every comparison downstream is then a value comparison that can't
// silently disagree on spelling.
package article

import "errors"

// ErrInvalidArticle is returned by Parse when the input is not one of the three
// German definite articles in nominative singular.
var ErrInvalidArticle = errors.New("article: must be one of der, die, das")

// Article is a validated German definite article. The zero value is the invalid
// article and never compares equal to a parsed one, so a forgotten assignment
// surfaces instead of masquerading as "der".
type Article struct {
	value string
}

// The three genders, exported as ready-made values so package-level code and
// seed data can name them directly without parsing string literals.
var (
	Der = Article{value: "der"} // masculine
	Die = Article{value: "die"} // feminine
	Das = Article{value: "das"} // neuter
)

// Parse validates s as a German definite article. It accepts exactly "der",
// "die", or "das" — case-sensitive and untrimmed, because these values come from
// curated data and a validated API field, not from free typing, and silently
// accepting " Der\n" would let malformed data in under the guise of leniency.
func Parse(s string) (Article, error) {
	switch s {
	case "der":
		return Der, nil
	case "die":
		return Die, nil
	case "das":
		return Das, nil
	default:
		return Article{}, ErrInvalidArticle
	}
}

// MustParse parses s and panics on failure. It is for tests and package-level
// values built from known-good constants — never for request-derived data.
func MustParse(s string) Article {
	a, err := Parse(s)
	if err != nil {
		panic(err)
	}

	return a
}

// String returns the article's lowercase form ("der"/"die"/"das"), or the empty
// string for the zero value.
func (a Article) String() string { return a.value }

// IsZero reports whether a is the unset zero value.
func (a Article) IsZero() bool { return a == Article{} }
