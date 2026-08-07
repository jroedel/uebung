package identitybus

import (
	"time"

	"github.com/jroedel/uebung/business/types/email"
	"github.com/jroedel/uebung/business/types/nickname"
	"github.com/jroedel/uebung/business/types/userid"
)

// User is an account. There is no password field and never will be: the only
// credential is control of the address, demonstrated by following a link sent to
// it.
//
// Verified records that this has actually happened at least once. A row can exist
// unverified — requesting a link creates one — and such an account can do
// nothing until a link is followed, so an address typed by a stranger never
// becomes a usable account on its own.
//
// Email and Nickname are both names for the same person and are treated as
// opposites throughout: the address is private and is never shown to anyone
// else, while the nickname exists precisely to be shown. Nothing in this package
// derives one from the other, and in particular a nickname is never defaulted
// from an address — a learner signing in as firstname.lastname@work.example
// should not discover their full name at the top of a public list.
type User struct {
	ID    userid.UserID
	Email email.Email

	// Nickname is the public display name. The zero value means the learner has
	// not chosen or accepted one yet, which is the state every account starts in
	// and the state the App layer prompts about.
	Nickname nickname.Nickname

	Verified bool
	Created  time.Time
	LastSeen time.Time
}

// HasNickname reports whether a display name has been settled on, either by
// being chosen or by being accepted from a suggestion.
func (u User) HasNickname() bool { return !u.Nickname.IsZero() }

// LoginToken is one issued magic link.
//
// Hash is the SHA-256 of the secret that went out in the email; the secret itself
// is never stored, so this row cannot be turned back into a working link. Used
// marks redemption, because a link that works twice is a link that still works
// after it has been forwarded, quoted in a reply, or scraped from a mailbox.
type LoginToken struct {
	Hash    string
	UserID  userid.UserID
	Created time.Time
	Expires time.Time
	Used    time.Time // zero until redeemed.
}

// IsRedeemable reports whether the token can still be exchanged for a session at
// time now: unused and unexpired.
func (t LoginToken) IsRedeemable(now time.Time) bool {
	return t.Used.IsZero() && now.Before(t.Expires)
}

// Session is a signed-in browser. Hash is the SHA-256 of the cookie value, for
// the same reason as LoginToken.Hash: the database never holds anything that
// could be replayed as a credential.
type Session struct {
	Hash    string
	UserID  userid.UserID
	Created time.Time
	Expires time.Time
}

// IsLive reports whether the session is still valid at time now.
func (s Session) IsLive(now time.Time) bool { return now.Before(s.Expires) }
