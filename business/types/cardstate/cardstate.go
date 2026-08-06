// Package cardstate is the strong type for where a card sits in its review
// lifecycle: new, learning, review, or relearning.
//
// Like rating, it mirrors the scheduler's own lifecycle enum without importing
// it, so the scheduler's type never leaks into a stored Business model. Its
// String form is what persistence writes to a text column, and Parse is what
// reads it back — a card row stores "review", not an opaque integer whose
// meaning depends on the scheduler package's iota order.
package cardstate

import "errors"

// ErrInvalidCardState is returned by Parse for an unrecognised state name.
var ErrInvalidCardState = errors.New("cardstate: must be one of new, learning, review, relearning")

// CardState is a validated lifecycle position. The zero value is New, which is
// the correct default for a card nobody has reviewed yet — unlike the other
// strong types here, a meaningful zero is wanted, so New sits at zero.
type CardState uint8

const (
	New        CardState = iota // never reviewed.
	Learning                    // in the initial short-term steps.
	Review                      // graduated to long-term scheduling.
	Relearning                  // lapsed and being rebuilt.
)

var names = [...]string{
	New:        "new",
	Learning:   "learning",
	Review:     "review",
	Relearning: "relearning",
}

// Parse validates s as one of the four state names.
func Parse(s string) (CardState, error) {
	switch s {
	case "new":
		return New, nil
	case "learning":
		return Learning, nil
	case "review":
		return Review, nil
	case "relearning":
		return Relearning, nil
	default:
		return New, ErrInvalidCardState
	}
}

// MustParse parses s and panics on failure; for tests and known-good constants.
func MustParse(s string) CardState {
	cs, err := Parse(s)
	if err != nil {
		panic(err)
	}

	return cs
}

// Valid reports whether cs is one of the four defined states.
func (cs CardState) Valid() bool { return cs <= Relearning }

// String returns the state's canonical name.
func (cs CardState) String() string {
	if !cs.Valid() {
		return ""
	}

	return names[cs]
}
