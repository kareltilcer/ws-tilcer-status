package apitest

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestNotificationsEndToEnd drives the real router: a crash ingested through the
// public endpoint, a report submitted through the widget routes, a regression
// reopened by triage and a fresh event — each reaching the fake provider as a
// digest whose links point at the dashboard.
func TestNotificationsEndToEnd(t *testing.T) {
	a := newAPI(t, 100, 1000)
	ctx := context.Background()
	// The worker's clock runs ahead of the wall clock the handlers stamp events
	// with, so each pass is past its window without the test sleeping.
	later := func(d time.Duration) time.Time { return time.Now().UTC().Add(d) }

	st, body := a.do(t, "GET", "/api/meta", nil, nil)
	if st != 200 || !strings.Contains(string(body), `"notifications_enabled":true`) {
		t.Fatalf("meta: %d %s", st, body)
	}

	st, body = a.do(t, "POST", "/api/sites", map[string]any{"id": "home", "name": "Home"}, nil)
	if st != 201 {
		t.Fatalf("create site: %d %s", st, body)
	}
	var site struct {
		IngestKey string `json:"ingest_key"`
	}
	mustJSON(t, body, &site)
	ingest := map[string]string{"X-Ingest-Key": site.IngestKey}

	st, body = a.do(t, "PUT", "/api/notifications/settings", map[string]any{
		"enabled": true, "recipients": []string{"karel@example.test"},
		"events": map[string]bool{"crash": true, "feedback": true, "downtime": true},
	}, nil)
	if st != 200 {
		t.Fatalf("enable notifications: %d %s", st, body)
	}

	// --- a new production crash ---
	crash := map[string]any{"message": "TypeError: boom", "level": "error", "environment": "prod"}
	st, body = a.do(t, "POST", "/api/ingest/home", crash, ingest)
	if st != 202 {
		t.Fatalf("ingest: %d %s", st, body)
	}
	var accepted struct {
		GroupID int64 `json:"group_id"`
	}
	mustJSON(t, body, &accepted)
	a.notify.Worker().RunOnce(ctx, later(2*time.Minute+time.Second))
	sent := a.mail.Sent()
	if len(sent) != 1 {
		t.Fatalf("sent %d emails after the first crash, want 1", len(sent))
	}
	if sent[0].Subject != "[status] Home: new crash: TypeError: boom" ||
		!strings.Contains(sent[0].Text, testPublicURL+"/crashes/"+itoa(accepted.GroupID)) {
		t.Fatalf("crash email: %q\n%s", sent[0].Subject, sent[0].Text)
	}

	// --- the same crash again is not news ---
	if st, body := a.do(t, "POST", "/api/ingest/home", crash, ingest); st != 202 {
		t.Fatalf("repeat ingest: %d %s", st, body)
	}
	a.notify.Worker().RunOnce(ctx, later(5*time.Minute))
	if n := len(a.mail.Sent()); n != 1 {
		t.Fatalf("a repeat of an announced crash sent email (%d total)", n)
	}

	// --- resolved, then back ---
	if st, body := a.do(t, "PATCH", "/api/crashes/"+itoa(accepted.GroupID), map[string]any{"status": "resolved"}, nil); st != 200 {
		t.Fatalf("resolve: %d %s", st, body)
	}
	if st, body := a.do(t, "POST", "/api/ingest/home", crash, ingest); st != 202 {
		t.Fatalf("regression ingest: %d %s", st, body)
	}
	a.notify.Worker().RunOnce(ctx, later(8*time.Minute))
	sent = a.mail.Sent()
	if len(sent) != 2 || sent[1].Subject != "[status] Home: crash came back: TypeError: boom" {
		t.Fatalf("after the regression: %d emails, last %q", len(sent), sent[len(sent)-1].Subject)
	}

	// --- a dev crash is not news ---
	dev := map[string]any{"message": "ReferenceError: dev only", "level": "fatal", "environment": "dev"}
	if st, body := a.do(t, "POST", "/api/ingest/home", dev, ingest); st != 202 {
		t.Fatalf("dev ingest: %d %s", st, body)
	}

	// --- a report through the widget ---
	ref := a.submitReport(t, "home")
	a.notify.Worker().RunOnce(ctx, later(11*time.Minute))
	sent = a.mail.Sent()
	if len(sent) != 3 {
		t.Fatalf("after the report: %d emails, want 3", len(sent))
	}
	if sent[2].Subject != "[status] Home: new bug report "+ref ||
		!strings.Contains(sent[2].Text, testPublicURL+"/reports/"+ref) ||
		strings.Contains(sent[2].Text, "dev only") {
		t.Fatalf("report email: %q\n%s", sent[2].Subject, sent[2].Text)
	}

	st, body = a.do(t, "GET", "/api/notifications/deliveries", nil, nil)
	if st != 200 || strings.Count(string(body), `"state":"sent"`) != 3 {
		t.Fatalf("deliveries: %d %s", st, body)
	}
}

// TestANotifyFailureCannotLookLikeARefCollision: feedback's insert loop retries
// on any UNIQUE failure, reading it as two reports minting the same ref. A
// notify failure worded that way — from a trigger here — must neither fail the
// report nor file it twice.
func TestANotifyFailureCannotLookLikeARefCollision(t *testing.T) {
	a := newAPI(t, 100, 1000)
	if st, body := a.do(t, "POST", "/api/sites", map[string]any{"id": "home", "name": "Home"}, nil); st != 201 {
		t.Fatalf("create site: %d %s", st, body)
	}
	if st, body := a.do(t, "PUT", "/api/notifications/settings", map[string]any{
		"enabled": true, "recipients": []string{"karel@example.test"},
		"events": map[string]bool{"crash": true, "feedback": true, "downtime": true},
	}, nil); st != 200 {
		t.Fatalf("enable: %d %s", st, body)
	}
	if _, err := a.db.Exec(`CREATE TRIGGER boom BEFORE INSERT ON notify_event
		BEGIN SELECT RAISE(ABORT, 'UNIQUE constraint failed: notify_event.fake'); END`); err != nil {
		t.Fatal(err)
	}
	a.submitReport(t, "home")
	var n int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM feedback_report`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("%d reports (%v), want exactly one", n, err)
	}
}

// submitReport enables feedback for a site and files one report through the
// widget routes, returning its ref.
func (a *api) submitReport(t *testing.T, siteID string) string {
	t.Helper()
	st, body := a.do(t, "PATCH", "/api/sites/"+siteID+"/feedback-config", map[string]any{"enabled": true}, nil)
	if st != 200 {
		t.Fatalf("enable feedback: %d %s", st, body)
	}
	var cfg struct {
		WidgetKey string `json:"widget_key"`
	}
	mustJSON(t, body, &cfg)
	wh := map[string]string{"X-Widget-Key": cfg.WidgetKey}
	st, _, body = a.doOrigin(t, "GET", "/api/ingest/"+siteID+"/feedback/config", widgetOrigin, nil, wh)
	if st != 200 {
		t.Fatalf("widget config: %d %s", st, body)
	}
	var wc struct {
		Ticket *string `json:"ticket"`
	}
	mustJSON(t, body, &wc)
	st, _, body = a.doOrigin(t, "POST", "/api/ingest/"+siteID+"/feedback", widgetOrigin, map[string]any{
		"message": "The board is empty on my phone.", "kind": "bug", "ticket": *wc.Ticket,
	}, wh)
	if st != 202 {
		t.Fatalf("submit: %d %s", st, body)
	}
	var acc struct {
		Ref string `json:"ref"`
	}
	mustJSON(t, body, &acc)
	return acc.Ref
}
