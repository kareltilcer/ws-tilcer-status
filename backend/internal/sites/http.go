package sites

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/httpx"
)

type createReq struct {
	ID               string  `json:"id"`
	Name             string  `json:"name"`
	MonitorURL       *string `json:"monitor_url"`
	MonitorEnabled   *bool   `json:"monitor_enabled"`
	ExpectedStatus   *int    `json:"expected_status"`
	CrashWindowHours *int    `json:"crash_window_hours"`
}

// listSites handles GET /api/sites (optional ?status color filter).
func (m *Module) listSites(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	all, err := m.store.List(r.Context(), now)
	if err != nil {
		httpx.WriteError(w, httpx.ErrInternal(""))
		return
	}
	if f := Color(r.URL.Query().Get("status")); isColor(f) {
		filtered := all[:0:0]
		for _, s := range all {
			if s.Color == f {
				filtered = append(filtered, s)
			}
		}
		all = filtered
	}
	httpx.JSON(w, http.StatusOK, all)
}

// createSite handles POST /api/sites.
func (m *Module) createSite(w http.ResponseWriter, r *http.Request) {
	var in createReq
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, httpx.ErrUnprocessable(err.Error()))
		return
	}
	if !ValidID(in.ID) {
		httpx.WriteError(w, httpx.ErrUnprocessable("id must match ^[a-z0-9][a-z0-9-]{0,62}$"))
		return
	}
	name, err := validateName(in.Name)
	if err != nil {
		httpx.WriteError(w, httpx.ErrUnprocessable(err.Error()))
		return
	}
	monURL := ""
	if in.MonitorURL != nil {
		monURL, err = validateMonitorURL(*in.MonitorURL)
		if err != nil {
			httpx.WriteError(w, httpx.ErrUnprocessable(err.Error()))
			return
		}
	}
	expected := 200
	if in.ExpectedStatus != nil {
		expected = *in.ExpectedStatus
		if err := validateExpectedStatus(expected); err != nil {
			httpx.WriteError(w, httpx.ErrUnprocessable(err.Error()))
			return
		}
	}
	window := 24
	if in.CrashWindowHours != nil {
		window = *in.CrashWindowHours
		if err := validateCrashWindow(window); err != nil {
			httpx.WriteError(w, httpx.ErrUnprocessable(err.Error()))
			return
		}
	}
	// monitor_enabled defaults true when a URL is set; without a URL it is off.
	enabled := monURL != ""
	if in.MonitorEnabled != nil {
		enabled = *in.MonitorEnabled && monURL != ""
	}

	plaintext, hash, err := GenerateKey()
	if err != nil {
		httpx.WriteError(w, httpx.ErrInternal(""))
		return
	}

	now := time.Now().UTC()
	sum, err := m.store.Create(r.Context(), createParams{
		ID: in.ID, Name: name, MonitorURL: monURL, MonitorEnabled: enabled,
		ExpectedStatus: expected, CrashWindowHours: window, IngestKeyHash: hash,
	}, now)
	if errors.Is(err, ErrDuplicate) {
		httpx.WriteError(w, httpx.ErrConflict("a site with this id already exists"))
		return
	}
	if err != nil {
		httpx.WriteError(w, httpx.ErrInternal(""))
		return
	}
	httpx.JSON(w, http.StatusCreated, SiteWithKey{SiteSummary: *sum, IngestKey: plaintext})
}

// getSite handles GET /api/sites/{id}.
func (m *Module) getSite(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	sum, err := m.store.Get(r.Context(), id, time.Now().UTC())
	if err != nil {
		httpx.WriteError(w, httpx.ErrInternal(""))
		return
	}
	if sum == nil {
		httpx.WriteError(w, httpx.ErrNotFound("unknown site"))
		return
	}
	httpx.JSON(w, http.StatusOK, sum)
}

var patchKeys = map[string]bool{
	"name": true, "monitor_url": true, "monitor_enabled": true,
	"expected_status": true, "crash_window_hours": true,
}

