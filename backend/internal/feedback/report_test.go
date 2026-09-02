package feedback

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestRefFormat: the reference is quoted back by the reporter, so it must survive
// being read aloud — Crockford base32 with no I, L, O or U.
func TestRefFormat(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		ref, err := NewRef()
		if err != nil {
			t.Fatalf("NewRef: %v", err)
		}
		if !ValidRef(ref) {
			t.Fatalf("NewRef produced %q, which ValidRef rejects", ref)
		}
		if strings.ContainsAny(ref[2:], "ILOU") {
			t.Fatalf("ref %q contains an ambiguous character", ref)
		}
		seen[ref] = true
	}
	if len(seen) < 150 {
		t.Fatalf("only %d distinct refs in 200 draws — generation is not random enough for the retry-on-collision strategy", len(seen))
	}
	for _, bad := range []string{"", "R-", "R-ABC", "R-ABCDE", "X-ABCD", "R-ABCI", "R-abcd", "r-ABCD"} {
		if ValidRef(bad) {
			t.Fatalf("ValidRef accepted %q", bad)
		}
	}
}

// TestRefsAreUniqueAcrossReports exercises the insert path that retries on the
// unique(ref) violation.
func TestRefsAreUniqueAcrossReports(t *testing.T) {
	h := newHarness(t, testConfig())
	seen := map[string]bool{}
	for i := 0; i < 25; i++ {
		code, raw := h.do(http.MethodPost, "/api/ingest/"+testSite+"/feedback", h.submission(), widgetHeaders(h.key))
		if code != http.StatusAccepted {
			t.Fatalf("submit %d = %d (%s)", i, code, raw)
		}
		var acc Accepted
		mustJSON(t, raw, &acc)
		if seen[acc.Ref] {
			t.Fatalf("ref %s was issued twice", acc.Ref)
		}
		seen[acc.Ref] = true
	}
}

// TestTriage covers FR-21: the state machine, the resolved_at stamp and its
// clearing, the internal note, and the validation.
func TestTriage(t *testing.T) {
	h := newHarness(t, testConfig())
	ref := h.submitWithFile(64).Ref

	patchReport := func(body map[string]any) (int, Report) {
		code, raw := h.do(http.MethodPatch, "/api/reports/"+ref, body, nil)
		var out Report
		if code == http.StatusOK {
			mustJSON(t, raw, &out)
		}
		return code, out
	}

	code, rep := patchReport(map[string]any{"state": StateOpen, "internal_note": "asked her for a screenshot"})
	if code != http.StatusOK {
		t.Fatalf("patch = %d, want 200", code)
	}
	if rep.State != StateOpen || rep.InternalNote == nil || *rep.InternalNote == "" {
		t.Fatalf("patched report = %+v", rep)
	}
	if rep.ResolvedAt != nil {
		t.Fatal("open is not a terminal state and must not stamp resolved_at")
	}

	_, rep = patchReport(map[string]any{"state": StateResolved})
	if rep.ResolvedAt == nil {
		t.Fatal("resolved must stamp resolved_at")
	}
	_, rep = patchReport(map[string]any{"state": StateDeclined})
	if rep.ResolvedAt == nil {
		t.Fatal("declined must stamp resolved_at")
	}
	_, rep = patchReport(map[string]any{"state": StateOpen})
	if rep.ResolvedAt != nil {
		t.Fatal("reopening must clear resolved_at")
	}
	_, rep = patchReport(map[string]any{"kind": KindIdea})
	if rep.Kind != KindIdea {
		t.Fatalf("kind = %s, want idea — triage may recategorise", rep.Kind)
	}

	if code, _ := patchReport(map[string]any{"state": "wontfix"}); code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown state = %d, want 422", code)
	}
	if code, _ := patchReport(map[string]any{}); code != http.StatusUnprocessableEntity {
		t.Fatalf("empty patch = %d, want 422", code)
	}
	if code, _ := h.do(http.MethodPatch, "/api/reports/R-0000", map[string]any{"state": StateOpen}, nil); code != http.StatusNotFound {
		t.Fatalf("unknown ref = %d, want 404", code)
	}
}

