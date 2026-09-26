package notify

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/kareltilcer/ws-tilcer-status/backend/internal/crash"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/feedback"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/monitoring"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/sites"
)

func TestQualifies(t *testing.T) {
	cases := []struct {
		level, env string
		want       bool
	}{
		{"error", "prod", true},
		{"fatal", "prod", true},
		{"error", "production", true},
		{"error", "PROD", true},
		{"error", " Prod ", true},
		{"error", "", true}, // a client that never set one is most likely the deployed app
		{"error", "dev", false},
		{"error", "staging", false},
		{"fatal", "development", false},
		{"warning", "prod", false},
		{"warning", "", false},
	}
	for _, c := range cases {
		if got := qualifies(c.level, c.env); got != c.want {
			t.Errorf("qualifies(%q, %q) = %t, want %t", c.level, c.env, got, c.want)
		}
	}
}

func TestBackoff(t *testing.T) {
	want := []time.Duration{time.Minute, 2 * time.Minute, 5 * time.Minute, 15 * time.Minute, 30 * time.Minute, time.Hour, time.Hour, time.Hour}
	for i, w := range want {
		if got := backoff(i + 1); got != w {
			t.Errorf("backoff(%d) = %s, want %s", i+1, got, w)
		}
	}
	if backoff(0) != time.Minute {
		t.Errorf("backoff(0) must not index out of the schedule")
	}
}

// --- crashes ------------------------------------------------------------------

// TestNewCrashIsAnnouncedOnce: the first qualifying event of a group is news; the
// second is the 2nd occurrence of the same news.
func TestNewCrashIsAnnouncedOnce(t *testing.T) {
	h := newHarness(t)
	h.enable("karel@example.test")
	g := h.seedGroup("home", crash.StatusOpen)

	h.crash(newCrash(g, t0))
	h.crash(repeat(g, "prod", t0.Add(time.Minute)))
	if got := h.queued(); !equalKinds(got, KindCrashNew) {
		t.Fatalf("queued %v, want exactly one crash_new", got)
	}
}

// TestGroupFirstSeenInDevIsNewsOnItsFirstProdError: the fingerprint does not
// include the environment, so a crash a developer hit first is the same group
// when it reaches production — and that is the moment it becomes news.
func TestGroupFirstSeenInDevIsNewsOnItsFirstProdError(t *testing.T) {
	h := newHarness(t)
	h.enable("karel@example.test")
	g := h.seedGroup("home", crash.StatusOpen)

	dev := newCrash(g, t0)
	dev.Environment = "dev"
	h.crash(dev)
	if got := h.queued(); len(got) != 0 {
		t.Fatalf("a dev-only crash queued %v", got)
	}
	h.crash(repeat(g, "prod", t0.Add(time.Minute)))
	if got := h.queued(); !equalKinds(got, KindCrashNew) {
		t.Fatalf("queued %v, want crash_new on the first prod error", got)
	}
}

func TestWarningsNeverNotify(t *testing.T) {
	h := newHarness(t)
	h.enable("karel@example.test")
	g := h.seedGroup("home", crash.StatusOpen)
	s := newCrash(g, t0)
	s.Level = crash.LevelWarning
	h.crash(s)
	if got := h.queued(); len(got) != 0 {
		t.Fatalf("a warning queued %v", got)
	}
	// …and it did not consume the announcement: the group's first error still is.
	h.crash(repeat(g, "prod", t0.Add(time.Minute)))
	if got := h.queued(); !equalKinds(got, KindCrashNew) {
		t.Fatalf("queued %v, want crash_new", got)
	}
}

// TestRegressionReopenAnnouncesACrashThatCameBack: resolved, then an event
// reopens the group — "came back", not "new".
func TestRegressionReopenAnnouncesACrashThatCameBack(t *testing.T) {
	h := newHarness(t)
	h.enable("karel@example.test")
	g := h.seedGroup("home", crash.StatusOpen)
	h.crash(newCrash(g, t0))

	back := repeat(g, "prod", t0.Add(time.Hour))
	back.Reopened = true
	h.crash(back)
	if got := h.queued(); !equalKinds(got, KindCrashNew, KindCrashRegression) {
		t.Fatalf("queued %v, want crash_new then crash_regression", got)
	}
	// A further repeat of the reopened group is not news again.
	h.crash(repeat(g, "prod", t0.Add(2*time.Hour)))
	if got := h.queued(); len(got) != 2 {
		t.Fatalf("a repeat after the regression queued more: %v", got)
	}
}

