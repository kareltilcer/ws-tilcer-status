// Package scheduler runs the service's periodic background jobs — the poller and
// the daily rollup→purge closure — as goroutines cancelled by a shared context.
// There is no cron library: the daily fire time is recomputed each iteration
// from a UTC wall clock (UTC has no DST, so the timer never drifts).
package scheduler

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Jobs are the closures the scheduler drives. Daily must itself run the rollup
// BEFORE the retention purge (the caller composes it that way) so no raw check is
// deleted before it is aggregated.
type Jobs struct {
	Poll  func(context.Context)
	Daily func(context.Context)
}

// Config configures the schedule.
type Config struct {
	CheckInterval time.Duration
	DailyAtHour   int // UTC
	DailyAtMin    int // UTC
	Logger        *slog.Logger
}

// Start launches the poller loop and the daily loop. Both stop when ctx is
// cancelled; wg tracks them for a graceful shutdown. The poller runs one cycle
// immediately so the board is fresh on boot.
func Start(ctx context.Context, wg *sync.WaitGroup, cfg Config, j Jobs) {
	wg.Add(2)
	go func() {
		defer wg.Done()
		pollLoop(ctx, cfg.CheckInterval, j.Poll, cfg.Logger)
	}()
	go func() {
		defer wg.Done()
		dailyLoop(ctx, cfg.DailyAtHour, cfg.DailyAtMin, j.Daily, cfg.Logger)
	}()
}

func pollLoop(ctx context.Context, interval time.Duration, job func(context.Context), logger *slog.Logger) {
	runSafe(ctx, job, logger, "poll")
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			runSafe(ctx, job, logger, "poll")
		}
	}
}

func dailyLoop(ctx context.Context, hour, min int, job func(context.Context), logger *slog.Logger) {
	for {
		now := time.Now().UTC()
		next := time.Date(now.Year(), now.Month(), now.Day(), hour, min, 0, 0, time.UTC)
		if !next.After(now) {
			next = next.AddDate(0, 0, 1)
		}
		timer := time.NewTimer(next.Sub(now))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			runSafe(ctx, job, logger, "daily")
		}
	}
}

// runSafe runs one job cycle, recovering from a panic so a single bad run never
// kills the loop.
func runSafe(ctx context.Context, job func(context.Context), logger *slog.Logger, name string) {
	defer func() {
		if p := recover(); p != nil {
			logger.Error("scheduler job panic", "job", name, "panic", p)
		}
	}()
	job(ctx)
}
