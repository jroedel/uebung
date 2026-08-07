// Package memdb is an in-memory identitybus.Storer for tests.
//
// It holds Business models directly rather than converting through a row type,
// because there is no serialization boundary to cross. The one piece of real
// behaviour it must reproduce faithfully is MarkLoginTokenUsed's single-use
// guarantee, since that is a correctness property the tests need to exercise.
package memdb

import (
	"context"
	"sync"
	"time"

	"github.com/jroedel/uebung/business/domain/identity/identitybus"
	"github.com/jroedel/uebung/business/types/email"
	"github.com/jroedel/uebung/business/types/nickname"
	"github.com/jroedel/uebung/business/types/userid"
)

// Store is a concurrency-safe in-memory identity store.
type Store struct {
	mu       sync.Mutex
	users    map[string]identitybus.User // by user id
	tokens   map[string]identitybus.LoginToken
	sessions map[string]identitybus.Session
}

// New constructs an empty store.
func New() *Store {
	return &Store{
		users:    make(map[string]identitybus.User),
		tokens:   make(map[string]identitybus.LoginToken),
		sessions: make(map[string]identitybus.Session),
	}
}

func (s *Store) UserByEmail(_ context.Context, addr email.Email) (identitybus.User, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, u := range s.users {
		if u.Email == addr {
			return u, true, nil
		}
	}

	return identitybus.User{}, false, nil
}

func (s *Store) UserByID(_ context.Context, id userid.UserID) (identitybus.User, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	u, ok := s.users[id.String()]

	return u, ok, nil
}

// UserByNickname finds an account by the folded form of a name, matching what
// the SQL store's unique index compares.
func (s *Store) UserByNickname(_ context.Context, name nickname.Nickname) (identitybus.User, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	fold := name.Fold()
	for _, u := range s.users {
		if !u.Nickname.IsZero() && u.Nickname.Fold() == fold {
			return u, true, nil
		}
	}

	return identitybus.User{}, false, nil
}

// SetNickname claims a name, reporting false if another account holds it.
//
// The scan and the write happen under one lock, which is the in-memory
// equivalent of the SQL store's single conditional UPDATE. Reproducing that
// faithfully is the point of this store: the race it closes — two people
// accepting the same suggestion at once — is one the business tests need to be
// able to exercise without a database.
func (s *Store) SetNickname(_ context.Context, id userid.UserID, name nickname.Nickname) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	user, ok := s.users[id.String()]
	if !ok {
		return false, nil
	}

	// The holder's own row does not count as a conflict, so re-spelling a name
	// you already have succeeds.
	fold := name.Fold()
	for otherID, other := range s.users {
		if otherID != id.String() && !other.Nickname.IsZero() && other.Nickname.Fold() == fold {
			return false, nil
		}
	}

	user.Nickname = name
	s.users[id.String()] = user

	return true, nil
}

// SaveUser stores an account, leaving the nickname as it was.
//
// The carve-out mirrors the SQL store, where the nickname is simply not among
// the columns this statement writes. Without it the in-memory store would be the
// more permissive of the two, and a test would pass here on behaviour that the
// real database does not have.
func (s *Store) SaveUser(_ context.Context, u identitybus.User) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.users[u.ID.String()]; ok {
		u.Nickname = existing.Nickname
	}

	s.users[u.ID.String()] = u

	return nil
}

func (s *Store) SaveLoginToken(_ context.Context, t identitybus.LoginToken) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.tokens[t.Hash] = t

	return nil
}

func (s *Store) LoginTokenByHash(_ context.Context, hash string) (identitybus.LoginToken, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	t, ok := s.tokens[hash]

	return t, ok, nil
}

// MarkLoginTokenUsed claims a token, reporting false if it was already claimed.
// The check and the write happen under one lock, which is the in-memory
// equivalent of the conditional UPDATE the SQL store uses.
func (s *Store) MarkLoginTokenUsed(_ context.Context, hash string, at time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	t, ok := s.tokens[hash]
	if !ok || !t.Used.IsZero() {
		return false, nil
	}

	t.Used = at
	s.tokens[hash] = t

	return true, nil
}

func (s *Store) SaveSession(_ context.Context, sess identitybus.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sessions[sess.Hash] = sess

	return nil
}

func (s *Store) SessionByHash(_ context.Context, hash string) (identitybus.Session, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[hash]

	return sess, ok, nil
}

func (s *Store) DeleteSession(_ context.Context, hash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.sessions, hash)

	return nil
}

func (s *Store) DeleteExpired(_ context.Context, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for h, t := range s.tokens {
		if !now.Before(t.Expires) {
			delete(s.tokens, h)
		}
	}
	for h, sess := range s.sessions {
		if !now.Before(sess.Expires) {
			delete(s.sessions, h)
		}
	}

	return nil
}