// TestReopenByADevEventArmsTheNextProdError: the reopen and the qualifying event
// need not be the same event.
func TestReopenByADevEventArmsTheNextProdError(t *testing.T) {
	h := newHarness(t)
	h.enable("karel@example.test")
	g := h.seedGroup("home", crash.StatusOpen)
	h.crash(newCrash(g, t0))

	devReopen := repeat(g, "dev", t0.Add(time.Hour))
	devReopen.Reopened = true
	h.crash(devReopen)
	if got := h.queued(); len(got) != 1 {
		t.Fatalf("a dev event that reopened the group queued %v", got)
	}
	h.crash(repeat(g, "prod", t0.Add(2*time.Hour)))
	if got := h.queued(); !equalKinds(got, KindCrashNew, KindCrashRegression) {
		t.Fatalf("queued %v, want the regression on the next prod error", got)
	}
}

// TestIgnoredAndManuallyReopenedGroupsStaySilent: an ignored group is never news,
// and a group Karel reopened by hand through triage is not armed by that.
func TestIgnoredAndManuallyReopenedGroupsStaySilent(t *testing.T) {
	h := newHarness(t)
	h.enable("karel@example.test")

	ignored := h.seedGroup("home", crash.StatusIgnored)
	s := repeat(ignored, "prod", t0)
	s.GroupStatus = crash.StatusIgnored
	h.crash(s)

	g := h.seedGroup("home", crash.StatusOpen)
	h.crash(newCrash(g, t0))
	// Triage PATCHes it resolved and back to open — no hook runs — and it recurs.
	h.crash(repeat(g, "prod", t0.Add(time.Hour)))

	if got := h.queued(); !equalKinds(got, KindCrashNew) {
		t.Fatalf("queued %v, want only the open group's first announcement", got)
	}
}

// TestATriagedGroupsFirstProdErrorIsConsumed: a group that is not open when its
// first production error arrives — ignored, or resolved while regressions do not
// reopen it — is one Karel has already triaged. That error consumes the
// announcement, as the upgrade seed does for every group that has had one, so
// setting the group back to open by hand does not make its next error a "new
// crash".
func TestATriagedGroupsFirstProdErrorIsConsumed(t *testing.T) {
	for _, status := range []string{crash.StatusIgnored, crash.StatusResolved} {
		t.Run(status, func(t *testing.T) {
			h := newHarness(t)
			h.enable("karel@example.test")
			g := h.seedGroup("home", status)

			s := repeat(g, "prod", t0)
			s.GroupStatus = status
			h.crash(s)
			if armed, announced, err := loadCrashState(context.Background(), h.db, g); err != nil || armed || !announced {
				t.Fatalf("after a prod error while %s: armed %t announced %t (%v), want disarmed and announced",
					status, armed, announced, err)
			}

			// Triage sets it back to open — no hook runs — and it recurs.
			h.crash(repeat(g, "prod", t0.Add(time.Hour)))
			if got := h.queued(); len(got) != 0 {
				t.Fatalf("queued %v for a group Karel had %s", got, status)
			}
		})
	}
}

// TestEventsWhileOffAreNeverSentLater: "off" consumes the news. Switching on
// must not deliver the backlog as if it had just happened.
func TestEventsWhileOffAreNeverSentLater(t *testing.T) {
	h := newHarness(t)
	g := h.seedGroup("home", crash.StatusOpen)
	h.crash(newCrash(g, t0))
	h.enable("karel@example.test")
	h.crash(repeat(g, "prod", t0.Add(time.Minute)))
	if got := h.queued(); len(got) != 0 {
		t.Fatalf("queued %v for a group announced while off", got)
	}
	armed, announced, err := loadCrashState(context.Background(), h.db, g)
	if err != nil || armed || !announced {
		t.Fatalf("state = armed %t announced %t (%v), want disarmed and announced", armed, announced, err)
	}
}

func TestMutesAndTogglesStopQueueing(t *testing.T) {
	h := newHarness(t)
	h.setSettings(settings{Enabled: true, Recipients: []string{"karel@example.test"},
		OnCrash: false, OnFeedback: true, OnDowntime: true})
	h.crash(newCrash(h.seedGroup("home", crash.StatusOpen), t0))
	if got := h.queued(); len(got) != 0 {
		t.Fatalf("crash toggle off queued %v", got)
	}

	h.enable("karel@example.test")
	if err := setMuted(context.Background(), h.db, "home", true, ts(t0)); err != nil {
		t.Fatal(err)
	}
	h.crash(newCrash(h.seedGroup("home", crash.StatusOpen), t0))
	h.report(feedback.ReportSignal{SiteID: "home", Ref: "R-AAAA", Kind: "bug", Message: "x", At: t0})
	if got := h.queued(); len(got) != 0 {
		t.Fatalf("a muted site queued %v", got)
	}
	h.report(feedback.ReportSignal{SiteID: "fin", Ref: "R-BBBB", Kind: "bug", Message: "x", At: t0})
	if got := h.queued(); !equalKinds(got, KindFeedback) {
		t.Fatalf("an unmuted site queued %v, want its report", got)
	}
}

