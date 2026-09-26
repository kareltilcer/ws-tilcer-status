// Package apitest wires the real router (Mode B dev-bypass, temp DB) and drives
// it over HTTP to cover the PRD §11 acceptance criteria for the sites registry
// and crash pipeline end to end.
package apitest

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/bootstrap"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/crash"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/feedback"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/feedback/blob/blobtest"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/monitoring"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/notify"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/notify/mail/mailtest"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/auth"
	appdb "github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/db"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/httpx"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/registry"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/reqctx"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/sites"
)

// uptimeWindowDays is the configured rolling window the test harness exposes via
// GET /api/meta and computes cached uptime over.
const uptimeWindowDays = 90

// testAllowedOrigins is the harness's STATUS_ALLOWED_ORIGINS. The wildcard is
// the production default; cross-origin behaviour is unobservable without one.
var testAllowedOrigins = []string{"https://*.tilcer.cz"}

type api struct {
	srv *httptest.Server
	// rt is the composed router. Its mux is walked by the routing tests, which
	// enumerate the real tree rather than a hand-written list of prefixes.
	rt *httpx.Router
	// blobs is the feedback module's fake bucket. ⚠ It truncates an oversized
	// upload rather than refusing it, because that is what R2 does (V3-D54).
	blobs *blobtest.Fake
	// fb is the feedback module, kept so a test can Drain the object deletes a
	// DELETE response deliberately does not wait for (FR-22).
	fb *feedback.Module
	// mail is the notify module's fake provider, and notify the module itself —
	// kept so a test can run the worker with a clock of its choosing.
	mail   *mailtest.Fake
	notify *notify.Module
	// db is the service's database, for the few assertions no route can make.
	db *sql.DB
}

// testPublicURL is the harness's STATUS_PUBLIC_URL: every emailed link starts
// with it.
const testPublicURL = "https://status.example.test"

