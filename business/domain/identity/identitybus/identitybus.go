// Package identitybus is the Business layer for accounts: who a learner is, and
// how they prove it without a password.
//
// The whole flow is three steps. RequestLogin takes an address and emails a
// one-time link. Redeem exchanges that link for a session. Authenticate turns a
// session cookie back into a user on every later request. Everything else here
// exists to make those three safe.
//
// Two properties are worth stating up front because they shape the signatures:
//
//   - Nothing in this package reveals whether an address has an account.
//     RequestLogin behaves identically for a known and an unknown address, so the
//     endpoint in front of it cannot be used to enumerate who has signed up.
//   - Secrets leave this package exactly once, in the return of RequestLogin's
//     mail, and are stored only as hashes. There is no method that can read a
//     token or session secret back out.
package identitybus

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jroedel/uebung/business/types/email"
	"github.com/jroedel/uebung/business/types/nickname"
	"github.com/jroedel/uebung/business/types/userid"
	"github.com/jroedel/uebung/foundation/secret"
)

// ErrInvalidCredential is returned whenever a presented token or session does not
// resolve to a live account.
//
// It is deliberately one error for every failure mode — unknown, expired, already
// used, belongs to a deleted user. The caller cannot tell which, and so neither
// can an attacker probing the callback endpoint.
var ErrInvalidCredential = errors.New("identitybus: invalid or expired credential")

// ErrNicknameTaken is returned when a display name is already held by another
// account. Unlike ErrInvalidCredential, this one is meant to be shown: a learner
// who cannot have the name they asked for needs to know that, and a nickname is
// public anyway, so saying "taken" reveals nothing that reading the leaderboard
// would not.
var ErrNicknameTaken = errors.New("identitybus: that nickname is already taken")

// Storer is the persistence port for accounts, login tokens and sessions.
type Storer interface {
	// UserByEmail returns the account for an address. found is false when there
	// is none; err is for real storage failures only.
	UserByEmail(ctx context.Context, addr email.Email) (User, bool, error)

	// UserByID returns the account for an id.
	UserByID(ctx context.Context, id userid.UserID) (User, bool, error)

	// UserByNickname returns the account holding a display name, comparing on
	// the folded form so that names differing only in case or separators are one
	// name. Used to check a suggestion before offering it.
	UserByNickname(ctx context.Context, name nickname.Nickname) (User, bool, error)

	// SetNickname assigns a display name to an account, reporting false if
	// another account already holds it.
	//
	// Like MarkLoginTokenUsed, this must decide the conflict in storage rather
	// than by a read-then-write here. Two people accepting the same suggested
	// name at the same moment is not a hypothetical — the suggestion is offered
	// to everyone who has not chosen one, from a pool that is finite — and a
	// check followed by a write would let both succeed and leave two accounts
	// sharing a name on a public list.
	SetNickname(ctx context.Context, id userid.UserID, name nickname.Nickname) (bool, error)

	// SaveUser creates or replaces an account by its ID.
	//
	// It does not write the nickname; SetNickname owns that column. Splitting
	// them keeps the uniqueness decision in one statement, and means the
	// ordinary sign-in path — which calls SaveUser on every redemption — cannot
	// disturb a name it was not asked to change.
	SaveUser(ctx context.Context, u User) error

	// SaveLoginToken records an issued magic link.
	SaveLoginToken(ctx context.Context, t LoginToken) error

	// LoginTokenByHash looks a token up by its stored hash.
	LoginTokenByHash(ctx context.Context, hash string) (LoginToken, bool, error)

	// MarkLoginTokenUsed stamps a token as redeemed. It must fail, or report
	// false, if the token was already used — this is the single-use guarantee, and
	// enforcing it in storage rather than in a read-then-write here is what closes
	// the window where two simultaneous clicks both succeed.
	MarkLoginTokenUsed(ctx context.Context, hash string, at time.Time) (bool, error)

	// SaveSession stores a new session.
	SaveSession(ctx context.Context, s Session) error

	// SessionByHash looks a session up by its stored hash.
	SessionByHash(ctx context.Context, hash string) (Session, bool, error)

	// DeleteSession removes one session; used for sign-out.
	DeleteSession(ctx context.Context, hash string) error

	// DeleteExpired purges spent tokens and sessions older than now. Housekeeping,
	// not correctness: the checks above never trust a row's presence alone.
	DeleteExpired(ctx context.Context, now time.Time) error
}

