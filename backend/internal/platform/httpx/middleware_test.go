package httpx

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/go-chi/chi/v5"
)

// apiSegments is the derived re-prefix set under test. Every case below states
// the segment it exercises, so a segment the router registers but this test
// never names is visible as an omission — see
// TestStripAPIPrefixCoversEveryRegisteredAPIRoute in internal/apitest, which
// enumerates the real routing tree.
var apiSegments = []string{"auth", "crashes", "ingest", "meta", "sites"}

func TestStripAPIPrefix(t *testing.T) {
	cases := []struct {
		name     string
		path     string
		expected string
	}{
		// Already correct: passed through untouched.
		{"api prefix once", "/api/sites", "/api/sites"},
		{"api auth", "/api/auth/login", "/api/auth/login"},
		{"api meta", "/api/meta", "/api/meta"},
		{"health", "/healthz", "/healthz"},
		{"readyz", "/readyz", "/readyz"},
		{"root", "/", "/"},

		// Double prefix: stripped back to one.
		{"double api prefix", "/api/api/sites", "/api/sites"},
		{"double api auth", "/api/api/auth/login", "/api/auth/login"},
		{"double api exact", "/api/api", "/api"},
		{"exact api", "/api", "/"},
		{"triple api", "/api/api/api/sites", "/api/api/sites"},

		// Stripped prefix: re-prefixed, one case per derived segment.
		{"stripped auth", "/auth/login", "/api/auth/login"},
		{"stripped sites", "/sites", "/api/sites"},
		{"stripped site detail", "/sites/home", "/api/sites/home"},
		{"stripped crashes", "/crashes", "/api/crashes"},
		{"stripped ingest", "/ingest/test", "/api/ingest/test"},
		// The segment the hand-written list forgot. /api/meta 404'd in production
		// because of it, and the test that guarded the list never named it.
		{"stripped meta", "/meta", "/api/meta"},

		// Not an API segment: left alone (an SPA route, or a typo that must 404
		// loudly rather than be bent into an API path).
		{"unknown segment", "/nope", "/nope"},
		{"segment prefix is not a segment", "/sitesomething", "/sitesomething"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tc.path, nil)
			w := httptest.NewRecorder()

			handler := StripAPIPrefix(apiSegments)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(r.URL.Path))
			}))

			handler.ServeHTTP(w, req)

			if got := w.Body.String(); got != tc.expected {
				t.Errorf("StripAPIPrefix(%q) = %q, want %q", tc.path, got, tc.expected)
			}
		})
	}
}

// TestAPISegments proves the set is read off the routing tree, including a
// segment nobody would think to add by hand.
func TestAPISegments(t *testing.T) {
	r := chi.NewRouter()
	r.Get("/healthz", func(http.ResponseWriter, *http.Request) {})
	r.Route("/api", func(api chi.Router) {
		api.Post("/auth/login", func(http.ResponseWriter, *http.Request) {})
		api.Get("/meta", func(http.ResponseWriter, *http.Request) {})
		api.Get("/sites/{id}/crashes", func(http.ResponseWriter, *http.Request) {})
		api.Get("/zzz-whatever-v4-adds", func(http.ResponseWriter, *http.Request) {})
	})

	got := APISegments(r)
	want := []string{"auth", "meta", "sites", "zzz-whatever-v4-adds"}
	if !slices.Equal(got, want) {
		t.Fatalf("APISegments = %v, want %v", got, want)
	}
}

func TestClientIP(t *testing.T) {
	const remote = "10.0.0.9:5555" // the direct peer (proxy) address:port

	cases := []struct {
		name           string
		xff            string
		trustedProxies int
		want           string
	}{
		// No XFF: fall back to the direct peer with the port stripped.
		{"no xff, one trusted", "", 1, "10.0.0.9"},
		{"no xff, zero trusted", "", 0, "10.0.0.9"},

		// Single Traefik (the Coolify default): the real client is the sole,
		// rightmost entry it appended.
		{"single proxy", "203.0.113.7", 1, "203.0.113.7"},
		{"single proxy spoof attempt to the left is ignored", "1.2.3.4, 203.0.113.7", 1, "203.0.113.7"},

		// CDN in front of Traefik: XFF is `client, cdnEdge`. Trusting one hop would
		// return the shared CDN edge IP (the bug); trusting two returns the client.
		{"cdn then traefik, one trusted (edge IP)", "203.0.113.7, 198.51.100.1", 1, "198.51.100.1"},
		{"cdn then traefik, two trusted (client)", "203.0.113.7, 198.51.100.1", 2, "203.0.113.7"},

		// Zero trusted proxies disables XFF entirely (spoofable header ignored).
		{"zero trusted ignores xff", "203.0.113.7", 0, "10.0.0.9"},

		// Header shorter than the configured hop count: the request did not traverse
		// the full trusted chain, so no XFF entry is provably proxy-appended and every
		// value is client-forgeable. Fall through to the unspoofable direct peer
		// rather than trusting the (attacker-controlled) leftmost entry.
		{"fewer entries than hops falls back to peer", "203.0.113.7", 3, "10.0.0.9"},

		// Whitespace around entries is trimmed.
		{"trims spaces", "203.0.113.7 , 198.51.100.1", 2, "203.0.113.7"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &http.Request{
				Header:     http.Header{},
				RemoteAddr: remote,
			}
			if tc.xff != "" {
				r.Header.Set("X-Forwarded-For", tc.xff)
			}
			if got := clientIP(r, tc.trustedProxies); got != tc.want {
				t.Fatalf("clientIP(xff=%q, trusted=%d) = %q, want %q", tc.xff, tc.trustedProxies, got, tc.want)
			}
		})
	}
}
