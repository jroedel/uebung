// Package loginmail composes the sign-in email and hands it to a transport.
//
// It exists so that identitybus can depend on "something that sends a login
// link" while foundation/mailer stays ignorant of what a login link is. The
// wording lives here because it is product copy, not transport.
package loginmail

import (
	"context"
	"fmt"
	"time"

	"github.com/jroedel/uebung/business/types/email"
	"github.com/jroedel/uebung/foundation/mailer"
)

// Mailer composes login links over a transport.
type Mailer struct {
	sender  mailer.Sender
	appName string
	ttl     time.Duration
}

// New builds a login mailer. ttl is stated in the message so the reader knows the
// link is perishable before they leave it for later.
func New(sender mailer.Sender, appName string, ttl time.Duration) Mailer {
	return Mailer{sender: sender, appName: appName, ttl: ttl}
}

// SendLoginLink implements identitybus.Mailer.
func (m Mailer) SendLoginLink(ctx context.Context, to email.Email, link string) error {
	subject := fmt.Sprintf("Your %s sign-in link", m.appName)

	// Deliberately plain text and short. A sign-in mail that looks like marketing
	// gets filtered like marketing, and this one has to arrive.
	body := fmt.Sprintf(`Tap the link below to sign in to %s:

%s

The link works once and expires in %s.

If you didn't ask to sign in, you can ignore this message — nothing was
created or changed, and no one can use this link but you.
`, m.appName, link, humanDuration(m.ttl))

	if err := m.sender.Send(ctx, to.String(), subject, body); err != nil {
		return fmt.Errorf("loginmail: %w", err)
	}

	return nil
}

// humanDuration renders a TTL the way a person would say it, so the email does
// not read "15m0s".
func humanDuration(d time.Duration) string {
	switch {
	case d >= time.Hour:
		h := int(d.Hours())

		return fmt.Sprintf("%d hour%s", h, plural(h))
	default:
		m := int(d.Minutes())

		return fmt.Sprintf("%d minute%s", m, plural(m))
	}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}

	return "s"
}
