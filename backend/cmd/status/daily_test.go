package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

// recorder stands in for all four daily steps and records the order they ran in.
type recorder struct {
	steps     []string
	rollupErr error
	purgeErr  error
	pruneErr  error
}

func (r *recorder) RunDaily(context.Context, time.Time) error {
	r.steps = append(r.steps, "rollup")
	return r.rollupErr
}

func (r *recorder) Purge(context.Context, time.Time) error {
	r.steps = append(r.steps, "purge")
	return r.purgeErr
}

func (r *recorder) Prune(context.Context, time.Time) error {
	r.steps = append(r.steps, "prune")
	return r.pruneErr
}

func (r *recorder) Sweep(context.Context, time.Time) {
	r.steps = append(r.steps, "sweep")
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func runSteps(t *testing.T, r *recorder) []string {
	t.Helper()
	runDailyJob(context.Background(), discardLogger(), r, r, r, r, time.Date(2026, 9, 2, 0, 15, 0, 0, time.UTC))
	return r.steps
}

// TestDailyJobOrder — PRD §V3-11: "The daily job runs rollup → purge → sweep, in
// that order", with the notification prune between the purge and the sweep.
//
// ⚠ The sweep is last because it is the only step that talks to the network; a
// bucket that will not answer must not delay the steps that keep the database
// honest.
func TestDailyJobOrder(t *testing.T) {
	got := runSteps(t, &recorder{})
	want := []string{"rollup", "purge", "prune", "sweep"}
	if len(got) != len(want) {
		t.Fatalf("daily job ran %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("daily job ran %v, want %v", got, want)
		}
	}
}

// TestDailyJobSkipsOnlyThePurgeAfterAFailedRollup: a purge that runs on
// un-aggregated checks deletes them permanently, leaving an uptime gap nothing
// can refill — so the purge, and only the purge, is skipped.
//
// ⚠ The prune and the sweep still run. Neither depends on the rollup, and a
// rollup that keeps failing must not quietly switch off the job that resolves
// unclaimed attachments and collects orphaned objects from a bucket that is paid
// for and deliberately not backed up.
func TestDailyJobSkipsOnlyThePurgeAfterAFailedRollup(t *testing.T) {
	got := runSteps(t, &recorder{rollupErr: errors.New("rollup exploded")})
	if len(got) != 3 || got[0] != "rollup" || got[1] != "prune" || got[2] != "sweep" {
		t.Fatalf("daily job ran %v after a failed rollup, want the rollup, the prune, then the sweep", got)
	}
}

// TestDailyJobSweepsAfterAFailedPurge: the purge reports its own failure and the
// sweep still runs — an orphaned object is not made safer by skipping the job
// that collects it.
func TestDailyJobSweepsAfterAFailedPurge(t *testing.T) {
	got := runSteps(t, &recorder{purgeErr: errors.New("purge exploded")})
	if len(got) != 4 || got[2] != "prune" || got[3] != "sweep" {
		t.Fatalf("daily job ran %v after a failed purge, want the prune and the sweep to still run", got)
	}
}

// TestDailyJobSweepsAfterAFailedPrune: a notification-history problem is the
// prune's to report; it must not cost the bucket its collector.
func TestDailyJobSweepsAfterAFailedPrune(t *testing.T) {
	got := runSteps(t, &recorder{pruneErr: errors.New("prune exploded")})
	if len(got) != 4 || got[3] != "sweep" {
		t.Fatalf("daily job ran %v after a failed prune, want the sweep to still run", got)
	}
}
