package notify

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kareltilcer/ws-tilcer-status/backend/internal/crash"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/feedback"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/notify/mail"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/sites"
)

// queueCrash queues one new-crash event at `at`.
func (h *harness) queueCrash(at time.Time) {
	h.t.Helper()
	h.crash(newCrash(h.seedGroup("home", crash.StatusOpen), at))
}

// TestADigestWaitsForItsWindow: nothing goes out before the oldest event has
// waited the window out, and whatever arrived meanwhile goes in the same email.
func TestADigestWaitsForItsWindow(t *testing.T) {
	h := newHarness(t)
	h.enable("karel@example.test")
	h.queueCrash(t0)
	h.report(feedback.ReportSignal{SiteID: "fin", Ref: "R-7QK2", Kind: "bug", Message: "Empty board", Attachments: 2, At: t0.Add(90 * time.Second)})

	h.run(t0.Add(time.Minute))
	if n := len(h.mail.Attempts()); n != 0 {
		t.Fatalf("%d emails before the window closed", n)
	}
	h.run(t0.Add(2 * time.Minute))
	sent := h.mail.Sent()
	if len(sent) != 1 {
		t.Fatalf("sent %d emails, want one digest", len(sent))
	}
	m := sent[0]
	if !strings.HasPrefix(m.Subject, "[status] 2 updates") {
		t.Fatalf("subject = %q", m.Subject)
	}
	if len(m.To) != 1 || m.To[0] != "karel@example.test" || m.From != "status <status@example.test>" {
		t.Fatalf("envelope = from %q to %v", m.From, m.To)
	}
	if !strings.Contains(m.Text, testPublicURL+"/reports/R-7QK2") || !strings.Contains(m.Text, testPublicURL+"/crashes/") {
		t.Fatalf("text is missing a link:\n%s", m.Text)
	}
	if !strings.HasPrefix(m.IdempotencyKey, "status-digest-") {
		t.Fatalf("idempotency key = %q", m.IdempotencyKey)
	}
	ds := h.allDigests()
	if len(ds) != 1 || ds[0].State != DigestSent || ds[0].EventCount != 2 || ds[0].Attempts != 1 {
		t.Fatalf("digest = %+v", ds)
	}
	if got := h.queued(); len(got) != 0 {
		t.Fatalf("events still queued after the digest: %v", got)
	}
}

// TestTheHourlyCapDelaysAndNeverDrops: at the cap, events keep collecting and go
// out together once the hour allows.
func TestTheHourlyCapDelaysAndNeverDrops(t *testing.T) {
	cfg := testConfig()
	cfg.MaxPerHour = 2
	h := newHarnessWith(t, cfg, true)
	h.enable("karel@example.test")

	h.queueCrash(t0)
	h.run(t0.Add(2 * time.Minute)) // digest 1
	h.queueCrash(t0.Add(3 * time.Minute))
	h.run(t0.Add(5 * time.Minute)) // digest 2
	h.queueCrash(t0.Add(6 * time.Minute))
	h.run(t0.Add(8 * time.Minute)) // capped
	h.queueCrash(t0.Add(10 * time.Minute))
	h.run(t0.Add(30 * time.Minute)) // still capped
	if n := len(h.mail.Sent()); n != 2 {
		t.Fatalf("sent %d, want 2 while capped", n)
	}
	if got := h.queued(); len(got) != 2 {
		t.Fatalf("queued %v while capped, want both events kept", got)
	}

	h.run(t0.Add(63 * time.Minute)) // digest 1 has left the hour
	sent := h.mail.Sent()
	if len(sent) != 3 || !strings.HasPrefix(sent[2].Subject, "[status] 2 updates") {
		t.Fatalf("after the cap: sent %d, last subject %q", len(sent), sent[len(sent)-1].Subject)
	}
}