// TestInboxFiltersAndPages covers FR-20: one queue across every site, newest
// first, keyset cursor, limit clamped 1..200 default 50.
func TestInboxFiltersAndPages(t *testing.T) {
	h := newHarness(t, testConfig())
	h.seedSite("fin", "Fin")
	finKey := h.enable("fin")

	for i := 0; i < 3; i++ {
		h.submitWithFile(64)
	}
	// One report on the other site, so "one queue across every site" is testable.
	finTicket := func() string {
		code, raw := h.do(http.MethodGet, "/api/ingest/fin/feedback/config", nil,
			map[string]string{"X-Widget-Key": finKey, "Origin": testOrigin})
		if code != http.StatusOK {
			t.Fatalf("fin config = %d (%s)", code, raw)
		}
		var cfg WidgetConfig
		mustJSON(t, raw, &cfg)
		return *cfg.Ticket
	}()
	if code, raw := h.do(http.MethodPost, "/api/ingest/fin/feedback",
		map[string]any{"message": "fin is slow", "kind": KindOther, "ticket": finTicket},
		map[string]string{"X-Widget-Key": finKey, "Origin": testOrigin}); code != http.StatusAccepted {
		t.Fatalf("fin submit = %d (%s)", code, raw)
	}

	list := func(query string) ReportPage {
		code, raw := h.do(http.MethodGet, "/api/reports"+query, nil, nil)
		if code != http.StatusOK {
			t.Fatalf("inbox%s = %d (%s)", query, code, raw)
		}
		var page ReportPage
		mustJSON(t, raw, &page)
		return page
	}

	all := list("")
	if len(all.Items) != 4 {
		t.Fatalf("inbox holds %d reports, want 4 across both sites", len(all.Items))
	}
	// Newest first.
	for i := 1; i < len(all.Items); i++ {
		if all.Items[i-1].CreatedAt < all.Items[i].CreatedAt {
			t.Fatal("the inbox is not ordered newest first")
		}
	}
	if got := list("?site=fin"); len(got.Items) != 1 || got.Items[0].SiteID != "fin" {
		t.Fatalf("site filter returned %+v", got.Items)
	}
	if got := list("?kind=other"); len(got.Items) != 1 {
		t.Fatalf("kind filter returned %d items, want 1", len(got.Items))
	}
	if got := list("?state=new"); len(got.Items) != 4 {
		t.Fatalf("state filter returned %d items, want 4", len(got.Items))
	}
	if got := list("?state=resolved"); len(got.Items) != 0 {
		t.Fatalf("no report is resolved yet, got %d", len(got.Items))
	}

	// Keyset paging: two pages of two, with no overlap and no gap.
	first := list("?limit=2")
	if len(first.Items) != 2 || first.NextCursor == nil {
		t.Fatalf("first page = %d items, cursor %v", len(first.Items), first.NextCursor)
	}
	second := list("?limit=2&cursor=" + *first.NextCursor)
	if len(second.Items) != 2 {
		t.Fatalf("second page = %d items, want 2", len(second.Items))
	}
	seen := map[string]bool{}
	for _, r := range append(first.Items, second.Items...) {
		if seen[r.Ref] {
			t.Fatalf("ref %s appeared on both pages", r.Ref)
		}
		seen[r.Ref] = true
	}
	if len(seen) != 4 {
		t.Fatalf("paging covered %d of 4 reports", len(seen))
	}

	// A malformed cursor is a 422, and the filters validate.
	if code, _ := h.do(http.MethodGet, "/api/reports?cursor=not-a-valid-cursor", nil, nil); code != http.StatusUnprocessableEntity {
		t.Fatalf("malformed cursor = %d, want 422", code)
	}
	if code, _ := h.do(http.MethodGet, "/api/reports?state=nonsense", nil, nil); code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown state filter = %d, want 422", code)
	}
	if code, _ := h.do(http.MethodGet, "/api/reports?kind=nonsense", nil, nil); code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown kind filter = %d, want 422", code)
	}
	// A malformed site filter fails like the other two rather than reading, to the
	// operator, as "this site has no reports".
	if code, _ := h.do(http.MethodGet, "/api/reports?site=Not%20A%20Site", nil, nil); code != http.StatusUnprocessableEntity {
		t.Fatalf("malformed site filter = %d, want 422", code)
	}
}

