package monitoring

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/httpx"
)

// meta is the client-facing service metadata the SPA needs to label its UI: the
// configurable uptime window, so the board does not hardcode a day count that
// drifts from STATUS_UPTIME_WINDOW, and (v3) whether this deployment has the
// feedback module configured at all.
//
// ⚠ FeedbackEnabled is a plain deployment fact, not a per-site switch — the
// per-site kill switch is GET/PATCH /api/sites/{id}/feedback-config. It is here
// because a dashboard that renders the feedback panel on a deployment with no
// object storage offers a switch whose only possible answer is 503.
type meta struct {
	UptimeWindowDays int  `json:"uptime_window_days"`
	FeedbackEnabled  bool `json:"feedback_enabled"`
}

// getMeta handles GET /api/meta.
func (m *Module) getMeta(w http.ResponseWriter, _ *http.Request) {
	httpx.JSON(w, http.StatusOK, meta{
		UptimeWindowDays: m.uptimeWindowDays,
		FeedbackEnabled:  m.feedbackEnabled,
	})
}

// getChecks handles GET /api/sites/{id}/checks.
func (m *Module) getChecks(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	_, found, err := m.store.SiteMeta(r.Context(), id)
	if err != nil {
		httpx.WriteError(w, httpx.ErrInternal(""))
		return
	}
	if !found {
		httpx.WriteError(w, httpx.ErrNotFound("unknown site"))
		return
	}
	q := r.URL.Query()
	page, err := m.store.ListChecks(r.Context(), id, httpx.AtoiDefault(q.Get("limit"), 0), q.Get("cursor"))
	if errors.Is(err, errInvalidCursor) {
		httpx.WriteError(w, httpx.ErrUnprocessable("invalid cursor"))
		return
	}
	if err != nil {
		httpx.WriteError(w, httpx.ErrInternal(""))
		return
	}
	httpx.JSON(w, http.StatusOK, page)
}

// getUptime handles GET /api/sites/{id}/uptime.
func (m *Module) getUptime(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	q := r.URL.Query()

	window := q.Get("window")
	if window == "" {
		window = "90d"
	}
	if !validWindow(window) {
		httpx.WriteError(w, httpx.ErrUnprocessable("window must be one of 24h|7d|30d|90d"))
		return
	}
	// Default the bucket grid to the window's natural granularity (one per rolled-up
	// day for 7d/30d/90d, hourly for the intraday 24h view). A fixed 90 left non-90d
	// strips mostly empty, since daily rollups only ever fill ~1 bucket/day.
	buckets := defaultBuckets(window)
	if v := q.Get("buckets"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 180 {
			httpx.WriteError(w, httpx.ErrUnprocessable("buckets must be an integer 1..180"))
			return
		}
		buckets = n
	}

	sum, found, err := m.Uptime(r.Context(), id, window, buckets, time.Now().UTC())
	if err != nil {
		httpx.WriteError(w, httpx.ErrInternal(""))
		return
	}
	if !found {
		httpx.WriteError(w, httpx.ErrNotFound("unknown site"))
		return
	}
	httpx.JSON(w, http.StatusOK, sum)
}

func validWindow(w string) bool {
	switch w {
	case "24h", "7d", "30d", "90d":
		return true
	}
	return false
}

// defaultBuckets returns the natural bucket count for a window when the client
// doesn't pass ?buckets: one per rolled-up UTC day for 7d/30d/90d, hourly for the
// intraday 24h view. Daily rollups fill at most one bucket per day, so aligning
// the default to whole days keeps the strip fully populated instead of gappy.
func defaultBuckets(window string) int {
	switch window {
	case "24h":
		return 24
	case "7d":
		return 7
	case "30d":
		return 30
	default: // "90d"
		return 90
	}
}
