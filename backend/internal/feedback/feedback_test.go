package feedback

import (
	"context"
	"database/sql"
	"encoding/json"
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
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/feedback/blob/blobtest"
	appdb "github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/db"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/registry"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/reqctx"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/timeutil"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/sites"
)

const (
	testSite   = "home"
	testOrigin = "https://home.tilcer.cz"
	testIP     = "203.0.113.9"
)

// harness is a feedback module over a temp database, a fake bucket, and a real
// chi router — the URL parameters the handlers read only exist through routing.
type harness struct {
	t     *testing.T
	mod   *Module
	db    *sql.DB
	blobs *blobtest.Fake
	srv   http.Handler
	key   string // plaintext widget key for testSite
}

func testConfig() Config {
	return Config{
		Enabled:        true,
		MaxFiles:       3,
		MaxImageBytes:  10 << 20,
		MaxVideoBytes:  50 << 20,
		MaxTextBytes:   8192,
		RatePerSec:     100,
		Burst:          100,
		IPRatePerSec:   100,
		IPBurst:        100,
		UploadTTL:      10 * time.Minute,
		ViewTTL:        5 * time.Minute,
		UnclaimedTTL:   24 * time.Hour,
		MinDwell:       0,
		TicketSecret:   "ticket-secret-for-tests",
		IPHashSalt:     "salt-for-tests",
		AllowedOrigins: []string{"https://*.tilcer.cz"},
	}
}

