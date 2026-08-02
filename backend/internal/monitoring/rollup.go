package monitoring

import (
	"context"
	"log/slog"
	"time"

	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/timeutil"
)

// Rollup owns the nightly aggregation: it turns raw checks into daily
// check_rollup rows and refreshes each site's cached uptime_pct. It never scans
// raw checks for the board or the uptime endpoint.
type Rollup struct {
	store            *Store
	uptimeWindowDays int
	logger           *slog.Logger
}

// NewRollup builds the rollup job runner.
func NewRollup(store *Store, uptimeWindowDays int, logger *slog.Logger) *Rollup {
	return &Rollup{store: store, uptimeWindowDays: uptimeWindowDays, logger: logger}
}

// RunForDay aggregates one UTC day (idempotent).
func (r *Rollup) RunForDay(ctx context.Context, day string) error {
	return r.store.RollupDay(ctx, day)
}

// CatchUp aggregates every past day (<= yesterday) that has raw checks but no
// rollup — run once on boot so a downtime gap is filled before the purge can
// remove those raw rows.
func (r *Rollup) CatchUp(ctx context.Context, now time.Time) error {
	yesterday := timeutil.Day(now.AddDate(0, 0, -1))
	days, err := r.store.DaysNeedingRollup(ctx, yesterday)
	if err != nil {
		return err
	}
	for _, d := range days {
		if err := r.store.RollupDay(ctx, d); err != nil {
			return err
		}
	}
	if len(days) > 0 {
		r.logger.Info("rollup catch-up", "days", len(days))
	}
	return nil
}

// RefreshUptimeCache updates every site's cached uptime_pct over UPTIME_WINDOW.
// The cutoff spans exactly uptimeWindowDays whole UTC days ending with today
// (today-(N-1) .. today), matching the /uptime endpoint's day-aligned window
// (uptime.go) so the board card and the detail strip report the same span. Using
// -uptimeWindowDays here would include one extra day (N+1) and disagree.
func (r *Rollup) RefreshUptimeCache(ctx context.Context, now time.Time) error {
	cutoff := timeutil.Day(now.AddDate(0, 0, -(r.uptimeWindowDays - 1)))
	return r.store.RefreshUptimeCache(ctx, cutoff)
}

// RunDaily rolls up every day still needing aggregation (up to yesterday) and
// refreshes the uptime cache. It rolls up the whole backlog — not just yesterday
// — so a day whose rollup previously failed (or was missed while the process was
// down) is retried here. The scheduler runs this before the retention purge and
// skips the purge when it returns an error, so no raw check is ever deleted
// before it has been aggregated.
func (r *Rollup) RunDaily(ctx context.Context, now time.Time) error {
	if err := r.CatchUp(ctx, now); err != nil {
		return err
	}
	return r.RefreshUptimeCache(ctx, now)
}