// TestAssemblyDropsWhatIsNoLongerWanted: a site muted after its event was queued
// is not mailed about.
func TestAssemblyDropsWhatIsNoLongerWanted(t *testing.T) {
	h := newHarness(t)
	h.enable("karel@example.test")
	h.queueCrash(t0)
	if err := setMuted(context.Background(), h.db, "home", true, ts(t0)); err != nil {
		t.Fatal(err)
	}
	h.run(t0.Add(3 * time.Minute))
	if n := len(h.mail.Attempts()); n != 0 {
		t.Fatalf("sent %d emails about a muted site", n)
	}
	if n := h.count(`SELECT COUNT(*) FROM notify_event`); n != 0 {
		t.Fatalf("%d dropped events remain", n)
	}
	if n := len(h.allDigests()); n != 0 {
		t.Fatalf("%d empty digests were created", n)
	}
}

// TestAPendingDigestHoldsTheNextOneBack: while a digest waits on a retry, what
// arrives meanwhile collects in the outbox instead of becoming digests of its
// own. The cap counts digests created, so without this a provider that is down
// for hours would receive one email per window the moment it answered again.
func TestAPendingDigestHoldsTheNextOneBack(t *testing.T) {
	h := newHarness(t)
	h.enable("karel@example.test")
	h.queueCrash(t0)
	h.mail.Fail(&mail.SendError{Status: 503, Detail: "unavailable", RetryAfter: time.Hour})
	h.run(t0.Add(2 * time.Minute)) // digest 1 fails; next attempt in an hour

	h.queueCrash(t0.Add(10 * time.Minute))
	h.run(t0.Add(13 * time.Minute))
	h.report(feedback.ReportSignal{SiteID: "fin", Ref: "R-7QK2", Kind: "bug", Message: "x", At: t0.Add(20 * time.Minute)})
	h.run(t0.Add(23 * time.Minute))
	if ds := h.allDigests(); len(ds) != 1 || ds[0].State != DigestPending {
		t.Fatalf("digests while one is pending = %+v, want only the pending one", ds)
	}
	if got := h.queued(); len(got) != 2 {
		t.Fatalf("queued %v while a digest is pending, want both events held", got)
	}

	h.run(t0.Add(62 * time.Minute)) // the retry goes out
	h.run(t0.Add(62*time.Minute + 15*time.Second))
	sent := h.mail.Sent()
	if len(sent) != 2 || !strings.HasPrefix(sent[1].Subject, "[status] 2 updates") {
		t.Fatalf("after recovery: %d emails, want the retry and ONE digest of what was held (%v)", len(sent), sent)
	}
	if got := h.queued(); len(got) != 0 {
		t.Fatalf("queued %v after recovery", got)
	}
}

// TestAChangedEnvelopeSupersedesAPendingDigest: a digest refused for its
// envelope — an unverified sender, a provider that mails only its owner — must
// not keep retrying that envelope for 23 hours, holding everything behind it,
// once the sender or the recipients are fixed. Its notifications go out again,
// once, to the envelope as it is now.
func TestAChangedEnvelopeSupersedesAPendingDigest(t *testing.T) {
	refused := &mail.SendError{Status: 403, Code: "validation_error", Detail: "domain is not verified"}
	for _, tc := range []struct {
		name   string
		change func(h *harness)
		wantTo string
		from   string
	}{
		{"recipients", func(h *harness) { h.enable("owner@example.test") }, "owner@example.test", "status <status@example.test>"},
		{"sender", func(h *harness) { h.mod.worker.cfg.From = "status <status@verified.example.test>" }, "karel@example.test", "status <status@verified.example.test>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.enable("karel@example.test")
			h.queueCrash(t0)
			h.mail.Fail(refused)
			h.run(t0.Add(2 * time.Minute)) // refused; retrying

			tc.change(h)
			h.run(t0.Add(4 * time.Minute)) // the retry would be due now
			ds := h.allDigests()
			if len(ds) != 2 || ds[0].State != DigestFailed || ds[0].LastError == nil || *ds[0].LastError != reasonSuperseded {
				t.Fatalf("digests after the %s changed = %+v, want the stale one superseded", tc.name, ds)
			}
			if ds[1].State != DigestSent || ds[1].EventCount != 1 {
				t.Fatalf("the re-sent digest = %+v", ds[1])
			}
			sent := h.mail.Sent()
			if len(sent) != 1 || len(sent[0].To) != 1 || sent[0].To[0] != tc.wantTo || sent[0].From != tc.from {
				t.Fatalf("sent %+v, want one email from %q to %q", sent, tc.from, tc.wantTo)
			}
			if at := h.mail.Attempts(); at[0].IdempotencyKey == at[1].IdempotencyKey {
				t.Fatal("the new envelope reused the stale digest's idempotency key, which the provider refuses")
			}
			if got := h.queued(); len(got) != 0 {
				t.Fatalf("queued %v after the re-send", got)
			}
		})
	}
}