// Mailer sends the magic link. It is a port rather than a concrete SMTP client so
// the tests can assert on what would have been sent, and so swapping providers
// never reaches into this package.
type Mailer interface {
	SendLoginLink(ctx context.Context, to email.Email, link string) error
}

// IDMaker returns the identifier for a new account. Injected so tests get stable
// ids; production passes a UUID generator.
type IDMaker func() (userid.UserID, error)

// NameMaker proposes display names for learners who would rather not invent one.
//
// attempt is how many previous proposals turned out to be taken, so an
// implementation can widen its search rather than offering from the same small
// space forever. It is a port for the same reason Mailer is: the word lists are
// data, and this package should not have to be rebuilt to change them.
type NameMaker interface {
	Generate(attempt int) (nickname.Nickname, error)
}

// Config tunes a Business. Zero values fall back to the defaults in NewBusiness.
type Config struct {
	// TokenTTL bounds how long a magic link works. Short, because the link is a
	// bearer credential sitting in an inbox.
	TokenTTL time.Duration

	// SessionTTL bounds a signed-in browser. Long, because this is a habit app on
	// a phone and being logged out breaks the habit; the mitigation for the length
	// is that sign-out deletes the row server-side.
	SessionTTL time.Duration

	// LinkBase is the absolute URL the emailed link points at, e.g.
	// "https://uebung.club/auth/callback". It must be absolute and https in
	// production: the token travels in it.
	LinkBase string

	Now    func() time.Time
	NewID  IDMaker
	Mailer Mailer
	Storer Storer
	Namer  NameMaker
}

// Business is the identity core.
type Business struct {
	store      Storer
	mailer     Mailer
	newID      IDMaker
	namer      NameMaker
	now        func() time.Time
	tokenTTL   time.Duration
	sessionTTL time.Duration
	linkBase   string
}

// Default lifetimes. Fifteen minutes is long enough for mail to be delivered and
// read, short enough that a link left in an inbox is usually already dead. Ninety
// days matches how long a phone habit app should stay signed in.
const (
	defaultTokenTTL   = 15 * time.Minute
	defaultSessionTTL = 90 * 24 * time.Hour
)

