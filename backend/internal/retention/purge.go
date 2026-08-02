// Package retention keeps the database lean: a daily sweep deletes raw checks
// and crash events past the retention window, deletes rollups past the (longer)
// rollup window, and removes crash groups left with no events. The scheduler runs
// it AFTER the nightly rollup so no check is deleted before it is aggregated.
package retention

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/timeutil"
)

// Purger runs the retention sweep.
type Purger struct {
	db                  *sql.DB
	retentionDays       int // raw check_result / crash_event window
	rollupRetentionDays int // check_rollup window (kept longer than raw)
	logger              *slog.Logger
}

// NewPurger builds the retention job.
func NewPurger(db *sql.DB, retentionDays, rollupRetentionDays int, logger *slog.Logger) *Purger {
	return &Purger{db: db, retentionDays: retentionDays, rollupRetentionDays: rollupRetentionDays, logger: logger}
}

// Purge deletes raw check_result / crash_event rows older than retentionDays,
// check_rollup rows older than rollupRetentionDays, and any crash group left with
// no events. A failed DELETE is logged and does not abort the remaining sweeps;
// all failures are joined and returned so the caller (scheduler) gets a signal.
// Crash events are purged by received_at (server clock), not the client-spoofable
// occurred_at.
func (p *Purger) Purge(ctx context.Context, now time.Time) error {
	rawCutoff := timeutil.Format(now.AddDate(0, 0, -p.retentionDays))
	rollupCutoffDay := timeutil.Day(now.AddDate(0, 0, -p.rollupRetentionDays))

	checks, errChecks := p.exec(ctx, "check_result", `DELETE FROM check_result WHERE checked_at < ?`, rawCutoff)
	events, errEvents := p.exec(ctx, "crash_event", `DELETE FROM crash_event WHERE received_at < ?`, rawCutoff)
	rollups, errRollups := p.exec(ctx, "check_rollup", `DELETE FROM check_rollup WHERE day < ?`, rollupCutoffDay)
	// Remove groups left empty after their events were purged.
	groups, errGroups := p.exec(ctx, "crash_group",
		`DELETE FROM crash_group WHERE NOT EXISTS (SELECT 1 FROM crash_event e WHERE e.group_id = crash_group.id)`)

	p.logger.Info("retention purge",
		"checks_deleted", checks, "events_deleted", events, "rollups_deleted", rollups, "empty_groups_deleted", groups)
	return errors.Join(errChecks, errEvents, errRollups, errGroups)
}

func (p *Purger) exec(ctx context.Context, table, query string, args ...any) (int64, error) {
	res, err := p.db.ExecContext(ctx, query, args...)
	if err != nil {
		p.logger.Error("retention purge failed", "table", table, "err", err)
		return 0, fmt.Errorf("retention: purge %s: %w", table, err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}