// TestNoProviderQueuesNothingButKeepsTheState: the state machine runs without a
// provider, so a key configured later does not mail old groups as new.
func TestNoProviderQueuesNothingButKeepsTheState(t *testing.T) {
	h := newHarnessWith(t, testConfig(), false)
	h.enable("karel@example.test")
	g := h.seedGroup("home", crash.StatusOpen)
	h.crash(newCrash(g, t0))
	h.report(feedback.ReportSignal{SiteID: "home", Ref: "R-AAAA", Kind: "bug", Message: "x", At: t0})
	if got := h.queued(); len(got) != 0 {
		t.Fatalf("queued %v with no provider", got)
	}
	if armed, _, _ := loadCrashState(context.Background(), h.db, g); armed {
		t.Fatal("the group is still armed: a provider switched on later would mail it as new")
	}
}

// --- downtime -----------------------------------------------------------------

func check(ok bool, color sites.Color, at time.Time) monitoring.CheckSignal {
	c := monitoring.CheckSignal{SiteID: "home", URL: "https://home.example.test", OK: ok, Color: color, At: at}
	if !ok {
		code := 502
		c.StatusCode = &code
	}
	return c
}

// TestDownThenBackUp: the poller's debounce sequence produces exactly one "down"
// (on the check that turns the site red) and one "back up".
func TestDownThenBackUp(t *testing.T) {
	h := newHarness(t)
	h.enable("karel@example.test")
	h.check(check(true, sites.Green, t0))
	h.check(check(false, sites.Green, t0.Add(5*time.Minute))) // debounce holds green
	h.check(check(false, sites.Red, t0.Add(10*time.Minute)))
	h.check(check(false, sites.Red, t0.Add(15*time.Minute)))
	h.check(check(true, sites.Green, t0.Add(20*time.Minute)))
	if got := h.queued(); !equalKinds(got, KindSiteDown, KindSiteRecovered) {
		t.Fatalf("queued %v, want site_down then site_recovered", got)
	}
	if n := h.count(`SELECT COUNT(*) FROM notify_site_state`); n != 0 {
		t.Fatalf("the downtime memory outlived the recovery (%d rows)", n)
	}
}

// TestAResetIsNotARecovery: editing a red site's URL resets its color to unknown
// without anything having recovered. Only a passing check is "back up", and the
// failures after the reset do not announce the same outage twice.
func TestAResetIsNotARecovery(t *testing.T) {
	h := newHarness(t)
	h.enable("karel@example.test")
	h.check(check(false, sites.Red, t0))
	h.check(check(false, sites.Unknown, t0.Add(5*time.Minute)))
	h.check(check(false, sites.Red, t0.Add(10*time.Minute)))
	if got := h.queued(); !equalKinds(got, KindSiteDown) {
		t.Fatalf("queued %v, want one site_down and no recovery", got)
	}
	h.check(check(true, sites.Green, t0.Add(15*time.Minute)))
	if got := h.queued(); !equalKinds(got, KindSiteDown, KindSiteRecovered) {
		t.Fatalf("queued %v, want the recovery on the passing check", got)
	}
}

// TestAnOutageThatBeganWhileOffStaysSilent: no "down" went out, so "back up"
// would be news about nothing.
func TestAnOutageThatBeganWhileOffStaysSilent(t *testing.T) {
	h := newHarness(t)
	h.check(check(false, sites.Red, t0))
	h.enable("karel@example.test")
	h.check(check(true, sites.Green, t0.Add(5*time.Minute)))
	if got := h.queued(); len(got) != 0 {
		t.Fatalf("queued %v for an outage nobody was told about", got)
	}
}

// --- the savepoint --------------------------------------------------------------

func (h *harness) marker() {
	h.t.Helper()
	if _, err := h.db.Exec(`CREATE TABLE marker (x INTEGER)`); err != nil {
		h.t.Fatal(err)
	}
}