func newHarness(t *testing.T, cfg Config) *harness {
	t.Helper()
	db, err := appdb.Open(filepath.Join(t.TempDir(), "feedback.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	// The sources are listed here rather than taken from internal/bootstrap, which
	// imports this package: an in-package test may not import its own importer.
	// internal/apitest drives the real bootstrap assembly, so the two agreeing is
	// covered there.
	migFS, err := registry.MergeMigrations([]registry.MigrationSource{
		{Name: "platform", FS: appdb.MigrationsFS},
		{Name: "sites", FS: sites.MigrationsFS},
		{Name: "feedback", FS: MigrationsFS},
	})
	if err != nil {
		t.Fatalf("migfs: %v", err)
	}
	if err := appdb.Migrate(db, migFS); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	blobs := blobtest.New()
	sitesStore := sites.NewStore(db, 2)
	mod := NewModule(db, sitesStore, blobs, cfg, discardLogger())

	r := chi.NewRouter()
	// The gated routes run behind an admin actor and a resolved client IP, which
	// in the real service come from the session middleware and RequestID.
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			// Two test-only headers stand in for the two things the real middleware
			// resolves per request: who is calling, and from where. A case that needs
			// a second client or a lesser role sets one rather than building a second
			// harness — and neither header means anything to the handlers, which read
			// both values from the context.
			roles := []string{"admin"}
			if v := req.Header.Get("X-Test-Roles"); v != "" {
				roles = strings.Split(v, ",")
			}
			ip := testIP
			if v := req.Header.Get("X-Test-IP"); v != "" {
				ip = v
			}
			ctx := reqctx.WithActor(req.Context(), reqctx.Actor{UserID: "dev", Type: "user", Roles: roles})
			ctx = reqctx.WithRequest(ctx, reqctx.RequestInfo{RequestID: "test", IP: ip})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Route("/api", func(api chi.Router) {
		mod.RegisterPublicRoutes(api)
		mod.RegisterRoutes(api)
	})

	h := &harness{t: t, mod: mod, db: db, blobs: blobs, srv: r}
	h.seedSite(testSite, "Home")
	h.key = h.enable(testSite)
	return h
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// seedSite inserts a bare site row (the registry's own CRUD is tested there).
func (h *harness) seedSite(id, name string) {
	h.t.Helper()
	_, hash, err := sites.GenerateKey()
	if err != nil {
		h.t.Fatalf("ingest key: %v", err)
	}
	if _, err := h.db.Exec(
		`INSERT INTO site (id, name, monitor_enabled, expected_status, crash_window_hours, ingest_key_hash, cached_color, created_at)
		 VALUES (?,?,0,200,24,?,'unknown',?)`,
		id, name, hash, timeutil.Format(time.Now().UTC())); err != nil {
		h.t.Fatalf("seed site: %v", err)
	}
}

// enable turns feedback on for a site and returns the plaintext widget key.
func (h *harness) enable(siteID string) string {
	h.t.Helper()
	plaintext, hash, err := GenerateWidgetKey()
	if err != nil {
		h.t.Fatalf("widget key: %v", err)
	}
	on := true
	if _, _, err := h.mod.store.UpsertConfig(context.Background(), siteID, &on, nil, hash, true, time.Now().UTC()); err != nil {
		h.t.Fatalf("enable: %v", err)
	}
	return plaintext
}

// do issues a request through the router with a JSON body.
func (h *harness) do(method, path string, body any, headers map[string]string) (int, []byte) {
	h.t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			h.t.Fatalf("marshal: %v", err)
		}
		rdr = strings.NewReader(string(b))
	}
	req := httptest.NewRequest(method, path, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.srv.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

// widgetHeaders is the header set a real widget sends.
func widgetHeaders(key string) map[string]string {
	return map[string]string{"X-Widget-Key": key, "Origin": testOrigin}
}

// with returns headers plus one more — for the test-only X-Test-IP and
// X-Test-Roles the harness middleware reads.
func with(headers map[string]string, name, value string) map[string]string {
	out := map[string]string{name: value}
	for k, v := range headers {
		out[k] = v
	}
	return out
}

// ticket fetches a fresh submission ticket through the public config route.
func (h *harness) ticket() string {
	h.t.Helper()
	code, body := h.do(http.MethodGet, "/api/ingest/"+testSite+"/feedback/config", nil, widgetHeaders(h.key))
	if code != http.StatusOK {
		h.t.Fatalf("config: status %d body %s", code, body)
	}
	var out WidgetConfig
	mustJSON(h.t, body, &out)
	if out.Ticket == nil {
		h.t.Fatalf("config returned no ticket: %s", body)
	}
	return *out.Ticket
}

func mustJSON(t *testing.T, body []byte, dst any) {
	t.Helper()
	if err := json.Unmarshal(body, dst); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
}

// submission builds a valid body with a fresh ticket.
func (h *harness) submission(files ...DeclaredFile) map[string]any {
	return map[string]any{
		"message": "The board does not load on my phone.",
		"kind":    KindBug,
		"ticket":  h.ticket(),
		"files":   files,
	}
}

// --- the guard chain --------------------------------------------------------

// spyBody records whether anything read the request body. It is how "the body is
// never parsed before the key is checked" is asserted structurally rather than by
// reading the handler.
type spyBody struct {
	read atomic.Bool
	r    *strings.Reader
}

func newSpyBody(s string) *spyBody { return &spyBody{r: strings.NewReader(s)} }

func (s *spyBody) Read(p []byte) (int, error) {
	s.read.Store(true)
	return s.r.Read(p)
}
func (s *spyBody) Close() error { return nil }

// TestGuardChainOrder enumerates PRD FR-17's fixed order: 404 unknown site → 401
// bad widget key → 403 disabled → 403 origin → 429 over rate → 413 oversized →
// 422 invalid → 202. Each case violates the checks AFTER the one it expects too,
// so a chain in the wrong order fails here rather than in production.
func TestGuardChainOrder(t *testing.T) {
	h := newHarness(t, testConfig())
	h.seedSite("fin", "Fin") // exists, but feedback was never enabled
	disabledKey := func() string {
		plaintext, hash, _ := GenerateWidgetKey()
		off := false
		if _, _, err := h.mod.store.UpsertConfig(context.Background(), "fin", &off, nil, hash, true, time.Now().UTC()); err != nil {
			t.Fatalf("disable fin: %v", err)
		}
		return plaintext
	}()

	oversized := `{"message":"` + strings.Repeat("x", 9000) + `","ticket":"x"}`

	cases := []struct {
		name    string
		site    string
		headers map[string]string
		body    string
		want    int
	}{
		{
			name:    "unknown site wins over every later check",
			site:    "nope",
			headers: map[string]string{"X-Widget-Key": "wk_wrong", "Origin": "https://evil.example"},
			body:    oversized,
			want:    http.StatusNotFound,
		},
		{
			name:    "bad key wins over disabled, origin and body size",
			site:    "fin",
			headers: map[string]string{"X-Widget-Key": "wk_wrong", "Origin": "https://evil.example"},
			body:    oversized,
			want:    http.StatusUnauthorized,
		},
		{
			name:    "missing key is a 401, not a 422",
			site:    testSite,
			headers: map[string]string{"Origin": testOrigin},
			body:    `{"message":"hi","ticket":"x"}`,
			want:    http.StatusUnauthorized,
		},
		{
			name:    "disabled wins over origin and body size",
			site:    "fin",
			headers: map[string]string{"X-Widget-Key": disabledKey, "Origin": "https://evil.example"},
			body:    oversized,
			want:    http.StatusForbidden,
		},
		{
			name:    "foreign origin wins over body size",
			site:    testSite,
			headers: map[string]string{"X-Widget-Key": h.key, "Origin": "https://evil.example"},
			body:    oversized,
			want:    http.StatusForbidden,
		},
		{
			name:    "oversized body wins over an invalid payload",
			site:    testSite,
			headers: widgetHeaders(h.key),
			body:    oversized,
			want:    http.StatusRequestEntityTooLarge,
		},
		{
			name:    "invalid payload is a 422",
			site:    testSite,
			headers: widgetHeaders(h.key),
			body:    `{"message":"","ticket":"nonsense"}`,
			want:    http.StatusUnprocessableEntity,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/ingest/"+tc.site+"/feedback", nil)
			spy := newSpyBody(tc.body)
			req.Body = spy
			req.ContentLength = int64(len(tc.body))
			req.Header.Set("Content-Type", "application/json")
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			h.srv.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.want, rec.Body.String())
			}
			// ⚠ The body must be untouched for everything the key gates.
			if tc.want == http.StatusNotFound || tc.want == http.StatusUnauthorized ||
				tc.want == http.StatusForbidden {
				if spy.read.Load() {
					t.Fatalf("request body was read before the %d guard — an untrusted body must never be parsed on a bad key", tc.want)
				}
			}
		})
	}

	// The 429 rung, which cannot share the table above: every other case needs the
	// burst headroom the harness is built with, so this one gets its own module
	// with a bucket of exactly one token. The refused request is BOTH oversized
	// and invalid, so a limiter moved below the decode would answer 413 here —
	// and would have read an untrusted body it was about to refuse anyway.
	t.Run("over rate wins over body size and an invalid payload", func(t *testing.T) {
		cfg := testConfig()
		cfg.RatePerSec, cfg.Burst = 0.001, 1
		rh := newHarness(t, cfg)
		if code, body := rh.do(http.MethodPost, "/api/ingest/"+testSite+"/feedback",
			rh.submission(), widgetHeaders(rh.key)); code != http.StatusAccepted {
			t.Fatalf("first submission spends the only token: %d %s", code, body)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/ingest/"+testSite+"/feedback", nil)
		spy := newSpyBody(oversized)
		req.Body = spy
		req.ContentLength = int64(len(oversized))
		req.Header.Set("Content-Type", "application/json")
		for k, v := range widgetHeaders(rh.key) {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		rh.srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("status = %d, want 429 before the 413 (body %s)", rec.Code, rec.Body.String())
		}
		if spy.read.Load() {
			t.Fatal("the request body was read before the 429 guard")
		}
	})
}

// TestGuardChainAcceptsValidSubmission is the 202 end of the chain.
func TestGuardChainAcceptsValidSubmission(t *testing.T) {
	h := newHarness(t, testConfig())
	code, body := h.do(http.MethodPost, "/api/ingest/"+testSite+"/feedback", h.submission(), widgetHeaders(h.key))
	if code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body %s)", code, body)
	}
	var out Accepted
	mustJSON(t, body, &out)
	if !ValidRef(out.Ref) {
		t.Fatalf("ref %q is not a valid reference code", out.Ref)
	}
	if len(out.Uploads) != 0 {
		t.Fatalf("a report with no declared files must issue no upload slots, got %d", len(out.Uploads))
	}
}

