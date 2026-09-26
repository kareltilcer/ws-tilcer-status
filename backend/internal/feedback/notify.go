package feedback

import (
	"context"
	"database/sql"
	"time"
)

// ReportSignal is what submit tells a notifier about one accepted report.
//
// It deliberately carries none of the report's context — reporter label, page
// URL, user agent, console tail, IP hash. A notification leaves this service for
// a mail provider; the message excerpt and a link back are all it needs.
type ReportSignal struct {
	SiteID  string
	Ref     string
	Kind    string
	Message string
	// Attachments is how many files the reporter DECLARED. They are uploaded after
	// the 202, straight to R2, so nothing about them is known yet.
	Attachments int
	At          time.Time
}

// Notifier is told about every accepted report, inside the insert transaction.
//
// ⚠ It runs as the last statement before the commit, and its error means the
// transaction is no longer usable. A notification that could not be queued is
// the notifier's to log — it must never cost the report, and it must never come
// back as a UNIQUE failure, which InsertReport would read as a ref collision.
type Notifier interface {
	ReportSubmitted(ctx context.Context, tx *sql.Tx, s ReportSignal) error
}

// SetNotifier injects the notifier. Composition only, before the router serves;
// nil — the default — means nothing is told.
func (m *Module) SetNotifier(n Notifier) { m.notifier = n }
