// Package batchid is the strong type for the identity of one flush of graded
// cards, chosen by the client when a batch starts.
//
// It exists so a flush can be sent more than once. A phone that loses signal
// between sending a flush and reading the answer cannot know whether the server
// applied it, and the only way to keep the round is to send it again. Grading the
// same answers twice would review every card a second time at the same instant —
// a schedule nobody earned — so the server remembers which batches it has applied
// and recognises the retry.
//
// The client mints it (crypto.randomUUID() in the browser), so the server only
// constrains its shape: short, and made of bytes that are safe in a storage key
// and readable in a sqlite3 shell during support.
package batchid

import "errors"

// ErrInvalidBatchID is returned by Parse for anything outside 1 to 64 ASCII
// letters, digits, hyphens and underscores.
var ErrInvalidBatchID = errors.New("batchid: must be 1-64 chars of ASCII letters, digits, '-' and '_'")

// maxLen bounds the id so it cannot bloat a storage key. A UUID is 36.
const maxLen = 64

// BatchID is a validated flush identifier. The zero value means "not supplied":
// a client from before this field existed sends none, and its flush is applied
// without being remembered.
type BatchID struct {
	value string
}

// Parse validates s as a batch id.
func Parse(s string) (BatchID, error) {
	if len(s) == 0 || len(s) > maxLen {
		return BatchID{}, ErrInvalidBatchID
	}

	for i := range len(s) {
		switch c := s[i]; {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
			continue
		default:
			return BatchID{}, ErrInvalidBatchID
		}
	}

	return BatchID{value: s}, nil
}

// MustParse parses s and panics on failure; for tests and known-good constants.
func MustParse(s string) BatchID {
	b, err := Parse(s)
	if err != nil {
		panic(err)
	}

	return b
}

// String returns the id, or "" for the zero value.
func (b BatchID) String() string { return b.value }

// IsZero reports whether b is the unset zero value.
func (b BatchID) IsZero() bool { return b == BatchID{} }
