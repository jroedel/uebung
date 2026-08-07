// Package mailer sends plain-text email. It is transport only: it knows nothing
// about what a message says or which domain wanted it sent, so it takes addresses
// as strings and never imports a Business type.
//
// The security-relevant part is Message.encode. Every header value is checked for
// CR/LF before it goes out, because a newline smuggled into a subject or an
// address is how an attacker adds their own Bcc: to someone else's mail. The
// address type upstream already rejects those, but this is the last gate before
// the wire and it does not get to assume its caller was careful.
package mailer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// Sender is the transport port. Implementations must not retry indefinitely; the
// caller is a request handler.
type Sender interface {
	Send(ctx context.Context, to, subject, body string) error
}

// ErrUnsafeHeader is returned when a header value contains a line break.
var ErrUnsafeHeader = errors.New("mailer: header value contains a line break")

// SMTP sends through an SMTP server.
type SMTP struct {
	Host     string // e.g. "mail.your-server.de"
	Port     int    // 587 for STARTTLS, 465 for implicit TLS
	Username string
	Password string
	From     string // envelope and From: address
	FromName string // display name, optional

	// Timeout bounds the whole conversation. A sign-in request is waiting on it.
	Timeout time.Duration
}

// Send delivers one plain-text message.
func (s SMTP) Send(ctx context.Context, to, subject, body string) error {
	msg, err := encode(s.From, s.FromName, to, subject, body)
	if err != nil {
		return err
	}

	timeout := s.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}

	addr := net.JoinHostPort(s.Host, fmt.Sprint(s.Port))
	auth := smtp.PlainAuth("", s.Username, s.Password, s.Host)

	// net/smtp has no context support, so the deadline is enforced by running the
	// call in a goroutine and abandoning the wait. The connection is left to its
	// own timeout rather than leaked indefinitely.
	done := make(chan error, 1)
	go func() { done <- smtp.SendMail(addr, auth, s.From, []string{to}, msg) }()

	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("mailer: sending via %s: %w", addr, err)
		}

		return nil
	case <-time.After(timeout):
		return fmt.Errorf("mailer: sending via %s timed out after %s", addr, timeout)
	case <-ctx.Done():
		return fmt.Errorf("mailer: sending via %s cancelled: %w", addr, ctx.Err())
	}
}

// Log writes messages to a logger instead of sending them. It is the local
// development sender: the magic link appears in the server output, so the flow is
// exercisable with no mail server at all.
type Log struct {
	Logger *slog.Logger
}

func (l Log) Send(_ context.Context, to, subject, body string) error {
	l.Logger.Info("email not sent (log mailer)", "to", to, "subject", subject, "body", body)

	return nil
}

// encode builds an RFC 5322 message, rejecting any header value that could break
// out of its header.
func encode(from, fromName, to, subject, body string) ([]byte, error) {
	for _, v := range []string{from, fromName, to, subject} {
		if strings.ContainsAny(v, "\r\n") {
			return nil, ErrUnsafeHeader
		}
	}

	fromHeader := from
	if fromName != "" {
		fromHeader = fmt.Sprintf("%s <%s>", fromName, from)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", fromHeader)
	fmt.Fprintf(&b, "To: %s\r\n", to)
	fmt.Fprintf(&b, "Subject: %s\r\n", subject)
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	// Sign-in mail is transactional and must never be auto-replied to or filed as
	// bulk; these two headers are what mail clients look at to decide that.
	b.WriteString("Auto-Submitted: auto-generated\r\n")
	b.WriteString("X-Auto-Response-Suppress: All\r\n")
	b.WriteString("\r\n")
	b.WriteString(body)

	return []byte(b.String()), nil
}
