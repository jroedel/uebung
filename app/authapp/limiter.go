package authapp

import (
	"sync"
	"time"
)

// limiter is a fixed-window counter keyed by an arbitrary string.
//
// Sign-up here is open to anyone, which means /auth/request is a button on the
// public internet that makes our mail server send a message. Unlimited, that is a
// spam cannon aimed at the domain's own sending reputation, and the cost lands on
// us rather than on whoever pushed the button. Two independent keys are limited:
// the client address, and the target address — because one attacker with many IPs
// mailbombing one victim and one IP enumerating many victims are different
// attacks, and stopping only one leaves the other open.
//
// A fixed window rather than a token bucket: the useful property is "no more than
// N mails to this address per hour", which a window states directly.
type limiter struct {
	mu     sync.Mutex
	window time.Duration
	max    int
	hits   map[string]*counter
	now    func() time.Time
}

type counter struct {
	count int
	reset time.Time
}

func newLimiter(max int, window time.Duration, now func() time.Time) *limiter {
	return &limiter{window: window, max: max, hits: make(map[string]*counter), now: now}
}

// allow records an attempt against key and reports whether it is within budget.
func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()

	c, ok := l.hits[key]
	if !ok || !now.Before(c.reset) {
		l.hits[key] = &counter{count: 1, reset: now.Add(l.window)}

		return true
	}

	if c.count >= l.max {
		return false
	}

	c.count++

	return true
}

// sweep drops windows that have closed. Called opportunistically rather than from
// a goroutine so the limiter has no lifecycle to manage; the map only grows as
// fast as distinct callers arrive within one window.
func (l *limiter) sweep() {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	for k, c := range l.hits {
		if !now.Before(c.reset) {
			delete(l.hits, k)
		}
	}
}
