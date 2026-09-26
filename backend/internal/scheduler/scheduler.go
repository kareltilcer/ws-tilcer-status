// Package scheduler runs the service's periodic background jobs — the poller, the
// notification worker and the daily rollup→purge closure — as goroutines
// cancelled by a shared context.
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
	// Notify, when non-nil, runs every NotifyInterval: the notification worker's
	// pass. It is nil on a deployment with no mail provider.
	Notify func(context.Context)
}

// Config configures the schedule.
type Config struct {
	CheckInterval time.Duration
	DailyAtHour   int // UTC
	DailyAtMin    int // UTC
	// NotifyInterval is how often the Notify job runs (default 15s).
	NotifyInterval time.Duration
	Logger         *slog.Logger
}

// defaultNotifyInterval is often enough that a digest goes out within seconds of
// its window closing, and rare enough to be idle work nobody notices.
const defaultNotifyInterval = 15 * time.Second

// Start launches the poller loop, the daily loop and — when there is one — the
// notification loop. All stop when ctx is cancelled; wg tracks them for a
// graceful shutdown. The tick loops run one cycle immediately: the board is fresh
// on boot, and a digest that came due while the service was down goes out.
func Start(ctx context.Context, wg *sync.WaitGroup, cfg Config, j Jobs) {
	wg.Add(2)
	go func() {
		defer wg.Done()
		tickLoop(ctx, cfg.CheckInterval, j.Poll, cfg.Logger, "poll")
	}()
	go func() {
		defer wg.Done()
		dailyLoop(ctx, cfg.DailyAtHour, cfg.DailyAtMin, j.Daily, cfg.Logger)
	}()
	if j.Notify != nil {
		interval := cfg.NotifyInterval
		if interval <= 0 {
			interval = defaultNotifyInterval
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			tickLoop(ctx, interval, j.Notify, cfg.Logger, "notify")
		}()
	}
}

// tickLoop runs job now and then every interval. A slow run delays the next
// rather than overlapping it: the ticker drops ticks a busy receiver misses.
func tickLoop(ctx context.Context, interval time.Duration, job func(context.Context), logger *slog.Logger, name string) {
	runSafe(ctx, job, logger, name)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			runSafe(ctx, job, logger, name)
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
