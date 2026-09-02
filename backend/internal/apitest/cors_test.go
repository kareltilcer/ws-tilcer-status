package apitest

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

const (
	allowedOrigin = "https://fin.tilcer.cz"
	foreignOrigin = "https://evil.example.com"
)

// doOrigin sends a request carrying an Origin header and returns the response
// headers as well as the status — none of this bug is observable from the
// same-origin helpers, which is exactly why it survived a build with 34 test
// functions.
func (a *api) doOrigin(t *testing.T, method, path, origin string, body any, headers map[string]string) (int, http.Header, []byte) {
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
	if origin != "" {
		req.Header.Set("Origin", origin)
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
	return resp.StatusCode, resp.Header, out
}

// publicPaths returns the concrete paths that answer a preflight — i.e. the
// public group, identified by the OPTIONS handlers NewRouter derived from it.
func publicPaths(t *testing.T, a *api) []string {
	t.Helper()
	var out []string
	_ = chi.Walk(a.rt.Mux, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if method == http.MethodOptions && strings.HasPrefix(route, "/api/") {
			out = append(out, pathParam.ReplaceAllString(route, "probe"))
		}
		return nil
	})
	if len(out) == 0 {
		t.Fatal("no public paths answer OPTIONS — the preflight derivation is broken")
	}
	return out
}

// TestPreflightFromAllowedOrigin covers the request that has never worked:
// clients/js/status-report.js sends Content-Type and X-Ingest-Key, neither
// CORS-safelisted, so every crash report from a browser on another origin is
// preflighted first. Before this, the OPTIONS matched no route, fell to the JSON
// 404, and the browser silently dropped the POST.
func TestPreflightFromAllowedOrigin(t *testing.T) {
	a := newAPI(t, 100, 1000)

	for _, path := range publicPaths(t, a) {
		t.Run(path, func(t *testing.T) {
			st, hdr, body := a.doOrigin(t, http.MethodOptions, path, allowedOrigin, nil, map[string]string{
				"Access-Control-Request-Method":  http.MethodPost,
				"Access-Control-Request-Headers": "content-type, x-ingest-key",
			})
			if st != http.StatusNoContent {
				t.Fatalf("preflight %s = %d (%s), want 204", path, st, body)
			}
			if got := hdr.Get("Access-Control-Allow-Origin"); got != allowedOrigin {
				t.Fatalf("preflight %s: Allow-Origin = %q, want %q", path, got, allowedOrigin)
			}
			if got := hdr.Get("Access-Control-Allow-Headers"); !strings.Contains(strings.ToLower(got), "x-ingest-key") {
				t.Fatalf("preflight %s: Allow-Headers = %q, want X-Ingest-Key", path, got)
			}
		})
	}
}

func TestPreflightFromForeignOriginIsNotAllowed(t *testing.T) {
	a := newAPI(t, 100, 1000)

	for _, path := range publicPaths(t, a) {
		st, hdr, _ := a.doOrigin(t, http.MethodOptions, path, foreignOrigin, nil, map[string]string{
			"Access-Control-Request-Method": http.MethodPost,
		})
		if got := hdr.Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("preflight %s from a foreign origin: Allow-Origin = %q, want none (status %d)", path, got, st)
		}
	}
}

// A real cross-origin crash report: preflight, then the POST itself, which must
// carry Allow-Origin or the browser discards the 202 the server sent.
func TestCrossOriginIngestCarriesAllowOrigin(t *testing.T) {
	a := newAPI(t, 100, 1000)

	st, body := a.do(t, "POST", "/api/sites", map[string]any{"id": "fin", "name": "Finance"}, nil)
	if st != http.StatusCreated {
		t.Fatalf("create site: %d %s", st, body)
	}
	var created struct {
		IngestKey string `json:"ingest_key"`
	}
	mustJSON(t, body, &created)

	st, hdr, body := a.doOrigin(t, http.MethodPost, "/api/ingest/fin", allowedOrigin,
		map[string]any{"message": "cross-origin boom"},
		map[string]string{"X-Ingest-Key": created.IngestKey})
	if st != http.StatusAccepted {
		t.Fatalf("cross-origin ingest = %d (%s), want 202", st, body)
	}
	if got := hdr.Get("Access-Control-Allow-Origin"); got != allowedOrigin {
		t.Fatalf("Allow-Origin = %q, want %q — the browser would drop this 202", got, allowedOrigin)
	}
	if !varyIncludesOrigin(hdr) {
		t.Fatalf("Vary = %v, want it to include Origin", hdr.Values("Vary"))
	}
}

// ⚠ The gated group stays same-origin. The dashboard is served from the status
// origin; nothing else may reach it, and an Allow-Origin here plus the session
// cookie would be a cross-origin door into the dashboard.
func TestGatedGroupIsNotCrossOrigin(t *testing.T) {
	a := newAPI(t, 100, 1000)

	for _, rt := range apiRoutes(t, a) {
		if rt.Method == http.MethodOptions {
			continue // the public group's own preflights
		}
		if isPublicPath(t, a, rt.Path) {
			continue
		}
		st, hdr, _ := a.doOrigin(t, rt.Method, rt.Path, allowedOrigin, nil, nil)
		if got := hdr.Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("gated %s %s: Allow-Origin = %q, want none (status %d)", rt.Method, rt.Path, got, st)
		}
	}
}

// ⚠ Allow-Credentials is never sent, anywhere, on any route or method (V3-D43).
// The public endpoints authenticate by key; sending it would be the difference
// between a public ingest endpoint and a cross-origin door into a session.
func TestNoResponseEverAllowsCredentials(t *testing.T) {
	a := newAPI(t, 100, 1000)

	for _, origin := range []string{allowedOrigin, foreignOrigin} {
		for _, rt := range apiRoutes(t, a) {
			_, hdr, _ := a.doOrigin(t, rt.Method, rt.Path, origin, nil, nil)
			if got := hdr.Get("Access-Control-Allow-Credentials"); got != "" {
				t.Fatalf("%s %s from %s sent Allow-Credentials = %q", rt.Method, rt.Path, origin, got)
			}
		}
	}
	for _, path := range []string{"/healthz", "/readyz"} {
		_, hdr, _ := a.doOrigin(t, http.MethodGet, path, allowedOrigin, nil, nil)
		if got := hdr.Get("Access-Control-Allow-Credentials"); got != "" {
			t.Fatalf("GET %s sent Allow-Credentials = %q", path, got)
		}
	}
}

func isPublicPath(t *testing.T, a *api, path string) bool {
	t.Helper()
	for _, p := range publicPaths(t, a) {
		if p == path {
			return true
		}
	}
	return false
}

func varyIncludesOrigin(h http.Header) bool {
	for _, v := range h.Values("Vary") {
		for _, part := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(part), "Origin") {
				return true
			}
		}
	}
	return false
}