// TestAFailedEnqueueKeepsTheTriggeringWrite: a statement of the hook's that fails
// is undone with everything else the hook wrote, and nothing the producer wrote.
func TestAFailedEnqueueKeepsTheTriggeringWrite(t *testing.T) {
	h := newHarness(t)
	h.enable("karel@example.test")
	h.marker()
	if _, err := h.db.Exec(`CREATE TRIGGER boom BEFORE INSERT ON notify_event BEGIN SELECT RAISE(ABORT, 'boom'); END`); err != nil {
		t.Fatal(err)
	}
	g := h.seedGroup("home", crash.StatusOpen)
	err := h.inTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO marker (x) VALUES (1)`); err != nil {
			return err
		}
		return h.mod.notifier.CrashRecorded(context.Background(), tx, newCrash(g, t0))
	})
	if err != nil {
		t.Fatalf("the hook failed the producer's transaction: %v", err)
	}
	if n := h.count(`SELECT COUNT(*) FROM marker`); n != 1 {
		t.Fatalf("the producer's write was lost (%d rows)", n)
	}
	// The state change the hook made before the failed insert went with it, so
	// the group's next qualifying event tries again.
	if armed, announced, _ := loadCrashState(context.Background(), h.db, g); !armed || announced {
		t.Fatalf("state = armed %t announced %t, want it untouched", armed, announced)
	}
}

// TestAUniqueFailureInsideTheHookNeverReachesTheProducer: feedback's insert loop
// reads ANY "UNIQUE constraint failed" as a ref collision and retries; a notify
// failure worded that way must not come back out of the hook.
func TestAUniqueFailureInsideTheHookNeverReachesTheProducer(t *testing.T) {
	h := newHarness(t)
	h.enable("karel@example.test")
	if _, err := h.db.Exec(`CREATE TRIGGER boom BEFORE INSERT ON notify_event BEGIN SELECT RAISE(ABORT, 'UNIQUE constraint failed: notify_event.fake'); END`); err != nil {
		t.Fatal(err)
	}
	err := h.inTx(func(tx *sql.Tx) error {
		return h.mod.notifier.ReportSubmitted(context.Background(), tx, feedback.ReportSignal{SiteID: "home", Ref: "R-AAAA", Kind: "bug", Message: "x", At: t0})
	})
	if err != nil {
		t.Fatalf("the hook returned %v, want the failure swallowed", err)
	}
}

// TestAnUnusableTransactionIsReported: when SQLite has rolled the whole
// transaction back, the hook cannot pretend otherwise — its error is the
// producer's cue to return rather than run a statement in autocommit.
func TestAnUnusableTransactionIsReported(t *testing.T) {
	h := newHarness(t)
	h.enable("karel@example.test")
	h.marker()
	if _, err := h.db.Exec(`CREATE TRIGGER boom BEFORE INSERT ON notify_event BEGIN SELECT RAISE(ROLLBACK, 'gone'); END`); err != nil {
		t.Fatal(err)
	}
	g := h.seedGroup("home", crash.StatusOpen)
	err := h.inTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO marker (x) VALUES (1)`); err != nil {
			return err
		}
		return h.mod.notifier.CrashRecorded(context.Background(), tx, newCrash(g, t0))
	})
	if err == nil {
		t.Fatal("the hook reported success on a transaction SQLite had rolled back")
	}
	if n := h.count(`SELECT COUNT(*) FROM marker`); n != 0 {
		t.Fatalf("a write survived a rolled-back transaction (%d rows)", n)
	}
}

// TestAPanickingHookIsContained: a panic inside the savepoint undoes the hook's
// writes and is not re-raised into the producer.
func TestAPanickingHookIsContained(t *testing.T) {
	h := newHarness(t)
	h.marker()
	err := h.inTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO marker (x) VALUES (1)`); err != nil {
			return err
		}
		return h.mod.notifier.guard(context.Background(), tx, "test", func() error {
			if _, err := tx.Exec(`INSERT INTO marker (x) VALUES (2)`); err != nil {
				return err
			}
			panic("hook exploded")
		})
	})
	if err != nil {
		t.Fatalf("guard returned %v after a contained panic", err)
	}
	var xs []int
	rows, _ := h.db.Query(`SELECT x FROM marker ORDER BY x`)
	for rows.Next() {
		var x int
		_ = rows.Scan(&x)
		xs = append(xs, x)
	}
	rows.Close()
	if len(xs) != 1 || xs[0] != 1 {
		t.Fatalf("marker = %v, want only the producer's row", xs)
	}
}

// TestAnErrorFromTheHookBodyIsSwallowed: the ordinary failure — a query that
// errors — is logged, undone, and not the producer's problem.
func TestAnErrorFromTheHookBodyIsSwallowed(t *testing.T) {
	h := newHarness(t)
	err := h.inTx(func(tx *sql.Tx) error {
		return h.mod.notifier.guard(context.Background(), tx, "test", func() error { return errors.New("nope") })
	})
	if err != nil {
		t.Fatalf("guard returned %v", err)
	}
}
