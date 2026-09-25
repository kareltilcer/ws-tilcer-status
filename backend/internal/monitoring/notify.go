package monitoring

import (
	"context"
	"database/sql"
	"time"

	"github.com/kareltilcer/ws-tilcer-status/backend/internal/sites"
)

// CheckSignal is what the poller tells a notifier about one written check: its
// outcome, and the color the site was left in.
//
// Only the poller can move a site into or out of red — fail_streak is advanced by
// WriteCheck alone — so this is the one place downtime is observable. The color
// recomputes in crash ingest and triage move a site between orange, green and
// unknown and never cross red.
type CheckSignal struct {
	SiteID     string
	URL        string
	OK         bool
	Color      sites.Color // after this check
	StatusCode *int
	Error      string
	At         time.Time
}

// Notifier is told about every written check, inside that check's transaction.
//
// ⚠ It runs as the LAST statement of the transaction and its error means the
// transaction is no longer usable; the poller then drops that one check exactly
// as it drops any other failed write.
type Notifier interface {
	CheckRecorded(ctx context.Context, tx *sql.Tx, c CheckSignal) error
}

// SetNotifier injects the notifier. Composition only, before the scheduler
// starts; nil — the default — means nothing is told.
func (m *Module) SetNotifier(n Notifier) { m.poller.notifier = n }
