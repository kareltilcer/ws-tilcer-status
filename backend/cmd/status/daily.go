package main

import (
	"context"
	"log/slog"
	"time"
)

// The daily job's three steps, as narrow interfaces so the composition below can
// be exercised without a database, a poller or a bucket.
type (
	// dailyRollup aggregates yesterday's checks and refreshes the uptime cache.
	dailyRollup interface {
		RunDaily(ctx context.Context, now time.Time) error
	}
	// dailyPurge applies RETENTION_DAYS to check_result and crash_event.
	dailyPurge interface {
		Purge(ctx context.Context, now time.Time) error
	}
	// dailySweep resolves unclaimed attachments and collects orphaned objects. It
	// returns nothing: a bucket problem is reported by the sweep itself and is not
	// a reason to fail the job.
	dailySweep interface {
		Sweep(ctx context.Context, now time.Time)
	}
)

// runDailyJob is the daily chain, in the one order that is correct:
// rollup → purge → feedback sweep.
//
// ⚠ Rollup before purge, or a raw check is deleted before it is aggregated. If
// the rollup fails the purge is SKIPPED entirely, so the un-aggregated checks
// survive to be retried on the next run rather than being deleted and leaving a
// permanent uptime gap.
//
// ⚠ The sweep runs LAST (V3-D25). It is the only step that talks to the network,
// and it must not be able to delay the two that keep the database honest.
//
// It lives here, named, rather than as a closure inside run() because the order
// is a normative requirement (PRD §V3-11) and a closure cannot be tested.
func runDailyJob(ctx context.Context, logger *slog.Logger, rollup dailyRollup, purge dailyPurge, sweep dailySweep, now time.Time) {
	if err := rollup.RunDaily(ctx, now); err != nil {
		logger.Error("daily rollup", "err", err)
		return
	}
	if err := purge.Purge(ctx, now); err != nil {
		logger.Error("retention purge", "err", err)
	}
	sweep.Sweep(ctx, now)
}