// TestSubmitRateLimited covers the 429, including the Retry-After header.
func TestSubmitRateLimited(t *testing.T) {
	cfg := testConfig()
	cfg.RatePerSec, cfg.Burst = 0.001, 1
	h := newHarness(t, cfg)

	if code, body := h.do(http.MethodPost, "/api/ingest/"+testSite+"/feedback", h.submission(), widgetHeaders(h.key)); code != http.StatusAccepted {
		t.Fatalf("first submission: %d %s", code, body)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/ingest/"+testSite+"/feedback", strings.NewReader(`{"message":"again","ticket":"x"}`))
	for k, v := range widgetHeaders(h.key) {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second submission = %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("429 must carry Retry-After")
	}
}

// TestOneIPCannotDrainTheSiteBudget is the fairness property the two buckets
// exist to provide, and it fails against a limiter that charges them in the
// wrong order.
//
// ⚠ The per-key bucket is the SITE's, shared by everyone reporting from it; the
// per-IP bucket is one caller's own. `Allow` decides and charges in one call, so
// consulting the key bucket first made every request an abusive IP was ALREADY
// being refused for cost the site a token anyway — one bad client silently spent
// the whole household's allowance, and the reporters who had done nothing got a
// 429 with no explanation available to them.
func TestOneIPCannotDrainTheSiteBudget(t *testing.T) {
	cfg := testConfig()
	// A site budget of three reports and an IP budget of one, refilling so slowly
	// that nothing recovers during the test.
	cfg.RatePerSec, cfg.Burst = 0.001, 3
	cfg.IPRatePerSec, cfg.IPBurst = 0.001, 1
	h := newHarness(t, cfg)
	const path = "/api/ingest/" + testSite + "/feedback"

	abusive := with(widgetHeaders(h.key), "X-Test-IP", "198.51.100.7")
	if code, body := h.do(http.MethodPost, path, h.submission(), abusive); code != http.StatusAccepted {
		t.Fatalf("first submission from the abusive client = %d, want 202: %s", code, body)
	}
	// Its own bucket is empty now. Every further attempt is refused — and must
	// cost the site nothing, because the site never served them.
	for i := 0; i < 3; i++ {
		if code, _ := h.do(http.MethodPost, path, map[string]any{"message": "again"}, abusive); code != http.StatusTooManyRequests {
			t.Fatalf("abusive attempt %d = %d, want 429", i+2, code)
		}
	}

	other := with(widgetHeaders(h.key), "X-Test-IP", "192.0.2.55")
	if code, body := h.do(http.MethodPost, path, h.submission(), other); code != http.StatusAccepted {
		t.Fatalf("a different reporter = %d, want 202 — one client drained the site's budget: %s", code, body)
	}
}

// TestASiteOverItsBudgetKeepsTheCallersToken is the other half of the ordering:
// a reporter refused because the SITE is out of budget has not been served, so
// the attempt must not cost them a token of their own either.
func TestASiteOverItsBudgetKeepsTheCallersToken(t *testing.T) {
	cfg := testConfig()
	// The site's bucket holds one report and takes 200 ms to refill it; the
	// reporter's own holds two and, for the length of this test, never refills.
	//
	// ⚠ The margins are wide on purpose. `Module`'s limiters are built on
	// time.Now — only `ratelimit`'s own suite can inject a clock — so the two
	// assertions below are wall-clock bets: that the second request lands within
	// 200 ms of the first (with a submission's ticket spend, report insert and
	// httptest round trip in between, over one SQLite writer), and that 300 ms of
	// sleep is more than 200. At 1 000 tokens/sec, which is what this said first,
	// the first bet was on a single millisecond and the test failed roughly once
	// in a hundred runs on an unloaded machine.
	cfg.RatePerSec, cfg.Burst = 5, 1
	cfg.IPRatePerSec, cfg.IPBurst = 0.001, 2
	h := newHarness(t, cfg)
	const path = "/api/ingest/" + testSite + "/feedback"
	me := with(widgetHeaders(h.key), "X-Test-IP", "192.0.2.11")

	if code, body := h.do(http.MethodPost, path, h.submission(), me); code != http.StatusAccepted {
		t.Fatalf("first submission = %d, want 202: %s", code, body)
	}
	// Immediately again: the site's single token has not refilled yet, so this is
	// the site refusing — not me.
	if code, _ := h.do(http.MethodPost, path, map[string]any{"message": "mine"}, me); code != http.StatusTooManyRequests {
		t.Fatal("the site is out of budget: this must be a 429")
	}

	// Once the site's bucket has refilled, my second token must still be there.
	// Without the refund it went to a request the site never served, and this is
	// a 429 for a reporter who has filed exactly one report.
	time.Sleep(300 * time.Millisecond)
	if code, body := h.do(http.MethodPost, path, h.submission(), me); code != http.StatusAccepted {
		t.Fatalf("second submission = %d, want 202 — a refusal by the site's bucket cost the caller a token: %s", code, body)
	}
}

// TestSubmitRejectsInvalid covers everything FR-17 folds into one 422.
func TestSubmitRejectsInvalid(t *testing.T) {
	h := newHarness(t, testConfig())
	honeypot := "https://example.com"
	cases := []struct {
		name string
		body map[string]any
	}{
		{"honeypot filled", map[string]any{"message": "hi", "ticket": h.ticket(), "website": honeypot}},
		{"empty message", map[string]any{"message": "   ", "ticket": h.ticket()}},
		{"unknown content type", map[string]any{"message": "hi", "ticket": h.ticket(),
			"files": []DeclaredFile{{ContentType: "application/pdf", ByteSize: 10}}}},
		{"too many files", map[string]any{"message": "hi", "ticket": h.ticket(),
			"files": []DeclaredFile{
				{ContentType: "image/png", ByteSize: 1},
				{ContentType: "image/png", ByteSize: 1},
				{ContentType: "image/png", ByteSize: 1},
				{ContentType: "image/png", ByteSize: 1},
			}}},
		{"zero-byte file", map[string]any{"message": "hi", "ticket": h.ticket(),
			"files": []DeclaredFile{{ContentType: "image/png", ByteSize: 0}}}},
		{"unknown kind", map[string]any{"message": "hi", "kind": "complaint", "ticket": h.ticket()}},
		{"message over 4000 characters", map[string]any{"message": strings.Repeat("é", 4001), "ticket": h.ticket()}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := h.do(http.MethodPost, "/api/ingest/"+testSite+"/feedback", tc.body, widgetHeaders(h.key))
			if code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422 (body %s)", code, body)
			}
		})
	}
}

