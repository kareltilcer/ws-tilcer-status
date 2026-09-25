package crash

import (
	"context"
	"database/sql"
	"time"
)

// Signal is what ingest tells a notifier about one accepted event: the facts, and
// no decision. Whether the event is worth an email — its level, its environment,
// whether the group was already announced — is the notifier's business, because
// the same group can be first seen in a developer's browser and only later in
// production.
type Signal struct {
	SiteID  string
	GroupID int64
	EventID int64
	// Created is true when this event opened a brand-new group; Reopened when it
	// brought a resolved group back (STATUS_REOPEN_ON_REGRESSION). A manual reopen
	// through triage is neither — it is Karel's own action, not news.
	Created     bool
	Reopened    bool
	GroupStatus string // the group's status after this event
	Level       string // this EVENT's level, not the group's highest
	Environment string
	Release     string
	Title       string
	Message     string
	At          time.Time
}

// Notifier is told about every accepted event, inside the ingest transaction.
//
// ⚠ It runs as the LAST statement of that transaction and its error means only
// one thing: the transaction itself is no longer usable, so the caller must
// return it. A notification that could not be queued is the notifier's problem
// to log — it must never cost the crash it describes.
type Notifier interface {
	CrashRecorded(ctx context.Context, tx *sql.Tx, s Signal) error
}

// SetNotifier injects the notifier. Composition only (cmd/status, tests), before
// the router serves; nil — the default — means nothing is told.
func (m *Module) SetNotifier(n Notifier) { m.notifier = n }
