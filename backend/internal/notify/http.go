package notify

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/notify/mail"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/httpx"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/sites"
)

// errUnconfigured is the 503 a deployment without a mail provider answers to
// anything that would need one.
var errUnconfigured = &httpx.APIError{
	Status: http.StatusServiceUnavailable,
	Code:   "mail_unconfigured",
	Detail: "this deployment has no mail provider (set STATUS_RESEND_API_KEY)",
}

// getSettings handles GET /api/notifications/settings.
func (m *Module) getSettings(w http.ResponseWriter, r *http.Request) {
	out, err := m.settingsView(r.Context(), httpx.IsAdmin(r.Context()))
	if err != nil {
		m.fail(w, "load settings", err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (m *Module) settingsView(ctx context.Context, admin bool) (Settings, error) {
	st, err := loadSettings(ctx, m.db)
	if err != nil {
		return Settings{}, err
	}
	muted, err := mutedSites(ctx, m.db)
	if err != nil {
		return Settings{}, err
	}
	out := Settings{
		Available:           m.mailer != nil,
		From:                m.cfg.From,
		Enabled:             st.Enabled,
		Events:              EventToggles{Crash: st.OnCrash, Feedback: st.OnFeedback, Downtime: st.OnDowntime},
		MutedSites:          muted,
		DigestWindowSeconds: int(m.cfg.DigestWindow / time.Second),
		MaxPerHour:          m.cfg.MaxPerHour,
		UpdatedAt:           st.UpdatedAt,
	}
	if m.mailer != nil {
		p := m.mailer.Provider()
		out.Provider = &p
	}
	if admin {
		out.Recipients = nonNil(st.Recipients)
	}
	return out, nil
}

// putSettings handles PUT /api/notifications/settings. The body replaces the
// whole of the delivery settings; mutes have their own routes.
func (m *Module) putSettings(w http.ResponseWriter, r *http.Request) {
	var in settingsUpdate
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, httpx.ErrUnprocessable(err.Error()))
		return
	}
	if in.Enabled == nil || in.Recipients == nil || in.Events == nil ||
		in.Events.Crash == nil || in.Events.Feedback == nil || in.Events.Downtime == nil {
		httpx.WriteError(w, httpx.ErrUnprocessable("enabled, recipients and events{crash,feedback,downtime} are all required"))
		return
	}
	recipients, err := validateRecipients(*in.Recipients)
	if err != nil {
		httpx.WriteError(w, httpx.ErrUnprocessable(err.Error()))
		return
	}
	if *in.Enabled && m.mailer == nil {
		httpx.WriteError(w, errUnconfigured)
		return
	}
	if *in.Enabled && len(recipients) == 0 {
		httpx.WriteError(w, httpx.ErrUnprocessable("add at least one recipient before turning notifications on"))
		return
	}
	st := settings{
		Enabled:    *in.Enabled,
		Recipients: recipients,
		OnCrash:    *in.Events.Crash,
		OnFeedback: *in.Events.Feedback,
		OnDowntime: *in.Events.Downtime,
	}
	if err := saveSettings(r.Context(), m.db, st, ts(time.Now().UTC())); err != nil {
		m.fail(w, "save settings", err)
		return
	}
	out, err := m.settingsView(r.Context(), true)
	if err != nil {
		m.fail(w, "load settings", err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// muteSite handles PUT /api/notifications/muted-sites/{siteId}: nothing about
// that site is emailed until it is unmuted. Idempotent.
func (m *Module) muteSite(w http.ResponseWriter, r *http.Request) { m.setMute(w, r, true) }

// unmuteSite handles DELETE /api/notifications/muted-sites/{siteId}. Idempotent.
func (m *Module) unmuteSite(w http.ResponseWriter, r *http.Request) { m.setMute(w, r, false) }

func (m *Module) setMute(w http.ResponseWriter, r *http.Request, muted bool) {
	siteID := chi.URLParam(r, "siteId")
	if !sites.ValidID(siteID) {
		httpx.WriteError(w, httpx.ErrNotFound("unknown site"))
		return
	}
	exists, err := m.sitesStore.Exists(r.Context(), siteID)
	if err != nil {
		m.fail(w, "check site", err)
		return
	}
	if !exists {
		httpx.WriteError(w, httpx.ErrNotFound("unknown site"))
		return
	}
	if err := setMuted(r.Context(), m.db, siteID, muted, ts(time.Now().UTC())); err != nil {
		m.fail(w, "set mute", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// sendTest handles POST /api/notifications/test: one email to the SAVED
// recipients, straight through the provider — not queued, not digested, and
// regardless of the master switch and the toggles, because its job is to prove
// the provider and the addresses work before anything real depends on them.
func (m *Module) sendTest(w http.ResponseWriter, r *http.Request) {
	if m.mailer == nil {
		httpx.WriteError(w, errUnconfigured)
		return
	}
	st, err := loadSettings(r.Context(), m.db)
	if err != nil {
		m.fail(w, "load settings", err)
		return
	}
	if len(st.Recipients) == 0 {
		httpx.WriteError(w, &httpx.APIError{Status: http.StatusUnprocessableEntity, Code: "no_recipients",
			Detail: "save at least one recipient first"})
		return
	}
	if ok, retry := m.testLimiter.Allow("test"); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(retry.Seconds()))))
		httpx.WriteError(w, &httpx.APIError{Status: http.StatusTooManyRequests, Code: "rate_limited",
			Detail: "wait a few seconds before sending another test email"})
		return
	}
	// ⚠ No transaction is open here and the settings row above was read with
	// QueryRow, which releases the connection: the send holds nothing.
	ctx, cancel := context.WithTimeout(r.Context(), sendTimeout)
	defer cancel()
	res, err := m.mailer.Send(ctx, testMessage(m.cfg, st, time.Now().UTC()))
	if err != nil {
		m.logger.Warn("notify: test email failed", "err", err)
		detail := err.Error()
		if errors.Is(err, context.DeadlineExceeded) {
			detail = "the mail provider did not answer in time"
		}
		httpx.WriteError(w, &httpx.APIError{Status: http.StatusBadGateway, Code: "mail_failed", Detail: detail})
		return
	}
	httpx.JSON(w, http.StatusOK, TestResult{ProviderMessageID: res.ID, Recipients: st.Recipients})
}

func testMessage(cfg Config, st settings, now time.Time) mail.Message {
	host := cfg.PublicURL
	if u, err := url.Parse(cfg.PublicURL); err == nil && u.Host != "" {
		host = u.Host
	}
	var kinds []string
	if st.OnCrash {
		kinds = append(kinds, "new crashes and crashes that come back")
	}
	if st.OnFeedback {
		kinds = append(kinds, "new feedback reports")
	}
	if st.OnDowntime {
		kinds = append(kinds, "sites going down and coming back up")
	}
	state := "Notifications are OFF — nothing but this test is being sent."
	if st.Enabled && len(kinds) > 0 {
		state = "Notifications are on for: " + strings.Join(kinds, "; ") + "."
	} else if st.Enabled {
		state = "Notifications are on, but every kind is switched off."
	}
	text := fmt.Sprintf("This is a test email from %s, sent at %s.\n\n%s\n\nChange what you get: %s/settings/notifications\n",
		host, formatTime(now), state, cfg.PublicURL)
	return mail.Message{
		From:    cfg.From,
		To:      st.Recipients,
		Subject: sanitizeSubject("[status] Test email"),
		Text:    text,
		// A fresh key per press: a test is never retried, and its key must never
		// collide with a digest's.
		IdempotencyKey: "status-test-" + uuid.NewString(),
	}
}

// listDeliveries handles GET /api/notifications/deliveries: the newest digests
// and what became of them.
func (m *Module) listDeliveries(w http.ResponseWriter, r *http.Request) {
	limit := httpx.AtoiDefault(r.URL.Query().Get("limit"), 20)
	if limit < 1 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	ds, err := recentDigests(r.Context(), m.db, limit)
	if err != nil {
		m.fail(w, "list deliveries", err)
		return
	}
	admin := httpx.IsAdmin(r.Context())
	out := DeliveryPage{Items: make([]Delivery, 0, len(ds))}
	for _, d := range ds {
		item := Delivery{
			ID: d.ID, CreatedAt: d.CreatedAt, State: d.State, Subject: d.Subject,
			EventCount: d.EventCount, Attempts: d.Attempts, LastError: d.LastError, SentAt: d.SentAt,
		}
		if d.State == DigestPending {
			next := d.NextAttemptAt
			item.NextAttemptAt = &next
		}
		if admin {
			item.Recipients = nonNil(d.Recipients)
		}
		out.Items = append(out.Items, item)
	}
	httpx.JSON(w, http.StatusOK, out)
}

// fail logs an unexpected error and answers a bare 500; the detail stays in the
// log.
func (m *Module) fail(w http.ResponseWriter, what string, err error) {
	m.logger.Error("notify: "+what, "err", err)
	httpx.WriteError(w, httpx.ErrInternal(""))
}