// TestTicketRules covers the four ways a ticket fails: spent, too young, expired,
// and signed for another site. The ticket is what makes the dwell check real, so
// each of these is a hole if it does not hold.
func TestTicketRules(t *testing.T) {
	cfg := testConfig()
	cfg.MinDwell = time.Hour // nothing submitted in this test can be old enough
	h := newHarness(t, cfg)
	ctx := context.Background()
	now := time.Now().UTC()

	// A ticket younger than the minimum dwell.
	young := newTicket(testSite, now)
	if err := h.mod.store.InsertTicket(ctx, young, now.Add(ticketTTL)); err != nil {
		t.Fatalf("insert ticket: %v", err)
	}
	post := func(token string) int {
		code, _ := h.do(http.MethodPost, "/api/ingest/"+testSite+"/feedback",
			map[string]any{"message": "hello", "ticket": token}, widgetHeaders(h.key))
		return code
	}
	if code := post(signTicket(young, cfg.TicketSecret)); code != http.StatusUnprocessableEntity {
		t.Fatalf("dwell below the minimum = %d, want 422", code)
	}

	// An expired ticket, and a ticket for another site, with the dwell satisfied.
	h.mod.cfg.MinDwell = 0
	expired := newTicket(testSite, now.Add(-ticketTTL-time.Minute))
	if err := h.mod.store.InsertTicket(ctx, expired, now); err != nil {
		t.Fatalf("insert expired: %v", err)
	}
	if code := post(signTicket(expired, cfg.TicketSecret)); code != http.StatusUnprocessableEntity {
		t.Fatalf("expired ticket = %d, want 422", code)
	}

	h.seedSite("fin", "Fin")
	foreign := newTicket("fin", now.Add(-time.Minute))
	if err := h.mod.store.InsertTicket(ctx, foreign, now.Add(ticketTTL)); err != nil {
		t.Fatalf("insert foreign: %v", err)
	}
	if code := post(signTicket(foreign, cfg.TicketSecret)); code != http.StatusUnprocessableEntity {
		t.Fatalf("ticket signed for another site = %d, want 422", code)
	}

	// A forged signature.
	valid := newTicket(testSite, now.Add(-time.Minute))
	if err := h.mod.store.InsertTicket(ctx, valid, now.Add(ticketTTL)); err != nil {
		t.Fatalf("insert valid: %v", err)
	}
	if code := post(signTicket(valid, "not-the-secret")); code != http.StatusUnprocessableEntity {
		t.Fatalf("forged ticket = %d, want 422", code)
	}

	// The same ticket spends exactly once.
	token := signTicket(valid, cfg.TicketSecret)
	if code := post(token); code != http.StatusAccepted {
		t.Fatalf("first use = %d, want 202", code)
	}
	if code := post(token); code != http.StatusUnprocessableEntity {
		t.Fatalf("second use of a spent ticket = %d, want 422", code)
	}
}