// TestInboxTruncatesTheMessage — openapi's ReportSummary.message is "Truncated in
// the list; the detail carries the whole text". A page of fifty four-thousand
// character reports is a payload no row renders.
func TestInboxTruncatesTheMessage(t *testing.T) {
	cfg := testConfig()
	cfg.MaxTextBytes = 1 << 20 // the body cap would otherwise 413 a long message first
	h := newHarness(t, cfg)
	long := strings.Repeat("é", maxMessageChars) // runes, not bytes

	body := h.submission()
	body["message"] = long
	code, raw := h.do(http.MethodPost, "/api/ingest/"+testSite+"/feedback", body, widgetHeaders(h.key))
	if code != http.StatusAccepted {
		t.Fatalf("submit = %d (%s)", code, raw)
	}
	var acc Accepted
	mustJSON(t, raw, &acc)

	code, raw = h.do(http.MethodGet, "/api/reports", nil, nil)
	if code != http.StatusOK {
		t.Fatalf("inbox = %d (%s)", code, raw)
	}
	var page ReportPage
	mustJSON(t, raw, &page)
	if n := len([]rune(page.Items[0].Message)); n != summaryMessageRunes {
		t.Fatalf("inbox message is %d runes, want the %d-rune preview", n, summaryMessageRunes)
	}
	// ⚠ And the detail still carries every character: the truncation is a list
	// affordance, never a change to what was stored.
	if n := len([]rune(h.report(acc.Ref).Message)); n != maxMessageChars {
		t.Fatalf("the report detail kept %d runes, want the whole %d", n, maxMessageChars)
	}
}

// TestClearTheInternalNote: openapi types ReportPatch.internal_note as
// [string, "null"], so an explicit null is how the dashboard clears one. A
// *string collapses "absent" and "null" into the same nil, which made clearing a
// note a 422.
func TestClearTheInternalNote(t *testing.T) {
	h := newHarness(t, testConfig())
	ref := h.submitWithFile(64).Ref

	code, raw := h.do(http.MethodPatch, "/api/reports/"+ref, map[string]any{"internal_note": "asked her for a screenshot"}, nil)
	if code != http.StatusOK {
		t.Fatalf("set note = %d (%s)", code, raw)
	}
	code, raw = h.do(http.MethodPatch, "/api/reports/"+ref, map[string]any{"internal_note": nil}, nil)
	if code != http.StatusOK {
		t.Fatalf("clearing a note with null = %d, want 200 (%s)", code, raw)
	}
	var rep Report
	mustJSON(t, raw, &rep)
	if rep.InternalNote != nil {
		t.Fatalf("internal_note = %q, want null after an explicit null", *rep.InternalNote)
	}
	// A non-string is still a 422.
	if code, _ := h.do(http.MethodPatch, "/api/reports/"+ref, map[string]any{"internal_note": 7}, nil); code != http.StatusUnprocessableEntity {
		t.Fatalf("a numeric internal_note = %d, want 422", code)
	}
}

// TestAFixableRejectionKeepsTheTicket: the ticket is spent only once the payload
// is known to be acceptable, so a reporter who attaches an unsupported file can
// correct it and send — rather than needing a full page reload for a new ticket.
func TestAFixableRejectionKeepsTheTicket(t *testing.T) {
	h := newHarness(t, testConfig())
	ticket := h.ticket()

	reject := map[string]any{"message": "the board is empty", "ticket": ticket,
		"files": []DeclaredFile{{ContentType: "application/pdf", ByteSize: 10}}}
	if code, raw := h.do(http.MethodPost, "/api/ingest/"+testSite+"/feedback", reject, widgetHeaders(h.key)); code != http.StatusUnprocessableEntity {
		t.Fatalf("unsupported content type = %d, want 422 (%s)", code, raw)
	}
	fixed := map[string]any{"message": "the board is empty", "ticket": ticket}
	if code, raw := h.do(http.MethodPost, "/api/ingest/"+testSite+"/feedback", fixed, widgetHeaders(h.key)); code != http.StatusAccepted {
		t.Fatalf("resubmitting after a correctable 422 = %d, want 202 (%s)", code, raw)
	}
	// And it is still single-use: the corrected submission spent it.
	if code, _ := h.do(http.MethodPost, "/api/ingest/"+testSite+"/feedback", fixed, widgetHeaders(h.key)); code != http.StatusUnprocessableEntity {
		t.Fatal("the ticket outlived the submission that spent it")
	}
}

