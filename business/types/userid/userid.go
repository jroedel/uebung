// Package userid is the strong type identifying one learner.
//
// The app runs single-user today, but the design brief called for it to be
// multi-user-ready. Rather than hard-code "the current user" as an implicit
// global, every study record is keyed by a UserID from the first commit. Today a
// single Local() value fills that key; when accounts arrive, the only change is
// where the UserID comes from (a session) — the storage schema, the business
// signatures, and the API already speak in user identity.
package userid

import "errors"

// ErrInvalidUserID is returned by Parse when the input is empty or too long.
var ErrInvalidUserID = errors.New("userid: must be 1-64 non-empty characters")

const maxLen = 64

// UserID identifies a learner. The zero value is invalid so an unkeyed record is
// impossible to write by accident.
type UserID struct {
	value string
}

// local is the single learner used until real accounts exist. It is a stable,
// known-good constant, so MustParse is appropriate.
var local = MustParse("local")

// Local returns the built-in single-user identity. Call sites use this instead of
// spelling "local" so the eventual switch to session-derived users is a change in
// one place.
func Local() UserID { return local }

// Parse validates s as a user identifier: 1..64 bytes, non-empty. It is
// deliberately permissive about character set because the eventual source is an
// account system whose id format is not this package's to dictate; it only
// guarantees the key is present and bounded.
func Parse(s string) (UserID, error) {
	if len(s) < 1 || len(s) > maxLen {
		return UserID{}, ErrInvalidUserID
	}

	return UserID{value: s}, nil
}

// MustParse parses s and panics on failure; for tests and known-good constants.
func MustParse(s string) UserID {
	id, err := Parse(s)
	if err != nil {
		panic(err)
	}

	return id
}

// String returns the identifier, or "" for the zero value.
func (id UserID) String() string { return id.value }

// IsZero reports whether id is the unset zero value.
func (id UserID) IsZero() bool { return id == UserID{} }
