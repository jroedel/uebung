// Package secret mints and checks the opaque random strings that stand in for a
// password in a passwordless system: magic-link tokens and session ids.
//
// Two rules drive everything here, and both exist because these strings ARE the
// credential — anyone holding one is the user.
//
//  1. They come from crypto/rand, never math/rand. A guessable login token is a
//     login.
//  2. Only their SHA-256 hash is ever stored. A database dump, a leaked backup or
//     an over-broad log then yields nothing that can be replayed, exactly as with
//     a password hash. Plain SHA-256 (not bcrypt/argon2) is right here precisely
//     because these are high-entropy random values, not human-chosen: there is no
//     dictionary to attack, so the slow-hash cost buys nothing.
//
// Comparison is constant-time, so a timing signal cannot be used to walk a guess
// toward a real token.
package secret

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
)

// entropyBytes is the size of a raw secret before encoding. 32 bytes is 256 bits
// — far beyond brute force, and it costs nothing to be generous with a value
// nobody has to type.
const entropyBytes = 32

// New returns a fresh URL-safe secret. The caller sends this to the user exactly
// once and stores only Hash(it).
func New() (string, error) {
	buf := make([]byte, entropyBytes)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand failing means the platform's entropy source is broken.
		// Returning an error rather than falling back is the point: a predictable
		// token is worse than no login at all.
		return "", fmt.Errorf("secret: reading random bytes: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// Hash returns the storable digest of a secret, hex-free and fixed width so it
// slots into a TEXT column.
func Hash(s string) string {
	sum := sha256.Sum256([]byte(s))

	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// Equal reports whether a presented secret matches a stored hash, in constant
// time with respect to the hash contents.
func Equal(presented, storedHash string) bool {
	return subtle.ConstantTimeCompare([]byte(Hash(presented)), []byte(storedHash)) == 1
}