// TestConsoleTailIsOptIn: ⚠ a console tail from a host app with a private-item
// model could carry a private title into an admin inbox, so lines are dropped
// unless the site opted in (PRD §V3-8).
func TestConsoleTailIsOptIn(t *testing.T) {
	// A larger body cap than production's: the console caps are a server-side
	// bound on what is STORED, and the 413 that a 30 kB body would earn first
	// (tested in the guard chain) would hide them.
	cfg := testConfig()
	cfg.MaxTextBytes = 1 << 20
	h := newHarness(t, cfg)
	body := h.submission()
	body["console_tail"] = []string{"private note: Kája's medical results", "another line"}
	body["last_error"] = "TypeError: x is not a function"

	code, raw := h.do(http.MethodPost, "/api/ingest/"+testSite+"/feedback", body, widgetHeaders(h.key))
	if code != http.StatusAccepted {
		t.Fatalf("submit = %d (%s)", code, raw)
	}
	var acc Accepted
	mustJSON(t, raw, &acc)

	rep := h.report(acc.Ref)
	if len(rep.ConsoleTail) != 0 || rep.LastError != nil {
		t.Fatalf("console lines were stored for a site that never opted in: %+v / %v", rep.ConsoleTail, rep.LastError)
	}

	// With the opt-in on, the lines are kept — and capped.
	on := true
	if _, _, err := h.mod.store.UpsertConfig(context.Background(), testSite, nil, &on, "", time.Now().UTC()); err != nil {
		t.Fatalf("opt in: %v", err)
	}
	body = h.submission()
	long := strings.Repeat("x", 500)
	lines := make([]string, 80)
	for i := range lines {
		lines[i] = long
	}
	body["console_tail"] = lines
	code, raw = h.do(http.MethodPost, "/api/ingest/"+testSite+"/feedback", body, widgetHeaders(h.key))
	if code != http.StatusAccepted {
		t.Fatalf("submit = %d (%s)", code, raw)
	}
	mustJSON(t, raw, &acc)
	rep = h.report(acc.Ref)
	if len(rep.ConsoleTail) != maxConsoleLines {
		t.Fatalf("console tail kept %d lines, want the %d-line cap", len(rep.ConsoleTail), maxConsoleLines)
	}
	if len([]rune(rep.ConsoleTail[0])) != maxConsoleLineRune {
		t.Fatalf("console line kept %d runes, want the %d-rune cap", len([]rune(rep.ConsoleTail[0])), maxConsoleLineRune)
	}
}

// TestClientIPIsHashedNeverStored: only the digest reaches a row, and it is
// useless without the deployment's salt.
func TestClientIPIsHashedNeverStored(t *testing.T) {
	h := newHarness(t, testConfig())
	acc := h.submitWithFile(64)

	var ipHash string
	if err := h.db.QueryRow(`SELECT ip_hash FROM feedback_report WHERE ref = ?`, acc.Ref).Scan(&ipHash); err != nil {
		t.Fatalf("read ip_hash: %v", err)
	}
	if ipHash == "" {
		t.Fatal("expected an ip_hash to be recorded")
	}
	if strings.Contains(ipHash, testIP) {
		t.Fatalf("ip_hash %q contains the client IP — the IP itself must never reach a row", ipHash)
	}
	// The whole row must not carry the address anywhere else either.
	var dump string
	if err := h.db.QueryRow(
		`SELECT COALESCE(message,'') || COALESCE(page_url,'') || COALESCE(user_agent,'') || COALESCE(reporter_label,'') || ip_hash
		   FROM feedback_report WHERE ref = ?`, acc.Ref).Scan(&dump); err != nil {
		t.Fatalf("dump row: %v", err)
	}
	if strings.Contains(dump, testIP) {
		t.Fatal("the client IP appears in the stored report")
	}
}

