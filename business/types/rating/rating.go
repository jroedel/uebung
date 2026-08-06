// Package rating is the strong type for how one review went, on the four-point
// scale a learner grades themselves with: again, hard, good, easy.
//
// It mirrors the scheduler's grading scale but is defined independently and does
// not import the scheduler. That is deliberate: the scheduler (foundation/fsrs)
// is an implementation detail the Business layer maps onto at the point it calls
// it, and per the layering rules a foundation type must not surface inside a
// business/types value. The study domain converts a Rating into the scheduler's
// own rating at that boundary. Keeping the two apart means swapping schedulers,
// or grading against something other than FSRS later, touches one converter
// rather than this vocabulary.
package rating

import "errors"

// ErrInvalidRating is returned by Parse for anything outside the four grades.
var ErrInvalidRating = errors.New("rating: must be one of again, hard, good, easy")

// Rating is a validated self-graded review outcome. The zero value is invalid so
// an unset grade cannot be mistaken for "again".
type Rating uint8

const (
	invalid Rating = iota // 0: no grade supplied.
	Again                 // failed to recall the gender.
	Hard                  // recalled, but with real hesitation.
	Good                  // recalled correctly, normal effort.
	Easy                  // recalled instantly.
)

// names is the canonical wire/string spelling of each grade, indexed by value.
var names = [...]string{
	Again: "again",
	Hard:  "hard",
	Good:  "good",
	Easy:  "easy",
}

// Parse validates s as one of the four grade names.
func Parse(s string) (Rating, error) {
	switch s {
	case "again":
		return Again, nil
	case "hard":
		return Hard, nil
	case "good":
		return Good, nil
	case "easy":
		return Easy, nil
	default:
		return invalid, ErrInvalidRating
	}
}

// MustParse parses s and panics on failure; for tests and known-good constants.
func MustParse(s string) Rating {
	r, err := Parse(s)
	if err != nil {
		panic(err)
	}

	return r
}

// Valid reports whether r is one of the four defined grades.
func (r Rating) Valid() bool { return r >= Again && r <= Easy }

// String returns the grade's canonical name, or "" for the invalid zero value.
func (r Rating) String() string {
	if !r.Valid() {
		return ""
	}

	return names[r]
}
