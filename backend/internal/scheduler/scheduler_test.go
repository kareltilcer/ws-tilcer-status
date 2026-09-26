package scheduler

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// start runs the scheduler with an idle poller and a daily job that never comes
// due, and returns a stop that cancels and joins every loop.
func start(t *testing.T, cfg Config, notify func(context.Context)) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	cfg.CheckInterval = time.Hour
	cfg.Logger = discardLogger()
	now := time.Now().UTC()
	// The daily fire time is a minute behind the clock, so it is ~a day away.
	cfg.DailyAtHour, cfg.DailyAtMin = now.Add(-time.Minute).Hour(), now.Add(-time.Minute).Minute()
	Start(ctx, &wg, cfg, Jobs{
		Poll:   func(context.Context) {},
		Daily:  func(context.Context) { t.Error("the daily job ran") },
		Notify: notify,
	})
	var once sync.Once
	stop = func() { once.Do(func() { cancel(); wg.Wait() }) }
	t.Cleanup(stop)
	return stop
}

// TestNotifyRunsAtStartAndEveryInterval: the worker runs once at boot — a digest
// that came due while the service was down goes out — and then on its ticker.
func TestNotifyRunsAtStartAndEveryInterval(t *testing.T) {
	var runs atomic.Int32
	three := make(chan struct{})
	start(t, Config{NotifyInterval: 5 * time.Millisecond}, func(context.Context) {
		if runs.Add(1) == 3 {
			close(three)
		}
	})
	select {
	case <-three:
	case <-time.After(5 * time.Second):
		t.Fatalf("the notify job ran %d times in 5s, want at least 3", runs.Load())
	}
}

// TestNotifyDefaultsAZeroInterval: an unset interval falls back to the default
// rather than reaching time.NewTicker, which panics on zero — inside a loop
// goroutine, where it would take the service down.
func TestNotifyDefaultsAZeroInterval(t *testing.T) {
	ran := make(chan struct{}, 1)
	stop := start(t, Config{}, func(context.Context) {
		select {
		case ran <- struct{}{}:
		default:
		}
	})
	select {
	case <-ran:
	case <-time.After(5 * time.Second):
		t.Fatal("the notify job never ran")
	}
	stop() // joins the loop: a ticker panic would have crashed the test binary by now
}
