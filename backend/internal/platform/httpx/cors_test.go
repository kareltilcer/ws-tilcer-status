package httpx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testOrigin = "https://home.tilcer.cz"

var testAllowed = []string{"https://*.tilcer.cz"}

func corsHandler() http.Handler {
	return NewCORS(testAllowed)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
}

func TestCORSPreflightFromAllowedOrigin(t *testing.T) {
	req := httptest.NewRequest(http.MethodOptions, "/api/ingest/home", nil)
	req.Header.Set("Origin", testOrigin)
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	req.Header.Set("Access-Control-Request-Headers", "content-type, x-ingest-key")
	w := httptest.NewRecorder()

	NewCORS(testAllowed)(http.HandlerFunc(Preflight)).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != testOrigin {
		t.Fatalf("Allow-Origin = %q, want the echoed origin %q", got, testOrigin)
	}
	for _, h := range []string{"X-Ingest-Key", "X-Widget-Key", "Content-Type"} {
		if !strings.Contains(w.Header().Get("Access-Control-Allow-Headers"), h) {
			t.Errorf("Allow-Headers %q missing %q", w.Header().Get("Access-Control-Allow-Headers"), h)
		}
	}
	if !strings.Contains(w.Header().Get("Access-Control-Allow-Methods"), http.MethodPost) {
		t.Errorf("Allow-Methods = %q, want POST", w.Header().Get("Access-Control-Allow-Methods"))
	}
	if w.Header().Get("Access-Control-Max-Age") == "" {
		t.Error("Max-Age not set: every call re-preflights")
	}
}

func TestCORSPreflightFromForeignOrigin(t *testing.T) {
	req := httptest.NewRequest(http.MethodOptions, "/api/ingest/home", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	w := httptest.NewRecorder()

	NewCORS(testAllowed)(http.HandlerFunc(Preflight)).ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Allow-Origin = %q for a foreign origin, want none", got)
	}
}

// The apex is not a subdomain: "https://*.tilcer.cz" must not match
// "https://tilcer.cz", and no pattern may be satisfied by a suffix trick.
func TestCORSOriginMatching(t *testing.T) {
	cases := []struct {
		origin string
		want   bool
	}{
		{"https://home.tilcer.cz", true},
		{"https://status.tilcer.cz", true},
		{"http://home.tilcer.cz", false}, // scheme must match
		{"https://tilcer.cz", false},     // bare apex is not a subdomain
		{"https://eviltilcer.cz", false}, // suffix without the dot
		{"https://tilcer.cz.evil.com", false},
		{"null", false},
	}
	for _, tc := range cases {
		if got := MatchOrigin(tc.origin, testAllowed...); got != tc.want {
			t.Errorf("MatchOrigin(%q) = %v, want %v", tc.origin, got, tc.want)
		}
	}
}

// ⚠ The public endpoints authenticate by key, not by cookie. Allow-Credentials
// would be the difference between a public ingest endpoint and a cross-origin
// door into a session (V3-D43), so it is never sent — on any method, matched
// origin or not.
func TestCORSNeverAllowsCredentials(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodOptions} {
		for _, origin := range []string{testOrigin, "https://evil.example.com", ""} {
			req := httptest.NewRequest(method, "/api/ingest/home", nil)
			if origin != "" {
				req.Header.Set("Origin", origin)
			}
			w := httptest.NewRecorder()
			corsHandler().ServeHTTP(w, req)
			if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "" {
				t.Fatalf("%s from %q sent Allow-Credentials = %q", method, origin, got)
			}
		}
	}
}

// Vary: Origin goes on every response, matched or not — otherwise a shared cache
// can serve one origin's response (with its Allow-Origin) to another.
func TestCORSAlwaysVariesOnOrigin(t *testing.T) {
	for _, origin := range []string{testOrigin, "https://evil.example.com", ""} {
		req := httptest.NewRequest(http.MethodPost, "/api/ingest/home", nil)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		corsHandler().ServeHTTP(w, req)
		if !slicesContainsFold(w.Header().Values("Vary"), "Origin") {
			t.Fatalf("origin %q: Vary = %v, want it to include Origin", origin, w.Header().Values("Vary"))
		}
	}
}

// An empty allow-list disables cross-origin access rather than opening it.
func TestCORSEmptyAllowListMatchesNothing(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/ingest/home", nil)
	req.Header.Set("Origin", testOrigin)
	w := httptest.NewRecorder()
	NewCORS(nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {})).ServeHTTP(w, req)
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Allow-Origin = %q with an empty allow-list, want none", got)
	}
}

func slicesContainsFold(vals []string, want string) bool {
	for _, v := range vals {
		for _, part := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(part), want) {
				return true
			}
		}
	}
	return false
}
