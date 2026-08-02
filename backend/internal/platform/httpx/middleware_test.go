package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStripAPIPrefix(t *testing.T) {
	cases := []struct {
		name     string
		path     string
		expected string
	}{
		// Normal paths should pass through unchanged
		{"no prefix", "/sites", "/sites"},
		{"api prefix once", "/api/sites", "/api/sites"},
		{"api auth", "/api/auth/login", "/api/auth/login"},
		{"health", "/healthz", "/healthz"},
		{"root", "/", "/"},

		// Double prefix should be stripped to single
		{"double api prefix", "/api/api/sites", "/api/sites"},
		{"double api auth", "/api/api/auth/login", "/api/auth/login"},
		{"double api exact", "/api/api", "/api"},
		{"exact api", "/api", "/"},
		{"triple api", "/api/api/api/sites", "/api/api/sites"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tc.path, nil)
			w := httptest.NewRecorder()

			handler := StripAPIPrefix(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(r.URL.Path))
			}))

			handler.ServeHTTP(w, req)

			if got := w.Body.String(); got != tc.expected {
				t.Errorf("StripAPIPrefix(%q) = %q, want %q", tc.path, got, tc.expected)
			}
		})
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