func newAPI(t *testing.T, burst int, ratePerSec float64) *api {
	t.Helper()
	db, err := appdb.Open(filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	migFS, err := bootstrap.MigrationFS()
	if err != nil {
		t.Fatalf("migfs: %v", err)
	}
	if err := appdb.Migrate(db, migFS); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	authCfg := auth.Config{BypassActor: &reqctx.Actor{UserID: "dev", Type: "user", Label: "dev", Roles: []string{"admin"}}}
	authHandler := auth.NewHandler(authCfg, db)
	sessionMW := auth.NewSessionAuth(authCfg)
	csrfMW := auth.NewCSRF(nil, true)

	sitesMod := sites.NewModule(db, 2)
	crashMod := crash.NewModule(db, sitesMod.Store(), crash.Config{
		MaxIngestBytes: 1024, IngestRatePerSec: ratePerSec, IngestBurst: burst,
		RedFailThreshold: 2, ReopenOnRegression: true,
	})
	monMod := monitoring.NewModule(db, monitoring.Config{
		CheckTimeout: time.Second, PollConcurrency: 1, RedFailThreshold: 2, UptimeWindowDays: uptimeWindowDays,
		FeedbackEnabled: true, NotificationsEnabled: true,
	}, discardLogger())
	blobs := blobtest.New()
	fbMod := feedback.NewModule(db, sitesMod.Store(), blobs, feedback.Config{
		Enabled: true, MaxFiles: 3, MaxImageBytes: 10 << 20, MaxVideoBytes: 50 << 20, MaxTextBytes: 8192,
		RatePerSec: 100, Burst: 100, IPRatePerSec: 100, IPBurst: 100,
		UploadTTL: 10 * time.Minute, ViewTTL: 5 * time.Minute, UnclaimedTTL: 24 * time.Hour,
		MinDwell: 0, TicketSecret: "apitest-ticket-secret", IPHashSalt: "apitest-salt",
		AllowedOrigins: testAllowedOrigins,
	}, discardLogger())
	// The board badge and the object half of the site cascade are injected here,
	// exactly as cmd/status does it — `sites` never imports `feedback`.
	sitesMod.SetReportCounter(fbMod)
	sitesMod.SetObjectPurger(fbMod)
	// …and the notifier into all three producers, as cmd/status does.
	mailer := mailtest.New()
	notifyMod := notify.NewModule(db, sitesMod.Store(), mailer, notify.Config{
		PublicURL: testPublicURL, From: "status <status@example.test>",
		DigestWindow: 2 * time.Minute, MaxPerHour: 6, RetentionDays: 90,
	}, discardLogger())
	crashMod.SetNotifier(notifyMod.Notifier())
	monMod.SetNotifier(notifyMod.Notifier())
	fbMod.SetNotifier(notifyMod.Notifier())
	modules := []registry.Module{sitesMod, crashMod, monMod, fbMod, notifyMod}

	handler := httpx.NewRouter(httpx.Deps{
		Logger: discardLogger(), DB: db, Site: "status", InsecureAuth: true,
		MountAuth: func(a chi.Router) { authHandler.Mount(a, csrfMW) },
		MountPublicAPI: func(a chi.Router) {
			crashMod.RegisterPublicRoutes(a)
			fbMod.RegisterPublicRoutes(a)
		},
		SessionMW: sessionMW, CSRFMW: csrfMW,
		MountAPI:       func(a chi.Router) { registry.MountAll(a, modules) },
		AllowedOrigins: testAllowedOrigins,
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &api{srv: srv, rt: handler, blobs: blobs, fb: fbMod, mail: mailer, notify: notifyMod, db: db}
}

func (a *api) do(t *testing.T, method, path string, body any, headers map[string]string) (int, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, a.srv.URL+path, rdr)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

// doRaw sends an exact request body (not JSON-marshalled), for exercising body
// hardening like the trailing-content check.
func (a *api) doRaw(t *testing.T, method, path, body string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, a.srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

func TestSitesAndCrashPipeline(t *testing.T) {
	a := newAPI(t, 100, 1000)

	// --- create a site, capture the ingest key (shown once) ---
	st, body := a.do(t, "POST", "/api/sites", map[string]any{
		"id": "fin", "name": "Finance", "monitor_url": "https://fin.tilcer.cz/readyz",
	}, nil)
	if st != 201 {
		t.Fatalf("create site: %d %s", st, body)
	}
	var created struct {
		Color     string `json:"color"`
		IngestKey string `json:"ingest_key"`
	}
	mustJSON(t, body, &created)
	if created.IngestKey == "" || !strings.HasPrefix(created.IngestKey, "ik_") {
		t.Fatalf("missing ingest key: %s", body)
	}
	if created.Color != "unknown" {
		t.Fatalf("new monitored site should be unknown, got %q", created.Color)
	}
	key := created.IngestKey

	// --- duplicate id → 409 ---
	if st, _ := a.do(t, "POST", "/api/sites", map[string]any{"id": "fin", "name": "x"}, nil); st != 409 {
		t.Fatalf("duplicate id should be 409, got %d", st)
	}
	// --- invalid id → 422 ---
	if st, _ := a.do(t, "POST", "/api/sites", map[string]any{"id": "BAD ID", "name": "x"}, nil); st != 422 {
		t.Fatalf("invalid id should be 422, got %d", st)
	}

	hdr := map[string]string{"X-Ingest-Key": key}

	// --- ingest a crash → 202, group + event ---
	st, body = a.do(t, "POST", "/api/ingest/fin", map[string]any{
		"message": "user 41 not found", "level": "error", "environment": "prod",
	}, hdr)
	if st != 202 {
		t.Fatalf("ingest: %d %s", st, body)
	}
	var acc struct {
		GroupID int64 `json:"group_id"`
		EventID int64 `json:"event_id"`
	}
	mustJSON(t, body, &acc)
	if acc.GroupID == 0 || acc.EventID == 0 {
		t.Fatalf("ingest returned zero ids: %s", body)
	}

	// --- a similar crash lands in the SAME group (count increments) ---
	st, body = a.do(t, "POST", "/api/ingest/fin", map[string]any{"message": "user 99 not found", "level": "error"}, hdr)
	mustJSON(t, body, &acc)
	firstGroup := acc.GroupID
	st2, body2 := a.do(t, "POST", "/api/ingest/fin", map[string]any{"message": "database connection refused"}, hdr)
	var acc2 struct {
		GroupID int64 `json:"group_id"`
	}
	mustJSON(t, body2, &acc2)
	_ = st
	_ = st2
	if acc2.GroupID == firstGroup {
		t.Fatalf("a distinct message must open a new group")
	}

	// --- site is now orange (recent crash in an open group) ---
	st, body = a.do(t, "GET", "/api/sites/fin", nil, nil)
	var summ struct {
		Color            string `json:"color"`
		OpenCrashGroups  int    `json:"open_crash_groups"`
		RecentCrashCount int    `json:"recent_crash_count"`
	}
	mustJSON(t, body, &summ)
	if summ.Color != "orange" {
		t.Fatalf("site with recent crashes should be orange, got %q", summ.Color)
	}
	if summ.OpenCrashGroups != 2 || summ.RecentCrashCount != 3 {
		t.Fatalf("expected 2 open groups / 3 recent, got %d / %d", summ.OpenCrashGroups, summ.RecentCrashCount)
	}

	// --- wrong key → 401; unknown site → 404 ---
	if st, _ := a.do(t, "POST", "/api/ingest/fin", map[string]any{"message": "x"}, map[string]string{"X-Ingest-Key": "ik_wrong"}); st != 401 {
		t.Fatalf("wrong key should be 401, got %d", st)
	}
	if st, _ := a.do(t, "POST", "/api/ingest/nope", map[string]any{"message": "x"}, hdr); st != 404 {
		t.Fatalf("unknown site should be 404, got %d", st)
	}

	// --- oversized body → 413 ---
	if st, _ := a.do(t, "POST", "/api/ingest/fin", map[string]any{"message": strings.Repeat("A", 4000)}, hdr); st != 413 {
		t.Fatalf("oversized body should be 413, got %d", st)
	}

	// --- resolve the first group → removed from orange signal ---
	if st, _ := a.do(t, "PATCH", "/api/crashes/"+itoa(firstGroup), map[string]any{"status": "resolved"}, nil); st != 200 {
		t.Fatalf("resolve group should be 200, got %d", st)
	}
	// second group is still open → still orange
	st, body = a.do(t, "GET", "/api/sites/fin", nil, nil)
	mustJSON(t, body, &summ)
	if summ.OpenCrashGroups != 1 {
		t.Fatalf("after resolving one group, expected 1 open, got %d", summ.OpenCrashGroups)
	}

	// --- reopen on regression: a new event on the resolved group reopens it ---
	a.do(t, "POST", "/api/ingest/fin", map[string]any{"message": "user 7 not found", "level": "error"}, hdr)
	st, body = a.do(t, "GET", "/api/sites/fin", nil, nil)
	mustJSON(t, body, &summ)
	if summ.OpenCrashGroups != 2 {
		t.Fatalf("reopen-on-regression should restore 2 open groups, got %d", summ.OpenCrashGroups)
	}

	// --- browse groups + drill into events ---
	st, body = a.do(t, "GET", "/api/sites/fin/crashes", nil, nil)
	var groups struct {
		Items []struct {
			ID    int64  `json:"id"`
			Count int    `json:"count"`
			Title string `json:"title"`
		} `json:"items"`
	}
	mustJSON(t, body, &groups)
	if len(groups.Items) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups.Items))
	}
	st, body = a.do(t, "GET", "/api/crashes/"+itoa(firstGroup), nil, nil)
	var detail struct {
		Group  struct{ Count int }        `json:"group"`
		Events []struct{ Message string } `json:"events"`
	}
	mustJSON(t, body, &detail)
	if len(detail.Events) == 0 {
		t.Fatalf("group detail should include events")
	}

	// --- delete cascades ---
	if st, _ := a.do(t, "DELETE", "/api/sites/fin", nil, nil); st != 204 {
		t.Fatalf("delete should be 204, got %d", st)
	}
	if st, _ := a.do(t, "GET", "/api/sites/fin", nil, nil); st != 404 {
		t.Fatalf("deleted site should be 404, got %d", st)
	}
	if st, _ := a.do(t, "GET", "/api/sites/fin/crashes", nil, nil); st != 404 {
		t.Fatalf("crashes for deleted site should be 404, got %d", st)
	}
}

func TestIngestRateLimit(t *testing.T) {
	a := newAPI(t, 1, 0.001) // burst 1, ~never refills within the test
	_, body := a.do(t, "POST", "/api/sites", map[string]any{"id": "rl", "name": "RL"}, nil)
	var created struct {
		IngestKey string `json:"ingest_key"`
	}
	mustJSON(t, body, &created)
	hdr := map[string]string{"X-Ingest-Key": created.IngestKey}

	if st, _ := a.do(t, "POST", "/api/ingest/rl", map[string]any{"message": "one"}, hdr); st != 202 {
		t.Fatalf("first ingest should be 202, got %d", st)
	}
	st, _ := a.do(t, "POST", "/api/ingest/rl", map[string]any{"message": "two"}, hdr)
	if st != 429 {
		t.Fatalf("second ingest past burst should be 429, got %d", st)
	}
}

func TestCrashOnlySiteNeverRed(t *testing.T) {
	a := newAPI(t, 100, 1000)
	// No monitor_url → crash-only.
	_, body := a.do(t, "POST", "/api/sites", map[string]any{"id": "yarnlog", "name": "Yarnlog"}, nil)
	var created struct {
		Color          string   `json:"color"`
		MonitorEnabled bool     `json:"monitor_enabled"`
		UptimePct      *float64 `json:"uptime_pct"`
	}
	mustJSON(t, body, &created)
	if created.MonitorEnabled {
		t.Fatalf("crash-only site should have monitoring off")
	}
	if created.UptimePct != nil {
		t.Fatalf("crash-only site uptime_pct should be null")
	}
	if created.Color != "green" {
		t.Fatalf("crash-only site with no crashes should be green, got %q", created.Color)
	}
}

// TestUpdateMonitoringContract locks in the PATCH monitoring-enable contract
// (enabling without a URL is a 422, not a silent disable) and the shared body
// hardening (trailing content after the JSON object is rejected).
func TestUpdateMonitoringContract(t *testing.T) {
	a := newAPI(t, 100, 1000)

	// crash-only site (no monitor_url).
	if st, body := a.do(t, "POST", "/api/sites", map[string]any{"id": "app", "name": "App"}, nil); st != 201 {
		t.Fatalf("create site: %d %s", st, body)
	}

	// Enabling monitoring with no URL is rejected (the same input createSite would
	// have silently coerced to disabled — the asymmetry the review flagged).
	if st, _ := a.do(t, "PATCH", "/api/sites/app", map[string]any{"monitor_enabled": true}, nil); st != 422 {
		t.Fatalf("enable monitoring without URL should be 422, got %d", st)
	}

	// Providing a URL and enabling together succeeds.
	if st, body := a.do(t, "PATCH", "/api/sites/app", map[string]any{"monitor_url": "https://app.tilcer.cz/readyz", "monitor_enabled": true}, nil); st != 200 {
		t.Fatalf("enable with URL should be 200, got %d %s", st, body)
	}

	// Clearing the URL while still asking to enable is likewise a 422.
	if st, _ := a.do(t, "PATCH", "/api/sites/app", map[string]any{"monitor_url": nil, "monitor_enabled": true}, nil); st != 422 {
		t.Fatalf("clear URL while enabling should be 422, got %d", st)
	}

	// Trailing content after the JSON object is rejected (DecodeJSON hardening now
	// applies to PATCH, matching POST).
	if st, _ := a.doRaw(t, "PATCH", "/api/sites/app", `{"name":"App2"}{"x":1}`); st != 422 {
		t.Fatalf("trailing content on PATCH should be 422, got %d", st)
	}
}

// TestUptimeBucketsAndMeta locks in the per-window default bucket count (a fixed
// 90 left short windows mostly empty) and the meta endpoint that surfaces the
// configured uptime window to the SPA.
func TestUptimeBucketsAndMeta(t *testing.T) {
	a := newAPI(t, 100, 1000)
	if st, body := a.do(t, "POST", "/api/sites", map[string]any{"id": "web", "name": "Web", "monitor_url": "https://web.tilcer.cz/readyz"}, nil); st != 201 {
		t.Fatalf("create site: %d %s", st, body)
	}

	var m struct {
		UptimeWindowDays int   `json:"uptime_window_days"`
		FeedbackEnabled  *bool `json:"feedback_enabled"`
	}
	st, body := a.do(t, "GET", "/api/meta", nil, nil)
	if st != 200 {
		t.Fatalf("meta should be 200, got %d %s", st, body)
	}
	mustJSON(t, body, &m)
	if m.UptimeWindowDays != uptimeWindowDays {
		t.Fatalf("meta uptime_window_days = %d, want %d", m.UptimeWindowDays, uptimeWindowDays)
	}
	// openapi 0.3.0 documents feedback_enabled here. A dashboard that reads it as
	// undefined hides the feature on a deployment that has it — the drift
	// /api/meta exists to prevent, so the field must be present, not merely
	// truthy-by-accident.
	if m.FeedbackEnabled == nil || !*m.FeedbackEnabled {
		t.Fatalf("meta must carry feedback_enabled=true for this deployment, got %s", body)
	}

	bucketsFor := func(query string) int {
		st, body := a.do(t, "GET", "/api/sites/web/uptime"+query, nil, nil)
		if st != 200 {
			t.Fatalf("uptime%s should be 200, got %d %s", query, st, body)
		}
		var u struct {
			Window  string            `json:"window"`
			Buckets []json.RawMessage `json:"buckets"`
		}
		mustJSON(t, body, &u)
		return len(u.Buckets)
	}

	for _, tc := range []struct {
		query string
		want  int
	}{
		{"", 90},                       // default window (90d) → 90 daily buckets
		{"?window=90d", 90},            // explicit 90d → 90
		{"?window=30d", 30},            // 30d → 30, not 90 mostly-empty buckets
		{"?window=7d", 7},              // 7d → 7
		{"?window=24h", 24},            // 24h intraday → 24 hourly buckets
		{"?window=30d&buckets=45", 45}, // an explicit buckets override still wins
	} {
		if got := bucketsFor(tc.query); got != tc.want {
			t.Fatalf("uptime%q buckets = %d, want %d", tc.query, got, tc.want)
		}
	}
}

// --- helpers ---

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func mustJSON(t *testing.T, b []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("unmarshal %s: %v", b, err)
	}
}

func itoa(n int64) string {
	return strings.TrimSpace(jsonNum(n))
}
func jsonNum(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