// TestSwitchingOffCancelsWhatIsPending: "off" means off — a digest waiting on a
// retry does not go out when notifications are switched back on next week.
func TestSwitchingOffCancelsWhatIsPending(t *testing.T) {
	h := newHarness(t)
	h.enable("karel@example.test")
	h.queueCrash(t0)
	h.mail.Fail(&mail.SendError{Status: 500, Detail: "internal"})
	h.run(t0.Add(2 * time.Minute))

	h.setSettings(settings{Enabled: false, Recipients: []string{"karel@example.test"}, OnCrash: true, OnFeedback: true, OnDowntime: true})
	h.run(t0.Add(10 * time.Minute))
	ds := h.allDigests()
	if len(ds) != 1 || ds[0].State != DigestFailed || ds[0].LastError == nil || !strings.Contains(*ds[0].LastError, "turned off") {
		t.Fatalf("digest after switching off = %+v", ds)
	}
	if n := len(h.mail.Attempts()); n != 1 {
		t.Fatalf("%d attempts, want only the failed one", n)
	}
}

// TestBootingWithoutAProviderDropsTheBacklog: removing STATUS_RESEND_API_KEY is
// switching off, not pausing. What an earlier deployment left — a digest waiting
// on a retry, and the "down" it was holding back — is dropped when the service
// boots without a provider, so putting the key back weeks later does not mail a
// three-week-old outage as news. With a provider the same step touches nothing.
func TestBootingWithoutAProviderDropsTheBacklog(t *testing.T) {
	h := newHarness(t)
	h.enable("karel@example.test")
	h.queueCrash(t0)
	h.mail.Fail(&mail.SendError{Status: 500, Detail: "internal"})
	h.run(t0.Add(2 * time.Minute))                          // pending, retrying
	h.check(check(false, sites.Red, t0.Add(3*time.Minute))) // held behind it

	if err := h.mod.DropBacklog(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ds := h.allDigests(); len(ds) != 1 || ds[0].State != DigestPending || len(h.queued()) != 1 {
		t.Fatalf("with a provider the boot step changed the queue: %+v, queued %v", ds, h.queued())
	}

	// The next deployment boots without a key.
	bare := NewModule(h.db, sites.NewStore(h.db, 2), nil, testConfig(), discardLogger())
	if err := bare.DropBacklog(context.Background()); err != nil {
		t.Fatal(err)
	}
	ds := h.allDigests()
	if len(ds) != 1 || ds[0].State != DigestFailed || ds[0].LastError == nil || *ds[0].LastError != reasonNoProvider {
		t.Fatalf("pending digest after a boot without a provider = %+v", ds)
	}
	if got := h.queued(); len(got) != 0 {
		t.Fatalf("queued %v after a boot without a provider, want nothing", got)
	}

	// …and the one after that has the key again, three weeks on.
	later := t0.AddDate(0, 0, 21)
	h.run(later)
	h.run(later.Add(15 * time.Second))
	if n := len(h.mail.Attempts()); n != 1 {
		t.Fatalf("%d send attempts once the key was back, want only the original failed one", n)
	}
}

// TestARetryResendsTheIdenticalRequest: the provider's idempotency key refuses
// the same key with a different body, so a retry must be byte-for-byte the
// first attempt — and it waits out the backoff first.
func TestARetryResendsTheIdenticalRequest(t *testing.T) {
	h := newHarness(t)
	h.enable("karel@example.test")
	h.queueCrash(t0)
	h.mail.Fail(&mail.SendError{Status: 503, Detail: "unavailable"})
	h.run(t0.Add(2 * time.Minute))

	h.run(t0.Add(2*time.Minute + 30*time.Second))
	if n := len(h.mail.Attempts()); n != 1 {
		t.Fatalf("%d attempts inside the backoff, want 1", n)
	}
	// A second site's event, arriving meanwhile, must not change the queued email.
	h.report(feedback.ReportSignal{SiteID: "fin", Ref: "R-7QK2", Kind: "bug", Message: "x", At: t0.Add(150 * time.Second)})

	h.run(t0.Add(3*time.Minute + time.Second))
	at := h.mail.Attempts()
	if len(at) < 2 {
		t.Fatalf("%d attempts after the backoff, want the retry", len(at))
	}
	a, b := at[0], at[1]
	if a.Subject != b.Subject || a.Text != b.Text || a.HTML != b.HTML || a.IdempotencyKey != b.IdempotencyKey ||
		strings.Join(a.To, ",") != strings.Join(b.To, ",") || a.From != b.From {
		t.Fatalf("the retry differs from the first attempt:\n%+v\n%+v", a, b)
	}
	ds := h.allDigests()
	if ds[0].State != DigestSent || ds[0].Attempts != 2 {
		t.Fatalf("digest after the retry = %+v", ds[0])
	}
}

func TestAPermanentRefusalGivesUp(t *testing.T) {
	h := newHarness(t)
	h.enable("karel@example.test")
	h.queueCrash(t0)
	h.mail.Fail(&mail.SendError{Status: 422, Code: "validation_error", Detail: "invalid `to`"})
	h.run(t0.Add(2 * time.Minute))
	h.run(t0.Add(3 * time.Hour))
	ds := h.allDigests()
	if len(ds) != 1 || ds[0].State != DigestFailed || ds[0].Attempts != 1 {
		t.Fatalf("digest = %+v, want failed after one attempt", ds)
	}
	if n := len(h.mail.Attempts()); n != 1 {
		t.Fatalf("%d attempts, want no retry of a permanent refusal", n)
	}
}

// TestAConfigurationRefusalKeepsRetrying: 401/403 — and a sender Resend cannot
// parse — are fixed in the environment, not by changing the email: the digest
// keeps its place.
func TestAConfigurationRefusalKeepsRetrying(t *testing.T) {
	for _, refusal := range []*mail.SendError{
		{Status: 403, Code: "validation_error", Detail: "domain not verified"},
		{Status: 422, Code: "invalid_from_address", Detail: "Invalid `from` field."},
	} {
		t.Run(fmt.Sprintf("%d-%s", refusal.Status, refusal.Code), func(t *testing.T) {
			h := newHarness(t)
			h.enable("karel@example.test")
			h.queueCrash(t0)
			h.mail.Fail(refusal)
			h.run(t0.Add(2 * time.Minute))
			ds := h.allDigests()
			if ds[0].State != DigestPending || ds[0].Attempts != 1 {
				t.Fatalf("digest = %+v, want pending after %v", ds[0], refusal)
			}
			h.run(t0.Add(4 * time.Minute))
			if ds := h.allDigests(); ds[0].State != DigestSent {
				t.Fatalf("digest = %+v, want sent once the environment is fixed", ds[0])
			}
		})
	}
}

func TestRetryAfterIsHonoured(t *testing.T) {
	h := newHarness(t)
	h.enable("karel@example.test")
	h.queueCrash(t0)
	h.mail.Fail(&mail.SendError{Status: 429, Code: "rate_limit_exceeded", RetryAfter: 10 * time.Minute})
	at := t0.Add(2 * time.Minute)
	h.run(at)
	ds := h.allDigests()
	if want := ts(at.Add(10 * time.Minute)); ds[0].NextAttemptAt != want {
		t.Fatalf("next attempt = %s, want %s (Retry-After beats the 1m backoff)", ds[0].NextAttemptAt, want)
	}
}

// TestRetryAfterIsCappedAtTheLongestBackoff: a pending digest holds the next one
// back, and expiry is checked only when a digest is due — so a provider's
// 30-hour hint, honoured, would hold every notification for 30 hours and expire
// the digest a day late. Capped, it is retried hourly and expires on schedule.
func TestRetryAfterIsCappedAtTheLongestBackoff(t *testing.T) {
	h := newHarness(t)
	h.enable("karel@example.test")
	h.queueCrash(t0)
	h.mail.Fail(&mail.SendError{Status: 429, Code: "daily_quota_exceeded", RetryAfter: 30 * time.Hour})
	at := t0.Add(2 * time.Minute)
	h.run(at)
	if ds, want := h.allDigests(), ts(at.Add(time.Hour)); ds[0].NextAttemptAt != want {
		t.Fatalf("next attempt = %s, want %s (the longest backoff step)", ds[0].NextAttemptAt, want)
	}
}

// cancellingMailer accepts every message and cancels the worker's context while
// doing so: a provider answering 200 just as shutdown begins.
type cancellingMailer struct{ cancel context.CancelFunc }

func (m cancellingMailer) Provider() string { return "fake" }

func (m cancellingMailer) Send(context.Context, mail.Message) (mail.Result, error) {
	m.cancel()
	return mail.Result{ID: "accepted-at-shutdown"}, nil
}

// TestASendAcceptedAsShutdownBeginsIsRecorded: the provider's acceptance is
// known, so it is recorded. Left pending, a boot more than 23 hours later would
// mark an email that did arrive as expired.
func TestASendAcceptedAsShutdownBeginsIsRecorded(t *testing.T) {
	h := newHarness(t)
	h.enable("karel@example.test")
	h.queueCrash(t0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.mod.worker.mailer = cancellingMailer{cancel: cancel}
	h.mod.worker.RunOnce(ctx, t0.Add(2*time.Minute))
	if ds := h.allDigests(); len(ds) != 1 || ds[0].State != DigestSent || ds[0].Attempts != 1 || ds[0].SentAt == nil {
		t.Fatalf("digest accepted as shutdown began = %+v, want it recorded as sent", ds)
	}
}

// TestATimeoutKeepsTheDigestPending: whether a timed-out request was delivered is
// unknown; the answer is to retry under the same key, which the provider
// deduplicates.
func TestATimeoutKeepsTheDigestPending(t *testing.T) {
	h := newHarness(t)
	h.enable("karel@example.test")
	h.queueCrash(t0)
	h.mail.Fail(fmt.Errorf("post: %w", context.DeadlineExceeded))
	h.run(t0.Add(2 * time.Minute))
	ds := h.allDigests()
	if ds[0].State != DigestPending || ds[0].LastError == nil || !strings.Contains(*ds[0].LastError, "same idempotency key") {
		t.Fatalf("digest = %+v", ds[0])
	}
}

// TestADigestExpiresInsideTheIdempotencyWindow: past 23 hours a retry could land
// outside the provider's 24-hour window, where it might duplicate an earlier
// attempt that did get through.
func TestADigestExpiresInsideTheIdempotencyWindow(t *testing.T) {
	h := newHarness(t)
	h.enable("karel@example.test")
	h.queueCrash(t0)
	h.mail.Fail(&mail.SendError{Status: 500})
	h.run(t0.Add(2 * time.Minute))
	h.run(t0.Add(2*time.Minute + 23*time.Hour + time.Minute))
	ds := h.allDigests()
	if ds[0].State != DigestFailed || !strings.Contains(*ds[0].LastError, "expired") {
		t.Fatalf("digest = %+v, want expired", ds[0])
	}
	if n := len(h.mail.Attempts()); n != 1 {
		t.Fatalf("%d attempts, want no send after expiry", n)
	}
}

// TestShutdownMidSendLeavesTheDigestPending: a cancelled send writes nothing, so
// the next boot resends it with the same key and the attempt count is honest.
func TestShutdownMidSendLeavesTheDigestPending(t *testing.T) {
	h := newHarness(t)
	h.enable("karel@example.test")
	h.queueCrash(t0)
	waiting, release := h.mail.Block()
	defer release()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		h.mod.worker.RunOnce(ctx, t0.Add(2*time.Minute))
		close(done)
	}()
	select {
	case <-waiting:
	case <-time.After(5 * time.Second):
		t.Fatal("the send never started")
	}
	cancel()
	<-done
	ds := h.allDigests()
	if len(ds) != 1 || ds[0].State != DigestPending || ds[0].Attempts != 0 || ds[0].LastError != nil {
		t.Fatalf("digest after a shutdown mid-send = %+v", ds)
	}
}

// TestPrune: digests past the retention window go whatever their state — one
// stuck pending because the worker stopped running would otherwise stay on the
// deliveries list forever — and their events go with them.
func TestPrune(t *testing.T) {
	h := newHarness(t)
	h.enable("karel@example.test")
	h.queueCrash(t0)
	h.run(t0.Add(2 * time.Minute)) // sent
	recent := t0.AddDate(0, 0, 60)
	h.queueCrash(recent)
	h.run(recent.Add(2 * time.Minute)) // sent, inside the window
	// An old digest assembled and then never sent: the worker stopped running
	// (the key was removed) before it could send or expire it. Assembled alone —
	// a full pass would expire it — and last, because a pending digest holds the
	// next one back.
	h.queueCrash(t0.Add(3 * time.Minute))
	if err := h.mod.worker.assemble(context.Background(), t0.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if n := h.count(`SELECT COUNT(*) FROM notify_digest WHERE state = 'pending'`); n != 1 {
		t.Fatalf("%d pending digests, want the stuck one", n)
	}

	// home is down, then its monitoring is switched off.
	h.check(check(false, sites.Red, t0))
	if _, err := h.db.Exec(`UPDATE site SET monitor_enabled = 0 WHERE id = 'home'`); err != nil {
		t.Fatal(err)
	}

	if err := h.mod.Prune(context.Background(), t0.AddDate(0, 0, 91)); err != nil {
		t.Fatal(err)
	}
	ds := h.allDigests()
	if len(ds) != 1 || ds[0].CreatedAt != ts(recent.Add(2*time.Minute)) {
		t.Fatalf("after prune: %+v, want only the recent digest", ds)
	}
	if n := h.count(`SELECT COUNT(*) FROM notify_event`); n != 1 {
		t.Fatalf("%d events after prune, want the recent digest's one", n)
	}
	if n := h.count(`SELECT COUNT(*) FROM notify_site_state`); n != 0 {
		t.Fatalf("an unmonitored site kept its downtime memory")
	}
}

// --- the transaction probe ------------------------------------------------------

// TestNoMailSendInsideATransaction: every path that reaches the provider — the
// worker, the test button — does so with the single connection free. The probe
// times out and records a violation if a send runs inside a transaction or with
// a cursor open.
func TestNoMailSendInsideATransaction(t *testing.T) {
	h := newHarness(t)
	h.enable("karel@example.test")
	h.mail.SetTxProbe(h.db)

	h.queueCrash(t0)
	h.mail.Fail(&mail.SendError{Status: 500})
	h.run(t0.Add(2 * time.Minute)) // fails, retry scheduled
	h.report(feedback.ReportSignal{SiteID: "fin", Ref: "R-7QK2", Kind: "bug", Message: "x", At: t0.Add(3 * time.Minute)})
	h.run(t0.Add(6 * time.Minute))                // the retry
	h.run(t0.Add(6*time.Minute + 15*time.Second)) // the report, held back until the retry went out
	if code, body := h.do("POST", "/api/notifications/test", nil, nil); code != 200 {
		t.Fatalf("test send: %d %s", code, body)
	}

	if v := h.mail.Violations(); len(v) != 0 {
		t.Fatalf("sends ran while the connection was held: %v", v)
	}
	if n := len(h.mail.Attempts()); n < 4 {
		t.Fatalf("only %d sends were exercised — the assertion would pass vacuously", n)
	}
}

// TestTxProbeDetectsASendInsideATransaction proves the detector above can fail.
func TestTxProbeDetectsASendInsideATransaction(t *testing.T) {
	h := newHarness(t)
	h.mail.SetTxProbe(h.db)
	err := h.inTx(func(*sql.Tx) error {
		_, err := h.mail.Send(context.Background(), mail.Message{Subject: "inside"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if v := h.mail.Violations(); len(v) != 1 {
		t.Fatalf("violations = %v, want the send inside the transaction", v)
	}
}