// TestDisabledSiteRendersNoLauncher: the config route answers 200 enabled:false
// with no ticket, so the widget shows no button at all rather than one that fails
// when pressed (V3-D35).
func TestDisabledSiteRendersNoLauncher(t *testing.T) {
	h := newHarness(t, testConfig())
	off := false
	if _, _, err := h.mod.store.UpsertConfig(context.Background(), testSite, &off, nil, "", false, time.Now().UTC()); err != nil {
		t.Fatalf("disable: %v", err)
	}
	code, body := h.do(http.MethodGet, "/api/ingest/"+testSite+"/feedback/config", nil, widgetHeaders(h.key))
	if code != http.StatusOK {
		t.Fatalf("config on a disabled site = %d, want 200 (%s)", code, body)
	}
	var out WidgetConfig
	mustJSON(t, body, &out)
	if out.Enabled {
		t.Fatal("a disabled site must report enabled:false")
	}
	if out.Ticket != nil {
		t.Fatal("a disabled site must not issue a submission ticket")
	}
	// And submitting against it is a 403, not a 404: the caller has proved it
	// holds the key, so there is nothing left to conceal.
	if code, _ := h.do(http.MethodPost, "/api/ingest/"+testSite+"/feedback",
		map[string]any{"message": "hi", "ticket": "x"}, widgetHeaders(h.key)); code != http.StatusForbidden {
		t.Fatalf("submitting to a disabled site = %d, want 403", code)
	}
}

