package sites

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

// countingReports is a stub ReportCounter, standing in for the feedback module.
type countingReports struct {
	counts map[string]int
	err    error
	calls  int
}

func (c *countingReports) ReportCounts(_ context.Context, siteIDs []string) (map[string]int, error) {
	c.calls++
	if c.err != nil {
		return nil, c.err
	}
	out := map[string]int{}
	for _, id := range siteIDs {
		out[id] = c.counts[id]
	}
	return out, nil
}

// TestOpenReportsIsNullWithoutACounter — V3-D53. Zero is a claim that there is
// nothing to read; null is the truth, which is that nobody asked.
func TestOpenReportsIsNullWithoutACounter(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	mustCreate(t, s, "home", "Home", "https://home.tilcer.cz/readyz", true)

	board, err := s.List(ctx, now)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(board) != 1 || board[0].OpenReports != nil {
		t.Fatalf("open_reports = %v, want null when no counter is registered", board[0].OpenReports)
	}
	one, err := s.Get(ctx, "home", now)
	if err != nil || one == nil {
		t.Fatalf("get: %v", err)
	}
	if one.OpenReports != nil {
		t.Fatalf("open_reports = %v on the detail, want null", *one.OpenReports)
	}
}

// TestOpenReportsCountsWhenRegistered: with a counter injected, a site with no
// reports carries a zero badge rather than an absent one.
func TestOpenReportsCountsWhenRegistered(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	mustCreate(t, s, "home", "Home", "https://home.tilcer.cz/readyz", true)
	mustCreate(t, s, "fin", "Fin", "https://fin.tilcer.cz/readyz", true)
	s.reports = &countingReports{counts: map[string]int{"home": 3}}

	board, err := s.List(ctx, now)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	got := map[string]*int{}
	for _, site := range board {
		got[site.ID] = site.OpenReports
	}
	if got["home"] == nil || *got["home"] != 3 {
		t.Fatalf("home open_reports = %v, want 3", got["home"])
	}
	if got["fin"] == nil || *got["fin"] != 0 {
		t.Fatalf("fin open_reports = %v, want 0 (a registered counter that found none)", got["fin"])
	}
}

// TestBoardSurvivesAFailingCounter: the board is a monitoring surface. A failed
// badge count degrades to no badge rather than failing the board.
func TestBoardSurvivesAFailingCounter(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, "home", "Home", "https://home.tilcer.cz/readyz", true)
	s.reports = &countingReports{err: errors.New("feedback store is unhappy")}

	board, err := s.List(context.Background(), time.Now().UTC())
	if err != nil {
		t.Fatalf("a failing report counter must not fail the board: %v", err)
	}
	if len(board) != 1 || board[0].OpenReports != nil {
		t.Fatalf("open_reports = %v, want null when the count could not be read", board[0].OpenReports)
	}
}

// TestComputeColorUntouchedByReports is named for the fact it protects: reports
// are counted on the board, never coloured into it.
//
// ⚠ Someone reading "open_reports" next to cached_color in the same handler will
// eventually be tempted to wire them together. A person saying "this is
// confusing" must not make a site look degraded beside a real outage.
func TestComputeColorUntouchedByReports(t *testing.T) {
	// ComputeColor's inputs are the whole contract: a report count is not among
	// them, and cannot become one without this failing to compile.
	green := ComputeColor(ColorInputs{
		MonitorEnabled: true,
		FailStreak:     0,
		RedThreshold:   2,
		HasAnyCheck:    true,
		PriorColor:     Green,
	})
	if green != Green {
		t.Fatalf("color = %s, want green", green)
	}

	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	mustCreate(t, s, "home", "Home", "https://home.tilcer.cz/readyz", true)
	if _, err := s.db.ExecContext(ctx,
		`UPDATE site SET last_checked_at = ?, last_ok = 1, fail_streak = 0, cached_color = 'green' WHERE id = 'home'`,
		time.Now().UTC().Format("2006-01-02T15:04:05.000000000Z07:00")); err != nil {
		t.Fatalf("seed check: %v", err)
	}
	before, err := s.Get(ctx, "home", now)
	if err != nil || before == nil {
		t.Fatalf("get: %v", err)
	}

	s.reports = &countingReports{counts: map[string]int{"home": 99}}
	after, err := s.Get(ctx, "home", now)
	if err != nil || after == nil {
		t.Fatalf("get: %v", err)
	}
	if after.Color != before.Color {
		t.Fatalf("99 reports changed the colour from %s to %s — reports must never colour the board", before.Color, after.Color)
	}
	if after.OpenReports == nil || *after.OpenReports != 99 {
		t.Fatalf("open_reports = %v, want 99 — counted, but not coloured", after.OpenReports)
	}
}

// recordingPurger stands in for the feedback module's object half.
type recordingPurger struct {
	keys      []string
	collected []string
	deleted   []string
	// openTx records whether the collect call ran inside a transaction, which is
	// exactly where it must run: the keys have to be the ones the cascade is about
	// to orphan.
	sawTx bool
}

func (p *recordingPurger) SiteObjectKeys(_ context.Context, tx *sql.Tx, _ string) ([]string, error) {
	p.sawTx = tx != nil
	p.collected = append(p.collected, "collect")
	return p.keys, nil
}

func (p *recordingPurger) DeleteObjects(_ context.Context, keys []string) {
	p.deleted = append(p.deleted, keys...)
}

// TestDeleteSiteCollectsInsideTheTransactionThenDeletes — FR-22's normative
// order. Collecting after the commit would find the rows already gone; deleting
// before it would destroy the attachments of a site that still exists if the
// commit failed.
func TestDeleteSiteCollectsInsideTheTransactionThenDeletes(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	mustCreate(t, s, "home", "Home", "https://home.tilcer.cz/readyz", true)

	p := &recordingPurger{keys: []string{"feedback/home/R-7QK2/0-a.png", "feedback/home/R-7QK2/1-b.mp4"}}
	keys, ok, err := s.Delete(ctx, "home", p.SiteObjectKeys)
	if err != nil || !ok {
		t.Fatalf("delete: %v ok=%v", err, ok)
	}
	if !p.sawTx {
		t.Fatal("the object keys were not collected inside the deleting transaction")
	}
	if len(keys) != 2 {
		t.Fatalf("collected %d keys, want 2", len(keys))
	}
	if gone, _ := s.Get(ctx, "home", time.Now().UTC()); gone != nil {
		t.Fatal("the site should be gone")
	}

	// An unknown site collects nothing and reports not-found.
	keys, ok, err = s.Delete(ctx, "nope", p.SiteObjectKeys)
	if err != nil || ok || keys != nil {
		t.Fatalf("deleting an unknown site = keys %v ok=%v err=%v", keys, ok, err)
	}
}
