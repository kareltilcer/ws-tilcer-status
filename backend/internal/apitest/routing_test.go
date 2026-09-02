package apitest

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// pathParam matches a chi wildcard segment ("{id}", "{siteId}") so a registered
// pattern can be turned into a concrete request path.
var pathParam = regexp.MustCompile(`\{[^}]+\}`)

// apiRoutes walks the composed router and returns every route registered under
// /api as a (method, concrete path) pair. Enumerating the real routing tree is
// the point of every test in this file: the hand-written re-prefix list this
// replaced omitted "/meta", and the test guarding it enumerated the same four
// prefixes somebody remembered in August — so the gap shipped.
func apiRoutes(t *testing.T, a *api) []struct{ Method, Path string } {
	t.Helper()
	var out []struct{ Method, Path string }
	err := chi.Walk(a.rt.Mux, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if !strings.HasPrefix(route, "/api/") {
			return nil
		}
		out = append(out, struct{ Method, Path string }{method, pathParam.ReplaceAllString(route, "probe")})
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("walked the router and found no /api routes — the walk is broken, not the router")
	}
	return out
}

// reachedARoute reports whether a response came from a real handler rather than
// the router's fall-through 404. Any status is fine — a handler answering 404
// "site not found" still proves the path routed; only "no such endpoint" means
// it did not.
func reachedARoute(status int, body []byte) bool {
	if status != http.StatusNotFound {
		return true
	}
	var env struct {
		Detail string `json:"detail"`
	}
	_ = json.Unmarshal(body, &env)
	return env.Detail != "no such endpoint"
}

// TestStripAPIPrefixCoversEveryRegisteredAPIRoute replays every registered API
// route with the "/api" prefix stripped — the shape a request arrives in when
// Coolify's Strip Prefix is enabled, which is what production was actually doing
// when GET /api/meta 404'd. Each must still reach its handler.
//
// The list is derived from the routing tree, so a route added in v4 is covered
// the day it is registered and nobody has to remember this test exists.
func TestStripAPIPrefixCoversEveryRegisteredAPIRoute(t *testing.T) {
	a := newAPI(t, 100, 1000)

	for _, rt := range apiRoutes(t, a) {
		stripped := strings.TrimPrefix(rt.Path, "/api")
		t.Run(rt.Method+" "+stripped, func(t *testing.T) {
			st, body := a.do(t, rt.Method, stripped, nil, nil)
			if !reachedARoute(st, body) {
				t.Fatalf("%s %s (stripped from %s) fell through to the router 404: %s",
					rt.Method, stripped, rt.Path, body)
			}
		})
	}
}

// The doubled-prefix shape (Coolify upstream already carrying /api) must route too.
func TestDoubleAPIPrefixCoversEveryRegisteredAPIRoute(t *testing.T) {
	a := newAPI(t, 100, 1000)

	for _, rt := range apiRoutes(t, a) {
		doubled := "/api" + rt.Path
		t.Run(rt.Method+" "+doubled, func(t *testing.T) {
			st, body := a.do(t, rt.Method, doubled, nil, nil)
			if !reachedARoute(st, body) {
				t.Fatalf("%s %s fell through to the router 404: %s", rt.Method, doubled, body)
			}
		})
	}
}

// The health probes are not under /api, so they must never be re-prefixed —
// Coolify's container check hits them directly on :112.
func TestHealthProbesAreNeverRewritten(t *testing.T) {
	a := newAPI(t, 100, 1000)
	for _, path := range []string{"/healthz", "/readyz"} {
		st, body := a.do(t, "GET", path, nil, nil)
		if st != http.StatusOK {
			t.Fatalf("GET %s = %d (%s), want 200", path, st, body)
		}
	}
}

// A path that is not an API segment must 404 loudly rather than be bent into an
// API path.
func TestUnknownPathStill404s(t *testing.T) {
	a := newAPI(t, 100, 1000)
	st, body := a.do(t, "GET", "/nope", nil, nil)
	if reachedARoute(st, body) {
		t.Fatalf("GET /nope = %d (%s), want the router 404", st, body)
	}
}