// NewBusiness constructs the identity core.
func NewBusiness(cfg Config) (*Business, error) {
	if cfg.Storer == nil {
		return nil, errors.New("identitybus: a Storer is required")
	}
	if cfg.Mailer == nil {
		return nil, errors.New("identitybus: a Mailer is required")
	}
	if cfg.LinkBase == "" {
		return nil, errors.New("identitybus: a LinkBase is required")
	}
	if cfg.NewID == nil {
		return nil, errors.New("identitybus: a NewID is required")
	}
	if cfg.Namer == nil {
		return nil, errors.New("identitybus: a Namer is required")
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	tokenTTL := cfg.TokenTTL
	if tokenTTL <= 0 {
		tokenTTL = defaultTokenTTL
	}

	sessionTTL := cfg.SessionTTL
	if sessionTTL <= 0 {
		sessionTTL = defaultSessionTTL
	}

	return &Business{
		store:      cfg.Storer,
		mailer:     cfg.Mailer,
		newID:      cfg.NewID,
		namer:      cfg.Namer,
		now:        now,
		tokenTTL:   tokenTTL,
		sessionTTL: sessionTTL,
		linkBase:   cfg.LinkBase,
	}, nil
}

// RequestLogin emails a one-time sign-in link to addr, creating the account if it
// does not exist yet.
//
// It returns no indication of whether the address was already known. That is the
// point: this endpoint is open to the internet, and a response that differed for
// known addresses would turn it into a "does this person use the site" oracle.
func (b *Business) RequestLogin(ctx context.Context, addr email.Email) error {
	if addr.IsZero() {
		return errors.New("identitybus: an email address is required")
	}

	now := b.now()

	user, found, err := b.store.UserByEmail(ctx, addr)
	if err != nil {
		return fmt.Errorf("identitybus: looking up %s: %w", addr.Domain(), err)
	}

	if !found {
		id, err := b.newID()
		if err != nil {
			return fmt.Errorf("identitybus: minting user id: %w", err)
		}

		// Created unverified. Until a link is followed this account can do
		// nothing, so a stranger typing someone else's address achieves only an
		// unusable row and one email.
		user = User{ID: id, Email: addr, Created: now, LastSeen: now}
		if err := b.store.SaveUser(ctx, user); err != nil {
			return fmt.Errorf("identitybus: creating account: %w", err)
		}
	}

	raw, err := secret.New()
	if err != nil {
		return fmt.Errorf("identitybus: minting token: %w", err)
	}

	token := LoginToken{
		Hash:    secret.Hash(raw),
		UserID:  user.ID,
		Created: now,
		Expires: now.Add(b.tokenTTL),
	}
	if err := b.store.SaveLoginToken(ctx, token); err != nil {
		return fmt.Errorf("identitybus: saving token: %w", err)
	}

	// The raw secret exists only in this local and in the mail. Nothing above
	// this line stored it, and nothing below can recover it.
	if err := b.mailer.SendLoginLink(ctx, addr, b.linkBase+"?token="+raw); err != nil {
		return fmt.Errorf("identitybus: sending link: %w", err)
	}

	return nil
}

// Peek reports which account a magic-link token belongs to WITHOUT consuming it.
//
// It exists so the App layer can show "sign in as this address?" before anything
// is spent. That confirmation step is what stops a link being redeemed by a
// cross-site navigation the person never chose to make: an attacker can send a
// victim to the callback, but cannot make the victim submit the form.
//
// Peek deliberately does not mark the token used, so a person who closes the
// confirmation page still has a working link. Every failure is the same
// ErrInvalidCredential as Redeem, so this cannot be used to probe which tokens
// exist.
func (b *Business) Peek(ctx context.Context, rawToken string) (User, error) {
	if rawToken == "" {
		return User{}, ErrInvalidCredential
	}

	token, found, err := b.store.LoginTokenByHash(ctx, secret.Hash(rawToken))
	if err != nil {
		return User{}, fmt.Errorf("identitybus: loading token: %w", err)
	}
	if !found || !token.IsRedeemable(b.now()) {
		return User{}, ErrInvalidCredential
	}

	user, found, err := b.store.UserByID(ctx, token.UserID)
	if err != nil {
		return User{}, fmt.Errorf("identitybus: loading account: %w", err)
	}
	if !found {
		return User{}, ErrInvalidCredential
	}

	return user, nil
}

// Redeem exchanges a magic-link token for a new session, returning the raw
// session secret for the caller to set as a cookie and the user it belongs to.
//
// Redeeming is also what verifies an address: reaching here proves the person
// controls the inbox the link was sent to.
func (b *Business) Redeem(ctx context.Context, rawToken string) (string, User, error) {
	if rawToken == "" {
		return "", User{}, ErrInvalidCredential
	}

	now := b.now()

	token, found, err := b.store.LoginTokenByHash(ctx, secret.Hash(rawToken))
	if err != nil {
		return "", User{}, fmt.Errorf("identitybus: loading token: %w", err)
	}
	if !found || !token.IsRedeemable(now) {
		return "", User{}, ErrInvalidCredential
	}

	// Claim the token before issuing anything. The store reports false if someone
	// else claimed it first, so two clicks on the same link cannot both produce a
	// session.
	claimed, err := b.store.MarkLoginTokenUsed(ctx, token.Hash, now)
	if err != nil {
		return "", User{}, fmt.Errorf("identitybus: claiming token: %w", err)
	}
	if !claimed {
		return "", User{}, ErrInvalidCredential
	}

	user, found, err := b.store.UserByID(ctx, token.UserID)
	if err != nil {
		return "", User{}, fmt.Errorf("identitybus: loading account: %w", err)
	}
	if !found {
		return "", User{}, ErrInvalidCredential
	}

	user.Verified = true
	user.LastSeen = now
	if err := b.store.SaveUser(ctx, user); err != nil {
		return "", User{}, fmt.Errorf("identitybus: verifying account: %w", err)
	}

	raw, err := secret.New()
	if err != nil {
		return "", User{}, fmt.Errorf("identitybus: minting session: %w", err)
	}

	session := Session{
		Hash:    secret.Hash(raw),
		UserID:  user.ID,
		Created: now,
		Expires: now.Add(b.sessionTTL),
	}
	if err := b.store.SaveSession(ctx, session); err != nil {
		return "", User{}, fmt.Errorf("identitybus: saving session: %w", err)
	}

	return raw, user, nil
}

// Authenticate resolves a session cookie to its user. This runs on every request,
// so it is deliberately one indexed lookup plus one user read.
func (b *Business) Authenticate(ctx context.Context, rawSession string) (User, error) {
	if rawSession == "" {
		return User{}, ErrInvalidCredential
	}

	session, found, err := b.store.SessionByHash(ctx, secret.Hash(rawSession))
	if err != nil {
		return User{}, fmt.Errorf("identitybus: loading session: %w", err)
	}
	if !found || !session.IsLive(b.now()) {
		return User{}, ErrInvalidCredential
	}

	user, found, err := b.store.UserByID(ctx, session.UserID)
	if err != nil {
		return User{}, fmt.Errorf("identitybus: loading account: %w", err)
	}

	// An unverified user holding a live session should be impossible, since only
	// Redeem issues sessions and it verifies first. Checked anyway: this is the
	// gate every study request passes through.
	if !found || !user.Verified {
		return User{}, ErrInvalidCredential
	}

	return user, nil
}

// Logout destroys one session. It deletes server-side rather than relying on the
// cookie being cleared, so "sign out" also covers a device you no longer hold.
func (b *Business) Logout(ctx context.Context, rawSession string) error {
	if rawSession == "" {
		return nil
	}

	if err := b.store.DeleteSession(ctx, secret.Hash(rawSession)); err != nil {
		return fmt.Errorf("identitybus: deleting session: %w", err)
	}

	return nil
}

// --- nicknames --------------------------------------------------------------

// suggestAttempts bounds how many candidates SuggestNickname will try before
// giving up, and how many times AssignNickname will retry a losing race.
//
// Each attempt past the third carries a number, so the space being drawn from
// grows rather than repeating; six is far more than enough to find a free name
// and small enough that a storage problem cannot turn into a long loop.
const suggestAttempts = 6

// SuggestNickname proposes a display name that is currently free.
//
// It reserves nothing. The name is a suggestion to show someone who has not
// picked one — "skip, and be Blaue Eule" — and between showing it and their
// accepting it, somebody else may take it. That race is handled where it is
// decided, in AssignNickname, rather than by holding a reservation that would
// need its own expiry and its own cleanup.
func (b *Business) SuggestNickname(ctx context.Context) (nickname.Nickname, error) {
	for attempt := range suggestAttempts {
		name, err := b.namer.Generate(attempt)
		if err != nil {
			return nickname.Nickname{}, fmt.Errorf("identitybus: generating nickname: %w", err)
		}

		free, err := b.nicknameIsFree(ctx, name)
		if err != nil {
			return nickname.Nickname{}, err
		}
		if free {
			return name, nil
		}
	}

	return nickname.Nickname{}, errors.New("identitybus: could not find a free nickname to suggest")
}

// SetNickname gives an account the name it asked for, and is the path behind
// both choosing a first name and renaming later.
//
// Renaming frees the previous name immediately. That is a deliberate choice
// rather than an oversight: holding a name in reserve after its owner has left
// it would need an expiry policy, and the alternative harm — somebody taking a
// name a person has just abandoned — is one this app has no reason to expect
// yet. It is worth revisiting once names are publicly visible and therefore
// publicly worth squatting.
func (b *Business) SetNickname(ctx context.Context, id userid.UserID, name nickname.Nickname) (User, error) {
	if name.IsZero() {
		return User{}, errors.New("identitybus: a nickname is required")
	}

	user, found, err := b.store.UserByID(ctx, id)
	if err != nil {
		return User{}, fmt.Errorf("identitybus: loading account: %w", err)
	}
	if !found {
		return User{}, ErrInvalidCredential
	}

	// Note that re-spelling a name you already hold — "blaue eule" to "Blaue
	// Eule" — folds to the key your own row already has. The store excludes the
	// holder's own row from the conflict, so this succeeds rather than colliding
	// with itself.
	claimed, err := b.store.SetNickname(ctx, id, name)
	if err != nil {
		return User{}, fmt.Errorf("identitybus: setting nickname: %w", err)
	}
	if !claimed {
		return User{}, ErrNicknameTaken
	}

	user.Nickname = name

	return user, nil
}

// AssignNickname gives an account a name without making anyone choose one. This
// is what "skip" does: everyone ends up with a name, and a learner who does not
// want to pick one still gets something a leaderboard can print.
//
// preferred is the name the learner was actually shown — on a button reading
// "Rather not choose? Be Blaue Eule" — and claiming it is the whole point:
// having been offered a specific name and agreed to it, that is the name they
// expect to find on their account. Passing the zero value asks for any free
// name, which is what a caller with nothing to honour should do.
//
// A generated name is used only as a fallback, because the suggestion reserves
// nothing and may have been taken between being offered and being accepted.
// Losing that race stays silent: the person asked not to think about this, so
// handing them a different name beats an error.
func (b *Business) AssignNickname(ctx context.Context, id userid.UserID, preferred nickname.Nickname) (User, error) {
	if !preferred.IsZero() {
		user, err := b.SetNickname(ctx, id, preferred)
		if err == nil {
			return user, nil
		}
		if !errors.Is(err, ErrNicknameTaken) {
			return User{}, err
		}
	}

	for attempt := range suggestAttempts {
		name, err := b.namer.Generate(attempt)
		if err != nil {
			return User{}, fmt.Errorf("identitybus: generating nickname: %w", err)
		}

		user, err := b.SetNickname(ctx, id, name)
		if errors.Is(err, ErrNicknameTaken) {
			continue
		}
		if err != nil {
			return User{}, err
		}

		return user, nil
	}

	return User{}, errors.New("identitybus: could not find a free nickname to assign")
}

// nicknameIsFree reports whether no account holds a name, comparing on the
// folded form.
func (b *Business) nicknameIsFree(ctx context.Context, name nickname.Nickname) (bool, error) {
	_, found, err := b.store.UserByNickname(ctx, name)
	if err != nil {
		return false, fmt.Errorf("identitybus: checking nickname: %w", err)
	}

	return !found, nil
}

// Purge removes expired tokens and sessions. Housekeeping only — every check
// above tests expiry explicitly, so a missed purge is untidy, not unsafe.
func (b *Business) Purge(ctx context.Context) error {
	if err := b.store.DeleteExpired(ctx, b.now()); err != nil {
		return fmt.Errorf("identitybus: purging: %w", err)
	}

	return nil
}

// SessionTTL reports the configured session lifetime, so the App layer can set a
// cookie Max-Age that agrees with the server-side expiry.
func (b *Business) SessionTTL() time.Duration { return b.sessionTTL }
