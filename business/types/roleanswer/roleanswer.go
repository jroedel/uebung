// Package roleanswer is the strong type for how a learner did on the *first*
// half of a two-part card: naming the participant role before producing the form.
//
// It exists because of a diagnostic that the deck design depends on. A learner
// who writes "dem Mann" where "den Mann" belonged has made one of two completely
// different mistakes: they either misread who was doing what to whom (a concept
// failure) or they knew the role perfectly and fetched the wrong ending (a form
// failure). Those need different help, and a single right/wrong on the card
// cannot tell them apart — so the role answer is recorded next to the grade.
//
// Only the role-then-form decks ask the question at all. Every other deck records
// None, which is why None is the zero value: a card that was never asked about
// roles should read as "not applicable", not as a silent failure. Like cardstate,
// the String form is what persistence writes to a text column, so a review row
// says "incorrect" rather than an integer whose meaning depends on iota order.
package roleanswer

import "errors"

// ErrInvalidRoleAnswer is returned by Parse for an unrecognised name.
var ErrInvalidRoleAnswer = errors.New("roleanswer: must be one of none, correct, incorrect")

// RoleAnswer is how the role half of a card went. The zero value is None, meaning
// the card never posed the question.
type RoleAnswer uint8

const (
	None      RoleAnswer = iota // the deck does not ask for a role.
	Correct                     // the learner named the role correctly.
	Incorrect                   // the learner named the wrong role.
)

var names = [...]string{
	None:      "none",
	Correct:   "correct",
	Incorrect: "incorrect",
}

// Parse validates s as one of the three names.
func Parse(s string) (RoleAnswer, error) {
	switch s {
	case "none":
		return None, nil
	case "correct":
		return Correct, nil
	case "incorrect":
		return Incorrect, nil
	default:
		return None, ErrInvalidRoleAnswer
	}
}

// MustParse parses s and panics on failure; for tests and known-good constants.
func MustParse(s string) RoleAnswer {
	ra, err := Parse(s)
	if err != nil {
		panic(err)
	}

	return ra
}

// Valid reports whether ra is one of the three defined answers.
func (ra RoleAnswer) Valid() bool { return ra <= Incorrect }

// String returns the answer's canonical name.
func (ra RoleAnswer) String() string {
	if !ra.Valid() {
		return ""
	}

	return names[ra]
}
