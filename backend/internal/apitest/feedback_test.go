package apitest

import (
	"bytes"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// widgetOrigin is a real second origin: every widget call in this file is
// cross-origin, which is the only way the CORS repair is observable.
const widgetOrigin = "https://home.tilcer.cz"

// TestFeedbackEndToEnd walks the whole path the widget will take, through the
// real router: enable → config → submit → upload → claim → inbox → view → triage
// → delete. v3 ships no integration (V3-D47), so this is the closest an automated
// test gets to the acceptance criterion that ends with "file one by hand".
func TestFeedbackEndToEnd(t *testing.T) {
	a := newAPI(t, 100, 1000)

	// --- a site, and feedback turned on for it ---
	st, body := a.do(t, "POST", "/api/sites", map[string]any{"id": "home", "name": "Home"}, nil)
	if st != 201 {
		t.Fatalf("create site: %d %s", st, body)
	}
	var created struct {
		IngestKey string `json:"ingest_key"`
	}
	mustJSON(t, body, &created)

	st, body = a.do(t, "GET", "/api/sites/home/feedback-config", nil, nil)
	if st != 200 {
		t.Fatalf("config before enabling: %d %s", st, body)
	}
	var off struct {
		Enabled        bool    `json:"enabled"`
		WidgetKeySetAt *string `json:"widget_key_set_at"`
	}
	mustJSON(t, body, &off)
	if off.Enabled || off.WidgetKeySetAt != nil {
		t.Fatalf("a site with no configuration row must read as off: %s", body)
	}

	st, body = a.do(t, "PATCH", "/api/sites/home/feedback-config", map[string]any{"enabled": true}, nil)
	if st != 200 {
		t.Fatalf("enable feedback: %d %s", st, body)
	}
	var enabled struct {
		Enabled   bool   `json:"enabled"`
		WidgetKey string `json:"widget_key"`
	}
	mustJSON(t, body, &enabled)
	if !enabled.Enabled || !strings.HasPrefix(enabled.WidgetKey, "wk_") {
		t.Fatalf("enable returned %s", body)
	}
	widgetKey := enabled.WidgetKey
	wh := map[string]string{"X-Widget-Key": widgetKey}

	// --- the widget fetches its configuration, cross-origin ---
	st, hdr, body := a.doOrigin(t, "GET", "/api/ingest/home/feedback/config", widgetOrigin, nil, wh)
	if st != 200 {
		t.Fatalf("widget config: %d %s", st, body)
	}
	if got := hdr.Get("Access-Control-Allow-Origin"); got != widgetOrigin {
		t.Fatalf("Access-Control-Allow-Origin = %q, want the echoed origin — without it the browser drops the response", got)
	}
	if hdr.Get("Access-Control-Allow-Credentials") != "" {
		t.Fatal("⚠ the widget routes authenticate by key, not by cookie: Allow-Credentials must never be sent")
	}
	var cfg struct {
		Enabled bool     `json:"enabled"`
		Ticket  *string  `json:"ticket"`
		Accept  []string `json:"accept"`
	}
	mustJSON(t, body, &cfg)
	if !cfg.Enabled || cfg.Ticket == nil || len(cfg.Accept) == 0 {
		t.Fatalf("widget config is incomplete: %s", body)
	}

	// --- submit a report declaring one screenshot ---
	st, _, body = a.doOrigin(t, "POST", "/api/ingest/home/feedback", widgetOrigin, map[string]any{
		"message":        "The board is empty on my phone.",
		"kind":           "bug",
		"ticket":         *cfg.Ticket,
		"reporter_label": "Kája",
		"page_url":       "https://home.tilcer.cz/nakup",
		"files":          []map[string]any{{"content_type": "image/png", "byte_size": 2048}},
	}, wh)
	if st != 202 {
		t.Fatalf("submit: %d %s", st, body)
	}
	var acc struct {
		Ref     string `json:"ref"`
		Uploads []struct {
			AttachmentID int64             `json:"attachment_id"`
			URL          string            `json:"url"`
			Headers      map[string]string `json:"headers"`
		} `json:"uploads"`
	}
	mustJSON(t, body, &acc)
	if len(acc.Uploads) != 1 {
		t.Fatalf("expected one upload slot, got %s", body)
	}
	slot := acc.Uploads[0]
	if slot.Headers["Content-Length"] != "2048" {
		t.Fatalf("the slot must be signed for the declared size, got %q", slot.Headers["Content-Length"])
	}

	// --- the browser PUTs the file straight to the bucket ---
	if err := a.blobs.Upload(slot.URL, slot.Headers["Content-Type"], 2048, bytes.Repeat([]byte("x"), 2048)); err != nil {
		t.Fatalf("upload: %v", err)
	}

	// --- claim ---
	st, _, body = a.doOrigin(t, "POST", "/api/ingest/home/feedback/"+acc.Ref+"/claim", widgetOrigin, nil, wh)
	if st != 200 {
		t.Fatalf("claim: %d %s", st, body)
	}
	var claimed struct {
		Attachments []struct {
			ID       int64  `json:"id"`
			State    string `json:"state"`
			ByteSize *int64 `json:"byte_size"`
		} `json:"attachments"`
	}
	mustJSON(t, body, &claimed)
	if len(claimed.Attachments) != 1 || claimed.Attachments[0].State != "stored" {
		t.Fatalf("claim result: %s", body)
	}
	if claimed.Attachments[0].ByteSize == nil || *claimed.Attachments[0].ByteSize != 2048 {
		t.Fatalf("claim must record the size R2 reported: %s", body)
	}

	// --- the inbox, and the badge on the board ---
	st, body = a.do(t, "GET", "/api/reports", nil, nil)
	if st != 200 {
		t.Fatalf("inbox: %d %s", st, body)
	}
	var inbox struct {
		Items []struct {
			Ref             string  `json:"ref"`
			SiteID          string  `json:"site_id"`
			State           string  `json:"state"`
			ReporterLabel   *string `json:"reporter_label"`
			AttachmentCount int     `json:"attachment_count"`
		} `json:"items"`
	}
	mustJSON(t, body, &inbox)
	if len(inbox.Items) != 1 || inbox.Items[0].Ref != acc.Ref || inbox.Items[0].AttachmentCount != 1 {
		t.Fatalf("inbox: %s", body)
	}
	if inbox.Items[0].ReporterLabel == nil || *inbox.Items[0].ReporterLabel != "Kája" {
		t.Fatalf("the reporter label should be shown as sent: %s", body)
	}
	if got := a.openReports(t, "home"); got == nil || *got != 1 {
		t.Fatalf("open_reports = %v, want 1", got)
	}

	// --- the attachment's view URL is presigned and short-lived ---
	st, body = a.do(t, "GET", "/api/reports/"+acc.Ref+"/attachments/"+strconv.FormatInt(slot.AttachmentID, 10)+"/url", nil, nil)
	if st != 200 {
		t.Fatalf("attachment url: %d %s", st, body)
	}
	var view struct {
		URL         string `json:"url"`
		ExpiresAt   string `json:"expires_at"`
		ContentType string `json:"content_type"`
	}
	mustJSON(t, body, &view)
	if view.URL == "" || view.ContentType != "image/png" || view.ExpiresAt == "" {
		t.Fatalf("view url: %s", body)
	}

	// --- triage: resolving clears the badge but not the colour ---
	before := a.siteColor(t, "home")
	st, body = a.do(t, "PATCH", "/api/reports/"+acc.Ref, map[string]any{"state": "resolved", "internal_note": "fixed in v9"}, nil)
	if st != 200 {
		t.Fatalf("triage: %d %s", st, body)
	}
	if got := a.openReports(t, "home"); got == nil || *got != 0 {
		t.Fatalf("open_reports after resolving = %v, want 0", got)
	}
	if after := a.siteColor(t, "home"); after != before {
		t.Fatalf("triage changed the site colour from %s to %s — reports are counted on the board, never coloured into it", before, after)
	}

	// --- delete: the row goes, then the object ---
	key := a.blobs.Keys()
	if len(key) != 1 {
		t.Fatalf("expected one stored object, got %v", key)
	}
	if st, _ := a.do(t, "DELETE", "/api/reports/"+acc.Ref, nil, nil); st != 204 {
		t.Fatalf("delete report: %d", st)
	}
	if left := a.blobs.Keys(); len(left) != 0 {
		t.Fatalf("the object outlived its report: %v", left)
	}
	if st, _ := a.do(t, "GET", "/api/reports/"+acc.Ref, nil, nil); st != 404 {
		t.Fatalf("deleted report = %d, want 404", st)
	}
}

// TestRotateWidgetKeyLeavesCrashIngestAlone is why the two keys are separate: a
// spammed widget must be revocable without silencing that site's crash reporting.
func TestRotateWidgetKeyLeavesCrashIngestAlone(t *testing.T) {
	a := newAPI(t, 100, 1000)
	_, body := a.do(t, "POST", "/api/sites", map[string]any{"id": "home", "name": "Home"}, nil)
	var created struct {
		IngestKey string `json:"ingest_key"`
	}
	mustJSON(t, body, &created)
	_, body = a.do(t, "PATCH", "/api/sites/home/feedback-config", map[string]any{"enabled": true}, nil)
	var enabled struct {
		WidgetKey string `json:"widget_key"`
	}
	mustJSON(t, body, &enabled)

	st, body := a.do(t, "POST", "/api/sites/home/rotate-widget-key", nil, nil)
	if st != 200 {
		t.Fatalf("rotate widget key: %d %s", st, body)
	}
	var rotated struct {
		WidgetKey string `json:"widget_key"`
	}
	mustJSON(t, body, &rotated)
	if rotated.WidgetKey == enabled.WidgetKey || !strings.HasPrefix(rotated.WidgetKey, "wk_") {
		t.Fatalf("rotate returned %s", body)
	}

	// The old widget key is dead immediately.
	if st, _ := a.do(t, "GET", "/api/ingest/home/feedback/config", nil,
		map[string]string{"X-Widget-Key": enabled.WidgetKey}); st != 401 {
		t.Fatalf("the old widget key still works (%d)", st)
	}
	// ⚠ And the ingest key is untouched: a crash still lands.
	if st, body := a.do(t, "POST", "/api/ingest/home", map[string]any{"message": "boom", "level": "error"},
		map[string]string{"X-Ingest-Key": created.IngestKey}); st != 202 {
		t.Fatalf("crash ingest after a widget-key rotation = %d %s — the two keys must be independent", st, body)
	}
}

// TestDeleteSiteRemovesReportsAndObjects — the SQL cascade removes rows; the
// objects need the second half, issued after the transaction commits.
func TestDeleteSiteRemovesReportsAndObjects(t *testing.T) {
	a := newAPI(t, 100, 1000)
	a.do(t, "POST", "/api/sites", map[string]any{"id": "home", "name": "Home"}, nil)
	_, body := a.do(t, "PATCH", "/api/sites/home/feedback-config", map[string]any{"enabled": true}, nil)
	var enabled struct {
		WidgetKey string `json:"widget_key"`
	}
	mustJSON(t, body, &enabled)
	wh := map[string]string{"X-Widget-Key": enabled.WidgetKey}

	_, body = a.do(t, "GET", "/api/ingest/home/feedback/config", nil, wh)
	var cfg struct {
		Ticket *string `json:"ticket"`
	}
	mustJSON(t, body, &cfg)
	_, body = a.do(t, "POST", "/api/ingest/home/feedback", map[string]any{
		"message": "cannot log in", "ticket": *cfg.Ticket,
		"files": []map[string]any{{"content_type": "image/png", "byte_size": 512}},
	}, wh)
	var acc struct {
		Ref     string `json:"ref"`
		Uploads []struct {
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"uploads"`
	}
	mustJSON(t, body, &acc)
	if err := a.blobs.Upload(acc.Uploads[0].URL, "image/png", 512, bytes.Repeat([]byte("y"), 512)); err != nil {
		t.Fatalf("upload: %v", err)
	}
	a.do(t, "POST", "/api/ingest/home/feedback/"+acc.Ref+"/claim", nil, wh)
	if len(a.blobs.Keys()) != 1 {
		t.Fatalf("expected one stored object, got %v", a.blobs.Keys())
	}

	if st, _ := a.do(t, "DELETE", "/api/sites/home", nil, nil); st != 204 {
		t.Fatalf("delete site: %d", st)
	}
	if left := a.blobs.Keys(); len(left) != 0 {
		t.Fatalf("deleting a site left its attachments in the bucket: %v — the SQL cascade cannot reach R2", left)
	}
	st, body := a.do(t, "GET", "/api/reports", nil, nil)
	if st != 200 {
		t.Fatalf("inbox: %d %s", st, body)
	}
	var inbox struct {
		Items []any `json:"items"`
	}
	mustJSON(t, body, &inbox)
	if len(inbox.Items) != 0 {
		t.Fatalf("the site's reports survived its deletion: %s", body)
	}
}

// TestWidgetPreflight covers the CORS half for the routes v3 adds: the same
// repair PR 1 made for crash ingest has to hold for the three widget paths, and
// only a cross-origin test can see it.
func TestWidgetPreflight(t *testing.T) {
	a := newAPI(t, 100, 1000)
	paths := []string{
		"/api/ingest/home/feedback/config",
		"/api/ingest/home/feedback",
		"/api/ingest/home/feedback/R-7QK2/claim",
	}
	for _, p := range paths {
		st, hdr, _ := a.doOrigin(t, http.MethodOptions, p, widgetOrigin, nil, map[string]string{
			"Access-Control-Request-Method":  "POST",
			"Access-Control-Request-Headers": "content-type,x-widget-key",
		})
		if st != http.StatusNoContent {
			t.Fatalf("preflight %s = %d, want 204", p, st)
		}
		if got := hdr.Get("Access-Control-Allow-Origin"); got != widgetOrigin {
			t.Fatalf("preflight %s echoed origin %q, want %q", p, got, widgetOrigin)
		}
		if !strings.Contains(strings.ToLower(hdr.Get("Access-Control-Allow-Headers")), "x-widget-key") {
			t.Fatalf("preflight %s does not allow X-Widget-Key: %q", p, hdr.Get("Access-Control-Allow-Headers"))
		}
		if hdr.Get("Access-Control-Allow-Credentials") != "" {
			t.Fatalf("preflight %s sent Allow-Credentials", p)
		}

		// A foreign origin gets no allowance at all.
		_, hdr, _ = a.doOrigin(t, http.MethodOptions, p, foreignOrigin, nil, map[string]string{
			"Access-Control-Request-Method": "POST",
		})
		if hdr.Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("preflight %s allowed a foreign origin", p)
		}
	}
}

// openReports reads one site's badge off the board.
func (a *api) openReports(t *testing.T, id string) *int {
	t.Helper()
	st, body := a.do(t, "GET", "/api/sites", nil, nil)
	if st != 200 {
		t.Fatalf("board: %d %s", st, body)
	}
	var board []struct {
		ID          string `json:"id"`
		OpenReports *int   `json:"open_reports"`
	}
	mustJSON(t, body, &board)
	for _, s := range board {
		if s.ID == id {
			return s.OpenReports
		}
	}
	t.Fatalf("site %s is not on the board", id)
	return nil
}

func (a *api) siteColor(t *testing.T, id string) string {
	t.Helper()
	st, body := a.do(t, "GET", "/api/sites/"+id, nil, nil)
	if st != 200 {
		t.Fatalf("site %s: %d %s", id, st, body)
	}
	var out struct {
		Color string `json:"color"`
	}
	mustJSON(t, body, &out)
	return out.Color
}
