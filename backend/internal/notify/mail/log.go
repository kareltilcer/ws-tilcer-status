package mail

import (
	"context"
	"log/slog"
)

// Log writes each message to the log instead of sending it. It is what a
// development deployment gets when no Resend key is set, so the whole pipeline —
// enqueue, digest, the dashboard's test button — can be exercised offline.
//
// ⚠ It is never built in production: a notification is an excerpt of a crash or
// of what a household member typed, and stdout is not where either belongs.
type Log struct {
	logger *slog.Logger
}

// NewLog returns a log-only mailer.
func NewLog(logger *slog.Logger) *Log { return &Log{logger: logger} }

// Provider implements Mailer.
func (l *Log) Provider() string { return "log" }

// Send implements Mailer.
func (l *Log) Send(_ context.Context, m Message) (Result, error) {
	l.logger.Info("mail.send (log-only, not delivered)",
		"from", m.From, "to", m.To, "subject", m.Subject,
		"idempotency_key", m.IdempotencyKey, "text", m.Text)
	return Result{ID: "log-" + m.IdempotencyKey}, nil
}
