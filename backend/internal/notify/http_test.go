package notify

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kareltilcer/ws-tilcer-status/backend/internal/notify/mail"
)

func settingsBody(enabled bool, recipients ...string) map[string]any {
	if recipients == nil {
		recipients = []string{}
	}
	return map[string]any{
		"enabled":    enabled,
		"recipients": recipients,
		"events":     map[string]bool{"crash": true, "feedback": false, "downtime": true},
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	h := newHarness(t)
	code, body := h.do("GET", "/api/notifications/settings", nil, nil)
	if code != 200 {
		t.Fatalf("get: %d %s", code, body)
	}
	var def Settings
	mustJSON(t, body, &def)
	if !def.Available || def.Enabled || def.Recipients == nil || len(def.Recipients) != 0 ||
		!def.Events.Crash || !def.Events.Feedback || !def.Events.Downtime || def.UpdatedAt != nil ||
		def.DigestWindowSeconds != 120 || def.MaxPerHour != 6 || def.Provider == nil || *def.Provider != "fake" {
		t.Fatalf("defaults = %s", body)
	}

	code, body = h.do("PUT", "/api/notifications/settings",
		settingsBody(true, " karel@example.test ", "KAREL@example.test", "", "partner@example.test"), nil)
	if code != 200 {
		t.Fatalf("put: %d %s", code, body)
	}
	var saved Settings
	mustJSON(t, body, &saved)
	if !saved.Enabled || strings.Join(saved.Recipients, ",") != "karel@example.test,partner@example.test" ||
		saved.Events.Feedback || saved.UpdatedAt == nil {
		t.Fatalf("saved = %s", body)
	}
}

func TestSettingsValidation(t *testing.T) {
	h := newHarness(t)
	bad := []map[string]any{
		settingsBody(false, "Karel <karel@example.test>"),
		settingsBody(false, "karel@example.test\r\nBcc: x@example.test"),
		settingsBody(false, "not-an-address"),
		settingsBody(false, "a@x.test", "b@x.test", "c@x.test", "d@x.test", "e@x.test", "f@x.test"),
		settingsBody(false, strings.Repeat("a", 250)+"@x.test"),
		settingsBody(true), // on with nobody to mail
		{"enabled": true, "recipients": []string{"a@x.test"}},                                                             // events missing
		{"enabled": true, "recipients": []string{"a@x.test"}, "events": map[string]bool{"crash": true, "feedback": true}}, // downtime missing
		{"enabled": true, "recipients": []string{"a@x.test"}, "events": map[string]bool{"crash": true}, "surprise": 1},    // unknown field
	}
	for i, b := range bad {
		if code, body := h.do("PUT", "/api/notifications/settings", b, nil); code != http.StatusUnprocessableEntity {
			t.Errorf("case %d: %d %s, want 422", i, code, body)
		}
	}
	if st, _ := loadSettings(context.Background(), h.db); st.Enabled || len(st.Recipients) != 0 {
		t.Fatalf("a refused PUT changed the settings: %+v", st)
	}
}

func TestEnablingWithoutAProviderIs503(t *testing.T) {
	h := newHarnessWith(t, testConfig(), false)
	code, body := h.do("PUT", "/api/notifications/settings", settingsBody(true, "karel@example.test"), nil)
	if code != http.StatusServiceUnavailable || !strings.Contains(string(body), "mail_unconfigured") {
		t.Fatalf("enable without provider: %d %s", code, body)
	}
	// Saving the addresses with the switch off is fine.
	if code, body := h.do("PUT", "/api/notifications/settings", settingsBody(false, "karel@example.test"), nil); code != 200 {
		t.Fatalf("save while off: %d %s", code, body)
	}
	code, body = h.do("GET", "/api/notifications/settings", nil, nil)
	var st Settings
	mustJSON(t, body, &st)
	if code != 200 || st.Available || st.Provider != nil {
		t.Fatalf("settings without provider: %s", body)
	}
	if code, _ := h.do("POST", "/api/notifications/test", nil, nil); code != http.StatusServiceUnavailable {
		t.Fatalf("test send without provider: %d, want 503", code)
	}
}

func TestMutesAreIdempotent(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < 2; i++ {
		if code, body := h.do("PUT", "/api/notifications/muted-sites/home", nil, nil); code != http.StatusNoContent {
			t.Fatalf("mute #%d: %d %s", i, code, body)
		}
	}
	_, body := h.do("GET", "/api/notifications/settings", nil, nil)
	var st Settings
	mustJSON(t, body, &st)
	if strings.Join(st.MutedSites, ",") != "home" {
		t.Fatalf("muted = %v", st.MutedSites)
	}
	for i := 0; i < 2; i++ {
		if code, body := h.do("DELETE", "/api/notifications/muted-sites/home", nil, nil); code != http.StatusNoContent {
			t.Fatalf("unmute #%d: %d %s", i, code, body)
		}
	}
	for _, path := range []string{"/api/notifications/muted-sites/nope", "/api/notifications/muted-sites/BAD%20ID"} {
		if code, _ := h.do("PUT", path, nil, nil); code != http.StatusNotFound {
			t.Fatalf("mute %s: %d, want 404", path, code)
		}
	}
}

func TestTestSend(t *testing.T) {
	h := newHarness(t)
	if code, body := h.do("POST", "/api/notifications/test", nil, nil); code != http.StatusUnprocessableEntity ||
		!strings.Contains(string(body), "no_recipients") {
		t.Fatalf("with no recipients: %d %s", code, body)
	}
	// Saved but switched off: the test still goes, because proving the address
	// works is what it is for.
	h.setSettings(settings{Recipients: []string{"karel@example.test"}, OnCrash: true})
	code, body := h.do("POST", "/api/notifications/test", nil, nil)
	if code != 200 {
		t.Fatalf("test send: %d %s", code, body)
	}
	var res TestResult
	mustJSON(t, body, &res)
	sent := h.mail.Sent()
	if res.ProviderMessageID == "" || len(sent) != 1 || sent[0].To[0] != "karel@example.test" ||
		!strings.Contains(sent[0].Text, "OFF") || sent[0].IdempotencyKey == "" {
		t.Fatalf("result %s, sent %+v", body, sent)
	}
	if n := len(h.allDigests()); n != 0 {
		t.Fatal("a test email went through the outbox")
	}

	h.mail.Fail(&mail.SendError{Status: 403, Code: "validation_error", Detail: "The tilcer.cz domain is not verified"})
	code, body = h.do("POST", "/api/notifications/test", nil, nil)
	if code != http.StatusBadGateway || !strings.Contains(string(body), "not verified") {
		t.Fatalf("refused test send: %d %s", code, body)
	}
	// The burst is three; the fourth quick press is refused.
	code, _ = h.do("POST", "/api/notifications/test", nil, nil)
	if code == http.StatusTooManyRequests {
		t.Fatal("rate limited on the third press")
	}
	if code, _ := h.do("POST", "/api/notifications/test", nil, nil); code != http.StatusTooManyRequests {
		t.Fatalf("fourth press: %d, want 429", code)
	}
}

func TestDeliveriesList(t *testing.T) {
	h := newHarness(t)
	h.enable("karel@example.test")
	h.queueCrash(t0)
	h.mail.Fail(&mail.SendError{Status: 500, Detail: "boom"})
	h.run(t0.Add(2 * time.Minute))

	code, body := h.do("GET", "/api/notifications/deliveries?limit=500", nil, nil)
	if code != 200 {
		t.Fatalf("deliveries: %d %s", code, body)
	}
	var page DeliveryPage
	mustJSON(t, body, &page)
	if len(page.Items) != 1 {
		t.Fatalf("items = %s", body)
	}
	d := page.Items[0]
	if d.State != DigestPending || d.Attempts != 1 || d.NextAttemptAt == nil || d.LastError == nil ||
		len(d.Recipients) != 1 || d.EventCount != 1 {
		t.Fatalf("delivery = %s", body)
	}
}

// TestNonAdminsReadWithoutAddressesAndCannotWrite: the page is readable by any
// session, but the recipients are personal data and every write is admin-only.
func TestNonAdminsReadWithoutAddressesAndCannotWrite(t *testing.T) {
	h := newHarness(t)
	h.enable("karel@example.test")
	h.queueCrash(t0)
	h.run(t0.Add(3 * time.Minute))
	viewer := map[string]string{"X-Test-Roles": "viewer"}

	code, body := h.do("GET", "/api/notifications/settings", nil, viewer)
	if code != 200 || !strings.Contains(string(body), `"recipients":null`) || strings.Contains(string(body), "karel@") {
		t.Fatalf("viewer settings: %d %s", code, body)
	}
	code, body = h.do("GET", "/api/notifications/deliveries", nil, viewer)
	if code != 200 || strings.Contains(string(body), "karel@") {
		t.Fatalf("viewer deliveries: %d %s", code, body)
	}
	writes := []struct{ method, path string }{
		{"PUT", "/api/notifications/settings"},
		{"PUT", "/api/notifications/muted-sites/home"},
		{"DELETE", "/api/notifications/muted-sites/home"},
		{"POST", "/api/notifications/test"},
	}
	for _, w := range writes {
		if code, _ := h.do(w.method, w.path, settingsBody(false), viewer); code != http.StatusForbidden {
			t.Errorf("viewer %s %s: %d, want 403", w.method, w.path, code)
		}
	}
}

func TestValidateRecipients(t *testing.T) {
	got, err := validateRecipients([]string{"a@x.test", " A@X.test", "", "b@x.test"})
	if err != nil || strings.Join(got, ",") != "a@x.test,b@x.test" {
		t.Fatalf("got %v, %v", got, err)
	}
	for _, bad := range []string{"a@x.test, b@x.test", "<a@x.test>", "a@x.test (work)", "@", "a b@x.test"} {
		if _, err := validateRecipients([]string{bad}); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

// --- the upgrade seed -----------------------------------------------------------

// TestTheUpgradeSeedDoesNotMailHistory: on a database that already has crash
// groups, a group that has had a qualifying event is old news and is recorded as
// announced; a group only ever seen outside production stays armed.
func TestTheUpgradeSeedDoesNotMailHistory(t *testing.T) {
	db := openDB(t)
	migrate(t, db, false) // the schema before this module
	if _, err := db.Exec(`INSERT INTO site (id, name, monitor_enabled, expected_status, crash_window_hours, ingest_key_hash, cached_color, created_at)
		VALUES ('home', 'Home', 0, 200, 24, 'h', 'unknown', ?)`, ts(t0)); err != nil {
		t.Fatal(err)
	}
	group := func(fp string, events ...[2]any) int64 {
		res, err := db.Exec(`INSERT INTO crash_group (site_id, fingerprint, title, level, count, status, first_seen, last_seen)
			VALUES ('home', ?, 't', 'error', 1, 'open', ?, ?)`, fp, ts(t0), ts(t0))
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		for _, e := range events {
			if _, err := db.Exec(`INSERT INTO crash_event (site_id, group_id, level, message, environment, occurred_at, received_at)
				VALUES ('home', ?, ?, 'm', ?, ?, ?)`, id, e[0], e[1], ts(t0), ts(t0)); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}
	prod := group("prod", [2]any{"error", "prod"})
	unset := group("unset", [2]any{"fatal", nil})
	devOnly := group("dev", [2]any{"error", "dev"})
	warnOnly := group("warn", [2]any{"warning", "prod"})

	migrate(t, db, true)
	seeded := func(id int64) bool {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM notify_crash_state WHERE group_id = ? AND armed = 0 AND announced = 1`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n == 1
	}
	if !seeded(prod) || !seeded(unset) {
		t.Fatal("a group with a production error was left armed: switching notifications on would mail it")
	}
	if seeded(devOnly) || seeded(warnOnly) {
		t.Fatal("a group that was never news was recorded as announced")
	}
}
