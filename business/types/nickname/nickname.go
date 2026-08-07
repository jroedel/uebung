// Package nickname is the strong type for a learner's public display name.
//
// A nickname is the one field on an account that other people will see: it is
// what a leaderboard prints next to a score. That is what drives every rule
// here, and it is worth being explicit about the difference from
// business/types/email — an address is private and only ever has to be
// consistent with itself, whereas a nickname sits in a public list beside other
// people's, so it also has to be hard to confuse with theirs.
//
// Three consequences follow:
//
//   - The character set is a tight allowlist rather than "any letter". A
//     leaderboard is exactly the setting where a homoglyph pays off: Cyrillic
//     U+0430 renders identically to Latin "a" in most fonts, so "Jeff" and
//     "Jeff" can sit two rows apart and no reader can tell which is which.
//     Allowing only ASCII letters, German umlauts, digits and two separators
//     makes that impossible by construction instead of by a confusables table
//     that has to be kept current.
//   - Uniqueness is decided on a folded form, not the typed one. Case and
//     separators carry no meaning to a reader glancing down a column, so
//     "Blaue Eule", "blaue-eule" and "BlaueEule" are one name here.
//   - Names are screened. See blocklist.go for what and why.
//
// Parse is the only constructor. Every rule above is enforced there, so a
// Nickname value that exists at all is one that has passed all of them.
package nickname

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// The errors are separate because the App layer can say something useful about
// each, and because they are not equally the user's fault: a too-long name is a
// typo to fix, a taken-looking reserved word is a rule they could not have
// known, and a screened name is a decision we are not going to explain in
// detail.
var (
	// ErrInvalidNickname covers length and character-set failures.
	ErrInvalidNickname = errors.New("nickname: must be 3-24 characters of letters, digits, spaces or hyphens")

	// ErrReservedNickname is returned for names that would let someone pass
	// themselves off as part of the site.
	ErrReservedNickname = errors.New("nickname: that name is reserved")

	// ErrProfaneNickname is returned for names caught by the blocklist.
	ErrProfaneNickname = errors.New("nickname: please choose a different name")
)

// Length bounds, counted in runes rather than bytes. Every umlaut is two bytes
// in UTF-8, so a byte limit would silently give a learner writing "Grüße" less
// room than one writing "Gruss" — for a German-learning app that is precisely
// the wrong way round.
//
// MaxLen is also the budget the generated-name word lists are curated against,
// so every pair nicknamer can produce is a name a person could have typed.
const (
	MinLen = 3
	MaxLen = 24
)

// Nickname is a validated, whitespace-normalised display name. The zero value is
// invalid, so an account cannot be given a nickname without one being parsed.
//
// Only the display form is stored. The folded form is derived on demand by
// Fold rather than held alongside, so there is no way for the two to drift apart
// and no second field to keep in sync.
type Nickname struct {
	value string
}

// Parse normalises and validates s.
//
// Normalisation happens first and is deliberately forgiving: leading and
// trailing whitespace is trimmed and internal runs are collapsed to one space,
// because "  Blaue   Eule " is not a person trying anything, it is a person who
// pasted from somewhere. Validation after that is strict, because everything it
// rejects is either unreadable in a list or actively deceptive in one.
func Parse(s string) (Nickname, error) {
	name := normalizeSpace(s)

	if n := utf8.RuneCountInString(name); n < MinLen || n > MaxLen {
		return Nickname{}, ErrInvalidNickname
	}

	if err := checkRunes(name); err != nil {
		return Nickname{}, err
	}

	// Screening runs on the parsed name rather than at the App edge so that it
	// cannot be skipped by a future caller who constructs a nickname some other
	// way. There is no other way in.
	if err := screen(name); err != nil {
		return Nickname{}, err
	}

	return Nickname{value: name}, nil
}

// MustParse parses s and panics on failure; for tests and known-good constants.
func MustParse(s string) Nickname {
	n, err := Parse(s)
	if err != nil {
		panic(err)
	}

	return n
}

// String returns the display form, or "" for the zero value.
func (n Nickname) String() string { return n.value }

// IsZero reports whether n is the unset zero value. A learner who has not chosen
// or accepted a name yet holds this.
func (n Nickname) IsZero() bool { return n == Nickname{} }

// Fold returns the uniqueness key: lowercased, "ß" expanded to "ss", separators
// removed.
//
// This is what a unique index should be built on, not String. Two names that
// fold together are two names a reader cannot tell apart in a list, which for a
// public leaderboard is the definition of a collision — the point is not to
// deduplicate storage but to stop one person shadowing another.
//
// Umlauts are deliberately NOT folded to their base vowels. "Grüne" and "Grune"
// are different words to anyone reading German, and collapsing them would hand
// out a false collision every time two unrelated people picked ordinary German
// names. "ß" is different in kind: German itself treats "ß" and "ss" as
// interchangeable spellings of one word, so leaving them apart would be the
// false negative.
func (n Nickname) Fold() string {
	folded := strings.ToLower(n.value)
	folded = strings.ReplaceAll(folded, "ß", "ss")

	var b strings.Builder
	b.Grow(len(folded))
	for _, r := range folded {
		if !isSeparator(r) {
			b.WriteRune(r)
		}
	}

	return b.String()
}

// normalizeSpace trims the ends and collapses internal whitespace runs to a
// single space. strings.Fields splits on every kind of Unicode space, which is
// what we want: a non-breaking space pasted in from a web page should become an
// ordinary one here rather than reaching checkRunes and being rejected as an
// unknown character.
func normalizeSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// checkRunes enforces the allowlist and the shape rules around separators.
//
// The shape rules exist because the characters alone are not enough: "-Eule",
// "Eule-" and "Blaue - Eule" are all inside the allowlist and all read as
// mistakes or as padding meant to sort a name to the top of a list.
func checkRunes(name string) error {
	prevSeparator := true // treat the start as a separator: a name cannot open with one.
	hasLetter := false

	for _, r := range name {
		switch {
		case isSeparator(r):
			// Two separators in a row is either a typo or an attempt at visual
			// padding. Note that runs of *whitespace* never reach here, having
			// been collapsed already; this catches "a--b" and "a -b".
			if prevSeparator {
				return ErrInvalidNickname
			}
			prevSeparator = true
		case isLetter(r):
			hasLetter = true
			prevSeparator = false
		case isDigit(r):
			prevSeparator = false
		default:
			return ErrInvalidNickname
		}
	}

	// A trailing separator fails for the same reason a leading one does.
	if prevSeparator {
		return ErrInvalidNickname
	}

	// At least one letter, so "123" and "4-5" are not names. Digits are allowed
	// only as part of one.
	if !hasLetter {
		return ErrInvalidNickname
	}

	return nil
}

// isLetter reports whether r is an allowed letter: ASCII plus the German
// umlauts and eszett, and nothing else. Written as an explicit set rather than
// unicode.IsLetter because the whole security argument in the package comment
// rests on this staying small.
func isLetter(r rune) bool {
	switch r {
	case 'ä', 'ö', 'ü', 'Ä', 'Ö', 'Ü', 'ß':
		return true
	}

	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func isDigit(r rune) bool { return r >= '0' && r <= '9' }

func isSeparator(r rune) bool { return r == ' ' || r == '-' }
