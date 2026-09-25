package notify

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/crash"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/feedback"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/monitoring"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/notify/mail"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/notify/mail/mailtest"
	appdb "github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/db"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/registry"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/reqctx"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/sites"
)

const testPublicURL = "https://status.example.test"

// t0 is the harness's clock origin. Every signal and every worker pass takes an
// explicit time, so nothing here depends on the wall clock.
var t0 = time.Date(2026, 9, 25, 14, 0, 0, 0, time.UTC)

func testConfig() Config {
	return Config{
		PublicURL:     testPublicURL,
		From:          "status <status@example.test>",
		DigestWindow:  2 * time.Minute,
		MaxPerHour:    6,
		RetentionDays: 90,
	}
}

// harness is the notify module over a temp database with the real migration
// sequence, a fake provider, and a chi router for the settings routes.
type harness struct {
	t    *testing.T
	db   *sql.DB
	mod  *Module
	mail *mailtest.Fake
	srv  http.Handler
}

// migrationSources lists the sources by hand: an in-package test may not import
// internal/bootstrap, which imports this package. internal/apitest drives the
// real assembly.
func migrationSources(withNotify bool) []registry.MigrationSource {
	src := []registry.MigrationSource{
		{Name: "platform", FS: appdb.MigrationsFS},
		{Name: "sites", FS: sites.MigrationsFS},
		{Name: "feedback", FS: feedback.MigrationsFS},
	}
	if withNotify {
		src = append(src, registry.MigrationSource{Name: "notify", FS: MigrationsFS})
	}
	return src
}

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := appdb.Open(filepath.Join(t.TempDir(), "notify.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func migrate(t *testing.T, db *sql.DB, withNotify bool) {
	t.Helper()
	migFS, err := registry.MergeMigrations(migrationSources(withNotify))
	if err != nil {
		t.Fatalf("migfs: %v", err)
	}
	if err := appdb.Migrate(db, migFS); err != nil {
		t.Fatalf("migrate: %v", err)
	}
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	return newHarnessWith(t, testConfig(), true)
}

// newHarnessWith builds a harness; available=false wires no provider at all.
func newHarnessWith(t *testing.T, cfg Config, available bool) *harness {
	t.Helper()
	db := openDB(t)
	migrate(t, db, true)
	fake := mailtest.New()
	var mailer mail.Mailer
	if available {
		mailer = fake
	}
	mod := NewModule(db, sites.NewStore(db, 2), mailer, cfg, discardLogger())
	mod.worker.gap = 0

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			roles := []string{"admin"}
			if v := req.Header.Get("X-Test-Roles"); v != "" {
				roles = strings.Split(v, ",")
			}
			ctx := reqctx.WithActor(req.Context(), reqctx.Actor{UserID: "dev", Type: "user", Roles: roles})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Route("/api", mod.RegisterRoutes)

	h := &harness{t: t, db: db, mod: mod, mail: fake, srv: r}
	h.seedSite("home", "Home", "https://home.example.test")
	h.seedSite("fin", "Finance", "https://fin.example.test")
	return h
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func (h *harness) seedSite(id, name, url string) {
	h.t.Helper()
	if _, err := h.db.Exec(
		`INSERT INTO site (id, name, monitor_url, monitor_enabled, expected_status, crash_window_hours, ingest_key_hash, cached_color, created_at)
		 VALUES (?, ?, ?, 1, 200, 24, 'hash', 'unknown', ?)`,
		id, name, url, ts(t0)); err != nil {
		h.t.Fatalf("seed site: %v", err)
	}
}

var groupSeq atomic.Int64

// seedGroup inserts a crash group in the given status and returns its id.
func (h *harness) seedGroup(siteID, status string) int64 {
	h.t.Helper()
	res, err := h.db.Exec(
		`INSERT INTO crash_group (site_id, fingerprint, title, level, count, status, first_seen, last_seen)
		 VALUES (?, ?, 'TypeError: x is undefined', 'error', 1, ?, ?, ?)`,
		siteID, fmt.Sprintf("fp-%d", groupSeq.Add(1)), status, ts(t0), ts(t0))
	if err != nil {
		h.t.Fatalf("seed group: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

// enable switches notifications on for the given recipients, every kind on.
func (h *harness) enable(recipients ...string) {
	h.t.Helper()
	if err := saveSettings(context.Background(), h.db, settings{
		Enabled: true, Recipients: recipients, OnCrash: true, OnFeedback: true, OnDowntime: true,
	}, ts(t0)); err != nil {
		h.t.Fatalf("enable: %v", err)
	}
}

func (h *harness) setSettings(st settings) {
	h.t.Helper()
	if err := saveSettings(context.Background(), h.db, st, ts(t0)); err != nil {
		h.t.Fatalf("settings: %v", err)
	}
}

func (h *harness) inTx(fn func(tx *sql.Tx) error) error {
	return appdb.WithTx(context.Background(), h.db, fn)
}

func (h *harness) crash(s crash.Signal) {
	h.t.Helper()
	if err := h.inTx(func(tx *sql.Tx) error { return h.mod.notifier.CrashRecorded(context.Background(), tx, s) }); err != nil {
		h.t.Fatalf("crash hook: %v", err)
	}
}

func (h *harness) check(c monitoring.CheckSignal) {
	h.t.Helper()
	if err := h.inTx(func(tx *sql.Tx) error { return h.mod.notifier.CheckRecorded(context.Background(), tx, c) }); err != nil {
		h.t.Fatalf("check hook: %v", err)
	}
}

func (h *harness) report(s feedback.ReportSignal) {
	h.t.Helper()
	if err := h.inTx(func(tx *sql.Tx) error { return h.mod.notifier.ReportSubmitted(context.Background(), tx, s) }); err != nil {
		h.t.Fatalf("report hook: %v", err)
	}
}

// queued returns the kinds of the events not yet in a digest, oldest first.
func (h *harness) queued() []string {
	h.t.Helper()
	rows, err := h.db.Query(`SELECT kind FROM notify_event WHERE digest_id IS NULL ORDER BY id`)
	if err != nil {
		h.t.Fatalf("queued: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			h.t.Fatal(err)
		}
		out = append(out, k)
	}
	return out
}

// allDigests returns every digest, oldest first.
func (h *harness) allDigests() []digest {
	h.t.Helper()
	ds, err := recentDigests(context.Background(), h.db, 1000)
	if err != nil {
		h.t.Fatalf("digests: %v", err)
	}
	for i, j := 0, len(ds)-1; i < j; i, j = i+1, j-1 {
		ds[i], ds[j] = ds[j], ds[i]
	}
	return ds
}

func (h *harness) run(at time.Time) { h.mod.worker.RunOnce(context.Background(), at) }

func (h *harness) count(query string, args ...any) int {
	h.t.Helper()
	var n int
	if err := h.db.QueryRow(query, args...).Scan(&n); err != nil {
		h.t.Fatalf("count %q: %v", query, err)
	}
	return n
}

// do issues a request through the router.
func (h *harness) do(method, path string, body any, headers map[string]string) (int, []byte) {
	h.t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			h.t.Fatal(err)
		}
		rdr = strings.NewReader(string(b))
	}
	req := httptest.NewRequest(method, path, rdr)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.srv.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

func mustJSON(t *testing.T, body []byte, dst any) {
	t.Helper()
	if err := json.Unmarshal(body, dst); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
}

// newCrash is a signal for a brand-new group's first, qualifying event.
func newCrash(groupID int64, at time.Time) crash.Signal {
	return crash.Signal{
		SiteID: "home", GroupID: groupID, GroupStatus: crash.StatusOpen,
		Level: crash.LevelError, Environment: "prod", Release: "home@1.2.3",
		Title: "TypeError: x is undefined", Message: "TypeError: x is undefined\n    at render (app.js:1:2)", At: at,
	}
}

// repeat is a later event in the same group.
func repeat(groupID int64, env string, at time.Time) crash.Signal {
	s := newCrash(groupID, at)
	s.Environment = env
	return s
}

func equalKinds(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