// TestConfigPublishesTheMinimumDwell: the dial has to reach the client that has
// to obey it.
//
// ⚠ The widget's "Send again" mints a replacement ticket and must wait out this
// dwell before posting it, or `checkTiming` refuses the retry as a script —
// every time, so the button can never work. The widget mirrored the 3 000 ms
// default as a constant of its own, which is correct for exactly one value of a
// setting configuration accepts anywhere under 30 s.
func TestConfigPublishesTheMinimumDwell(t *testing.T) {
	cfg := testConfig()
	cfg.MinDwell = 9 * time.Second
	h := newHarness(t, cfg)

	code, body := h.do(http.MethodGet, "/api/ingest/"+testSite+"/feedback/config", nil, widgetHeaders(h.key))
	if code != http.StatusOK {
		t.Fatalf("config = %d, want 200 (%s)", code, body)
	}
	var out WidgetConfig
	mustJSON(t, body, &out)
	if out.MinDwellMs != 9000 {
		t.Fatalf("min_dwell_ms = %d, want 9000 — a client that cannot read the dwell cannot wait it out", out.MinDwellMs)
	}
}

// TestWidgetRoutesAreNotCacheable covers the three public responses, two of
// which carry single-use credentials.
//
// ⚠ None of them sent any cache directive at all, which leaves an intermediary
// free to compute freshness heuristically. A cache that served one reporter's
// ticket to another dead-ends the second dialog with a 422 nobody can act on,
// and a shared upload URL is a write into somebody else's report.
func TestWidgetRoutesAreNotCacheable(t *testing.T) {
	h := newHarness(t, testConfig())
	base := "/api/ingest/" + testSite + "/feedback"

	ref := ""
	cases := []struct {
		name   string
		method string
		path   func() string
		body   func() any
	}{
		{"config", http.MethodGet, func() string { return base + "/config" }, func() any { return nil }},
		{"submit", http.MethodPost, func() string { return base }, func() any { return h.submission() }},
		{"claim", http.MethodPost, func() string { return base + "/" + ref + "/claim" }, func() any { return nil }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(c.method, c.path(), nil)
			if b := c.body(); b != nil {
				raw, err := json.Marshal(b)
				if err != nil {
					t.Fatalf("marshal: %v", err)
				}
				req = httptest.NewRequest(c.method, c.path(), strings.NewReader(string(raw)))
				req.Header.Set("Content-Type", "application/json")
			}
			for k, v := range widgetHeaders(h.key) {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			h.srv.ServeHTTP(rec, req)
			if rec.Code >= 500 {
				t.Fatalf("%s = %d: %s", c.name, rec.Code, rec.Body.String())
			}
			if got := rec.Header().Get("Cache-Control"); got != "no-store" {
				t.Fatalf("%s Cache-Control = %q, want no-store", c.name, got)
			}
			if c.name == "submit" {
				var out Accepted
				mustJSON(t, rec.Body.Bytes(), &out)
				ref = out.Ref
			}
		})
	}
}

