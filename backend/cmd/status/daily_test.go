package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

// recorder stands in for all three daily steps and records the order they ran in.
type recorder struct {
	steps     []string
	rollupErr error
	purgeErr  error
}

func (r *recorder) RunDaily(context.Context, time.Time) error {
	r.steps = append(r.steps, "rollup")
	return r.rollupErr
}

func (r *recorder) Purge(context.Context, time.Time) error {
	r.steps = append(r.steps, "purge")
	return r.purgeErr
}

func (r *recorder) Sweep(context.Context, time.Time) {
	r.steps = append(r.steps, "sweep")
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func runSteps(t *testing.T, r *recorder) []string {
	t.Helper()
	runDailyJob(context.Background(), discardLogger(), r, r, r, time.Date(2026, 9, 2, 0, 15, 0, 0, time.UTC))
	return r.steps
}

// TestDailyJobOrder — PRD §V3-11: "The daily job runs rollup → purge → sweep, in
// that order."
//
// ⚠ The sweep is last because it is the only step that talks to the network; a
// bucket that will not answer must not delay the two that keep the database
// honest.
func TestDailyJobOrder(t *testing.T) {
	got := runSteps(t, &recorder{})
	want := []string{"rollup", "purge", "sweep"}
	if len(got) != len(want) {
		t.Fatalf("daily job ran %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("daily job ran %v, want %v", got, want)
		}
	}
}

// TestDailyJobSkipsEverythingAfterAFailedRollup: a purge that runs on
// un-aggregated checks deletes them permanently, leaving an uptime gap nothing
// can refill.
func TestDailyJobSkipsEverythingAfterAFailedRollup(t *testing.T) {
	got := runSteps(t, &recorder{rollupErr: errors.New("rollup exploded")})
	if len(got) != 1 || got[0] != "rollup" {
		t.Fatalf("daily job ran %v after a failed rollup, want the rollup alone", got)
	}
}

// TestDailyJobSweepsAfterAFailedPurge: the purge reports its own failure and the
// sweep still runs — an orphaned object is not made safer by skipping the job
// that collects it.
func TestDailyJobSweepsAfterAFailedPurge(t *testing.T) {
	got := runSteps(t, &recorder{purgeErr: errors.New("purge exploded")})
	if len(got) != 3 || got[2] != "sweep" {
		t.Fatalf("daily job ran %v after a failed purge, want the sweep to still run", got)
	}
}
