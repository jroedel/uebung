// Package deckid is the strong type for which deck a study record belongs to,
// held as a lowercase slug (for example "der-die-das").
//
// A deck is the unit a learner actually picks up: a named set of items drilled
// one way, with its own introduction and its own unlock rule. Until now there was
// exactly one, so the scheduler could key a card by (user, language, lemma) and be
// right. A second deck makes that key ambiguous — "mit" is a preposition in one
// deck and could be a noun row in another — so the deck joins the key.
//
// Deliberately a slug rather than an integer id. A deck id is written into a
// storage key, appears in a URL query, and is read in a sqlite3 shell during
// support; "der-die-das" answers what a row is and "7" does not. The validation
// exists because of those three uses: anything that would need escaping in a URL,
// or that would make two ids look alike in a listing, is rejected at the door.
package deckid

import "errors"

// ErrInvalidDeckID is returned by Parse for anything that is not a slug: ASCII
// lowercase letters, digits and single inner hyphens, 1 to 64 bytes.
var ErrInvalidDeckID = errors.New("deckid: must be 1-64 chars of lowercase ASCII letters, digits and single inner hyphens")

// maxLen bounds the slug so a deck id cannot bloat a storage key. Sixty-four is
// far above any readable deck name and far below anything that would matter.
const maxLen = 64

// DeckID is a validated deck identifier. The zero value is invalid, so a struct
// that forgot to set its deck fails a check rather than silently landing in some
// default deck and mixing two learners' material together.
type DeckID struct {
	value string
}

// DerDieDas is the German gender deck — the original module, and the deck every
// pre-existing study record is backfilled to. Offered as a ready value so wiring
// and migrations need not parse a literal.
var DerDieDas = DeckID{value: "der-die-das"}

// Parse validates s as a deck slug. Leading, trailing and doubled hyphens are
// rejected as well as the obvious bad bytes: they are the difference between two
// ids a person reading a list would take for the same deck.
func Parse(s string) (DeckID, error) {
	if len(s) == 0 || len(s) > maxLen {
		return DeckID{}, ErrInvalidDeckID
	}

	for i := range len(s) {
		c := s[i]

		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			continue
		case c != '-':
			return DeckID{}, ErrInvalidDeckID
		}

		// A hyphen, and it may not lead, trail, or follow another hyphen.
		if i == 0 || i == len(s)-1 || s[i-1] == '-' {
			return DeckID{}, ErrInvalidDeckID
		}
	}

	return DeckID{value: s}, nil
}

// MustParse parses s and panics on failure; for tests and known-good constants.
func MustParse(s string) DeckID {
	d, err := Parse(s)
	if err != nil {
		panic(err)
	}

	return d
}

// String returns the slug, or "" for the zero value.
func (d DeckID) String() string { return d.value }

// IsZero reports whether d is the unset zero value.
func (d DeckID) IsZero() bool { return d == DeckID{} }
