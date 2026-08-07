// Package email is the strong type for a learner's email address.
//
// Email is identity here: there are no passwords, so the address is both the
// account name and the only channel by which anyone proves they own it. That
// makes normalisation a correctness concern rather than a nicety — if
// "John@Example.com" and "john@example.com" can both exist, one person ends up
// with two decks and a magic link that signs them into the wrong one.
package email

import (
	"errors"
	"strings"
	"unicode"
)

// ErrInvalidEmail is returned by Parse for anything that is not a plausible
// single address.
var ErrInvalidEmail = errors.New("email: must be a single address of the form name@domain")

// maxLen is the practical limit from RFC 5321 for a whole address.
const maxLen = 254

// Email is a validated, normalised address. The zero value is invalid, so an
// account cannot be written without one.
type Email struct {
	value string
}

// Parse validates and normalises s.
//
// Validation is deliberately shallow: it checks the shape rather than trying to
// decide deliverability, because the only real proof an address exists is that
// someone followed a link sent to it — which this whole flow is built around.
// Rejecting exotic-but-legal addresses would lock people out for no security
// gain, so this rejects only what cannot be an address at all.
//
// Normalisation lowercases the whole address. The local part is technically
// case-sensitive per RFC 5321, but no mail provider in practice treats it that
// way, and honouring the RFC here would let one person hold several accounts.
func Parse(s string) (Email, error) {
	addr := strings.TrimSpace(s)

	if len(addr) == 0 || len(addr) > maxLen {
		return Email{}, ErrInvalidEmail
	}

	// No whitespace or control characters anywhere: these are what turn an
	// address into a header-injection attempt once it reaches a mail library.
	for _, r := range addr {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return Email{}, ErrInvalidEmail
		}
	}

	// Exactly one @, with something either side. LastIndex plus a Contains check
	// on the remainder rejects "a@b@c" without a full parser.
	local, domain, found := strings.Cut(addr, "@")
	if !found || local == "" || domain == "" || strings.Contains(domain, "@") {
		return Email{}, ErrInvalidEmail
	}

	// A domain needs a dot and cannot start or end with one; this rejects
	// "user@localhost" and "user@.com", the two shapes that most often reach a
	// signup form by accident.
	if !strings.Contains(domain, ".") || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return Email{}, ErrInvalidEmail
	}

	return Email{value: strings.ToLower(addr)}, nil
}

// MustParse parses s and panics on failure; for tests and known-good constants.
func MustParse(s string) Email {
	e, err := Parse(s)
	if err != nil {
		panic(err)
	}

	return e
}

// String returns the normalised address, or "" for the zero value.
func (e Email) String() string { return e.value }

// IsZero reports whether e is the unset zero value.
func (e Email) IsZero() bool { return e == Email{} }

// Domain returns the part after the @, for logging and rate-limiting decisions
// that should not record the whole address.
func (e Email) Domain() string {
	_, domain, _ := strings.Cut(e.value, "@")

	return domain
}