// patchSite handles PATCH /api/sites/{id}. The body is decoded into a raw map so
// a provided monitor_url:null (clear to crash-only) is distinguishable from an
// absent field (leave unchanged).
func (m *Module) patchSite(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	// Decode via the shared helper so PATCH enforces the same body cap and
	// trailing-content rejection as createSite (a raw map still accepts any keys —
	// the allow-list check below rejects unknown fields).
	raw := map[string]json.RawMessage{}
	if err := httpx.DecodeJSON(r, &raw); err != nil {
		httpx.WriteError(w, httpx.ErrUnprocessable(err.Error()))
		return
	}
	for k := range raw {
		if !patchKeys[k] {
			httpx.WriteError(w, httpx.ErrUnprocessable("unknown field: "+k))
			return
		}
	}

	var p updateParams
	if v, ok := raw["name"]; ok {
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			httpx.WriteError(w, httpx.ErrUnprocessable("name must be a string"))
			return
		}
		n, err := validateName(s)
		if err != nil {
			httpx.WriteError(w, httpx.ErrUnprocessable(err.Error()))
			return
		}
		p.Name = &n
	}
	if v, ok := raw["monitor_url"]; ok {
		p.MonitorURLSet = true
		var s *string
		if err := json.Unmarshal(v, &s); err != nil {
			httpx.WriteError(w, httpx.ErrUnprocessable("monitor_url must be a string or null"))
			return
		}
		if s != nil {
			u, err := validateMonitorURL(*s)
			if err != nil {
				httpx.WriteError(w, httpx.ErrUnprocessable(err.Error()))
				return
			}
			p.MonitorURL = u
		}
	}
	if v, ok := raw["monitor_enabled"]; ok {
		var b bool
		if err := json.Unmarshal(v, &b); err != nil {
			httpx.WriteError(w, httpx.ErrUnprocessable("monitor_enabled must be a boolean"))
			return
		}
		p.MonitorEnabledSet = true
		p.MonitorEnabled = b
	}
	if v, ok := raw["expected_status"]; ok {
		var n int
		if err := json.Unmarshal(v, &n); err != nil {
			httpx.WriteError(w, httpx.ErrUnprocessable("expected_status must be an integer"))
			return
		}
		if err := validateExpectedStatus(n); err != nil {
			httpx.WriteError(w, httpx.ErrUnprocessable(err.Error()))
			return
		}
		p.ExpectedStatus = &n
	}
	if v, ok := raw["crash_window_hours"]; ok {
		var n int
		if err := json.Unmarshal(v, &n); err != nil {
			httpx.WriteError(w, httpx.ErrUnprocessable("crash_window_hours must be an integer"))
			return
		}
		if err := validateCrashWindow(n); err != nil {
			httpx.WriteError(w, httpx.ErrUnprocessable(err.Error()))
			return
		}
		p.CrashWindowHours = &n
	}

	sum, err := m.store.Update(r.Context(), id, p, time.Now().UTC())
	if errors.Is(err, ErrMonitorURLRequired) {
		httpx.WriteError(w, httpx.ErrUnprocessable("monitor_url is required to enable monitoring"))
		return
	}
	if err != nil {
		httpx.WriteError(w, httpx.ErrInternal(""))
		return
	}
	if sum == nil {
		httpx.WriteError(w, httpx.ErrNotFound("unknown site"))
		return
	}
	httpx.JSON(w, http.StatusOK, sum)
}

// deleteSite handles DELETE /api/sites/{id}.
func (m *Module) deleteSite(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	ok, err := m.store.Delete(r.Context(), id)
	if err != nil {
		httpx.WriteError(w, httpx.ErrInternal(""))
		return
	}
	if !ok {
		httpx.WriteError(w, httpx.ErrNotFound("unknown site"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// rotateKey handles POST /api/sites/{id}/rotate-key.
func (m *Module) rotateKey(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	plaintext, hash, err := GenerateKey()
	if err != nil {
		httpx.WriteError(w, httpx.ErrInternal(""))
		return
	}
	ok, err := m.store.SetIngestKeyHash(r.Context(), id, hash)
	if err != nil {
		httpx.WriteError(w, httpx.ErrInternal(""))
		return
	}
	if !ok {
		httpx.WriteError(w, httpx.ErrNotFound("unknown site"))
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"ingest_key": plaintext})
}

func isColor(c Color) bool {
	switch c {
	case Red, Orange, Green, Unknown:
		return true
	}
	return false
}