// TestRotateWidgetKeyInvalidatesTheOldOne — FR-15. That it leaves crash ingest
// alone is asserted end to end in internal/apitest, where both modules run.
func TestRotateWidgetKeyInvalidatesTheOldOne(t *testing.T) {
	h := newHarness(t, testConfig())
	old := h.key

	code, raw := h.do(http.MethodPost, "/api/sites/"+testSite+"/rotate-widget-key", nil, nil)
	if code != http.StatusOK {
		t.Fatalf("rotate = %d (%s)", code, raw)
	}
	var out map[string]string
	mustJSON(t, raw, &out)
	fresh := out["widget_key"]
	if !strings.HasPrefix(fresh, "wk_") || fresh == old {
		t.Fatalf("rotate returned %q", fresh)
	}
	if code, _ := h.do(http.MethodGet, "/api/ingest/"+testSite+"/feedback/config", nil, widgetHeaders(old)); code != http.StatusUnauthorized {
		t.Fatalf("the old key still works (%d) — rotation must kill it immediately", code)
	}
	if code, _ := h.do(http.MethodGet, "/api/ingest/"+testSite+"/feedback/config", nil, widgetHeaders(fresh)); code != http.StatusOK {
		t.Fatal("the new key does not work")
	}
}

// TestSiteConfigRoutes covers FR-14: absence means off, the plaintext is shown
// exactly once, and an unknown site is a 404.
func TestSiteConfigRoutes(t *testing.T) {
	h := newHarness(t, testConfig())
	h.seedSite("karel", "Karel") // no configuration row at all

	code, raw := h.do(http.MethodGet, "/api/sites/karel/feedback-config", nil, nil)
	if code != http.StatusOK {
		t.Fatalf("config for a site with no row = %d, want 200 (%s)", code, raw)
	}
	var cfg FeedbackConfig
	mustJSON(t, raw, &cfg)
	if cfg.Enabled || cfg.SiteID != "karel" || cfg.WidgetKeySetAt != nil {
		t.Fatalf("absent configuration should read as off: %+v", cfg)
	}

	code, raw = h.do(http.MethodPatch, "/api/sites/karel/feedback-config", map[string]any{"enabled": true}, nil)
	if code != http.StatusOK {
		t.Fatalf("enable = %d (%s)", code, raw)
	}
	var first FeedbackConfigWithKey
	mustJSON(t, raw, &first)
	if !strings.HasPrefix(first.WidgetKey, "wk_") {
		t.Fatalf("first enable must return the plaintext key once, got %q", first.WidgetKey)
	}
	if !first.Enabled {
		t.Fatal("the site should be enabled")
	}

	// Never again: a second patch returns no key, and the stored value is a hash.
	code, raw = h.do(http.MethodPatch, "/api/sites/karel/feedback-config", map[string]any{"console_capture": true}, nil)
	if code != http.StatusOK {
		t.Fatalf("second patch = %d (%s)", code, raw)
	}
	var second FeedbackConfigWithKey
	mustJSON(t, raw, &second)
	if second.WidgetKey != "" {
		t.Fatal("the widget key must be shown exactly once and never again")
	}
	if !second.ConsoleCapture {
		t.Fatal("console_capture was not applied")
	}
	if !second.Enabled {
		t.Fatal("a patch that omits `enabled` must not turn the site off")
	}
	var stored string
	if err := h.db.QueryRow(`SELECT widget_key_hash FROM feedback_site_config WHERE site_id = 'karel'`).Scan(&stored); err != nil {
		t.Fatalf("read hash: %v", err)
	}
	if stored == first.WidgetKey {
		t.Fatal("the plaintext key was stored — only its SHA-256 may be")
	}

	if code, _ := h.do(http.MethodGet, "/api/sites/nope/feedback-config", nil, nil); code != http.StatusNotFound {
		t.Fatalf("unknown site = %d, want 404", code)
	}
	if code, _ := h.do(http.MethodPatch, "/api/sites/nope/feedback-config", map[string]any{"enabled": true}, nil); code != http.StatusNotFound {
		t.Fatalf("patching an unknown site = %d, want 404", code)
	}
	if code, _ := h.do(http.MethodPatch, "/api/sites/karel/feedback-config", map[string]any{}, nil); code != http.StatusUnprocessableEntity {
		t.Fatalf("empty patch = %d, want 422", code)
	}
}

// report reads one report through the gated route.
func (h *harness) report(ref string) Report {
	h.t.Helper()
	code, raw := h.do(http.MethodGet, "/api/reports/"+ref, nil, nil)
	if code != http.StatusOK {
		h.t.Fatalf("get report %s = %d (%s)", ref, code, raw)
	}
	var rep Report
	mustJSON(h.t, raw, &rep)
	return rep
}
