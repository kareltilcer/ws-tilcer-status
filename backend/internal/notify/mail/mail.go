// Package mail is the notify module's view of an email provider: one Send, one
// error type that says whether a retry can help, and nothing provider-specific
// leaking past it. It mirrors feedback/blob — an interface the module depends on,
// a real implementation (Resend), a log-only one for local development, and an
// in-memory fake in mailtest.
//
// ⚠ Send is a network call. Like every R2 call it must never run inside a
// transaction or with a rows cursor open: the service has ONE database
// connection, and a mail provider's TCP timeout would hold it for everyone.
// mailtest's probe is what asserts that (TestNoMailSendInsideATransaction).
package mail

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Message is one email as the provider receives it. Everything in it is frozen
// at the time the notify worker renders a digest: a retry must send the SAME
// request, or the provider's idempotency check refuses it (see IdempotencyKey).
type Message struct {
	From    string
	To      []string
	Subject string
	Text    string
	HTML    string // optional; the text part is always sent
	// IdempotencyKey makes a retried send safe: the provider delivers a key at
	// most once within its window and answers a repeat with the original result.
	// Empty sends without one.
	IdempotencyKey string
}

// Result is what the provider reports for an accepted message.
type Result struct {
	ID string // the provider's message id
}

// Mailer sends one message.
type Mailer interface {
	Send(ctx context.Context, m Message) (Result, error)
	// Provider names the implementation for the dashboard ("resend", "log").
	Provider() string
}

// SendError is a refusal the provider actually answered with. A transport
// failure (DNS, TLS, timeout) is NOT a SendError — it is returned as-is and is
// always worth retrying, because the provider may never have seen the request.
type SendError struct {
	Status int    // HTTP status
	Code   string // the provider's error name, e.g. "validation_error"
	Detail string // the provider's message, truncated
	// RetryAfter is the provider's own hint (Retry-After), zero when absent.
	RetryAfter time.Duration
}

func (e *SendError) Error() string {
	s := fmt.Sprintf("resend: %d", e.Status)
	if e.Code != "" {
		s += " " + e.Code
	}
	if e.Detail != "" {
		s += ": " + e.Detail
	}
	return s
}

// Permanent reports whether sending the same message again cannot succeed.
//
// ⚠ 401 and 403 are deliberately NOT permanent. They mean the key is wrong or the
// sender's domain is not verified — both fixed in the environment, neither by
// changing the stored message — so a digest that keeps its place in the queue is
// delivered once the operator fixes the deployment, instead of being thrown away
// for a configuration mistake. A 422 invalid_from_address is the same kind of
// mistake — STATUS_MAIL_FROM in a form net/mail accepts and Resend does not, say
// "status@tilcer.cz (status)" — and fixing it supersedes the held digest, which a
// permanent failure would already have thrown away. A 409 is permanent only when
// the key was reused with a DIFFERENT body; "the first request with this key is
// still in flight" is exactly the case a later retry resolves.
func (e *SendError) Permanent() bool {
	switch e.Status {
	case 400, 404, 405, 413:
		return true
	case 422:
		return e.Code != "invalid_from_address"
	case 409:
		return e.Code == "invalid_idempotent_request"
	}
	return false
}

// IsPermanent reports whether err is a provider refusal that a retry cannot fix.
func IsPermanent(err error) bool {
	var se *SendError
	return errors.As(err, &se) && se.Permanent()
}

// RetryAfterOf returns the provider's Retry-After hint carried by err, if any.
func RetryAfterOf(err error) time.Duration {
	var se *SendError
	if errors.As(err, &se) {
		return se.RetryAfter
	}
	return 0
}