// TestConfigWithoutStorageIsDisabled: a deployment with no object storage reports
// every site as disabled, whatever its row says. A switch must not be flippable
// into a state the process cannot serve.
func TestConfigWithoutStorageIsDisabled(t *testing.T) {
	cfg := testConfig()
	cfg.Enabled = false
	h := newHarness(t, cfg)
	h.mod.blobs = nil

	code, body := h.do(http.MethodGet, "/api/ingest/"+testSite+"/feedback/config", nil, widgetHeaders(h.key))
	if code != http.StatusOK {
		t.Fatalf("config = %d, want 200 (%s)", code, body)
	}
	var out WidgetConfig
	mustJSON(t, body, &out)
	if out.Enabled {
		t.Fatal("with no object storage every site must read as disabled")
	}
	// And turning it on is a 503 rather than a setting that silently does nothing.
	code, body = h.do(http.MethodPatch, "/api/sites/"+testSite+"/feedback-config", map[string]any{"enabled": true}, nil)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("enable without storage = %d, want 503 (%s)", code, body)
	}
}

// TestOriginNotRequired: a caller with no Origin header is not a browser, and the
// allow-list has never claimed to stop a shell prompt — the rate limits and the
// kill switch do that. This documents the boundary rather than asserting a hole.
func TestOriginNotRequired(t *testing.T) {
	h := newHarness(t, testConfig())
	code, body := h.do(http.MethodPost, "/api/ingest/"+testSite+"/feedback",
		h.submission(), map[string]string{"X-Widget-Key": h.key})
	if code != http.StatusAccepted {
		t.Fatalf("no-Origin submission = %d, want 202 (%s)", code, body)
	}
}
