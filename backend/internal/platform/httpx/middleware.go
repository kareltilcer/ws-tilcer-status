package httpx

import (
	"log/slog"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/idgen"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/reqctx"
)

// requestHeaderID is the inbound/outbound correlation header.
const requestHeaderID = "X-Request-Id"

// clientHeaderID carries the browser's opaque per-tab id.
const clientHeaderID = "X-Client-Id"

// statusRecorder captures the response status for the access log.
type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.wrote && code >= 200 {
		r.status = code
		r.wrote = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if !r.wrote {
		r.status = http.StatusOK
		r.wrote = true
	}
	return r.ResponseWriter.Write(b)
}

// RequestID mints (or accepts) a request id, stores request metadata in the
// context, and echoes the id on the response. trustedProxies is threaded into
// client-IP resolution (see clientIP).
func RequestID(site string, trustedProxies int) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get(requestHeaderID)
			if id == "" {
				id = idgen.New()
			}
			w.Header().Set(requestHeaderID, id)
			info := reqctx.RequestInfo{
				RequestID: id,
				IP:        clientIP(r, trustedProxies),
				UserAgent: r.UserAgent(),
				Site:      site,
				ClientID:  r.Header.Get(clientHeaderID),
			}
			next.ServeHTTP(w, r.WithContext(reqctx.WithRequest(r.Context(), info)))
		})
	}
}

// Logger emits one structured access log line per request carrying the request id.
func Logger(l *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			id := ""
			if info, ok := reqctx.RequestFrom(r.Context()); ok {
				id = info.RequestID
			}
			l.Info("request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"latency_ms", time.Since(start).Milliseconds(),
				"request_id", id,
			)
		})
	}
}

// Recover converts a panic into a 500 and logs it with the request id, so one
// bad handler cannot take the process down.
func Recover(l *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if p := recover(); p != nil {
					id := ""
					if info, ok := reqctx.RequestFrom(r.Context()); ok {
						id = info.RequestID
					}
					l.Error("panic recovered", "panic", p, "path", r.URL.Path, "request_id", id)
					WriteError(w, ErrInternal(""))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// StripAPIPrefix normalizes the request path so a reverse-proxy misconfiguration
// does not silently unroute the API. It tolerates two shapes:
//
//  1. Double prefix: the upstream already includes "/api" (Coolify upstream =
//     :112/api)      /api/api/auth/login → /api/auth/login
//  2. Stripped prefix: "Strip Prefix" is enabled on an /api path rule
//     /auth/login → /api/auth/login,  /sites → /api/sites
//
// segments is the set of first path segments under /api that may be re-prefixed.
// ⚠ It is DERIVED from the routes the router actually registers (see
// APISegments), never hand-written: the hand-written list this replaced omitted
// "/meta", so GET /api/meta 404'd in production for a month while the SPA
// silently fell back to a hard-coded 90-day uptime window. A defensive layer
// that needs manual maintenance is not defensive — it is a second place to be
// wrong.
//
// Health probes (/healthz, /readyz) are not under /api, so they are never in the
// derived set and never rewritten.
//
// ⚠ Where the same origin also serves the SPA (StaticDir, the local harness
// only), an SPA route that shares a first segment with an API route — /sites/:id,
// /crashes/:groupId — is rewritten to the API path and answers JSON. Production
// splits the two apps across Traefik, so this affects no deployment; it is the
// price of the normalizer, and it is why the normalizer is a fallback for a
// misconfigured proxy rather than a supported routing mode.
func StripAPIPrefix(segments []string) func(http.Handler) http.Handler {
	set := make(map[string]struct{}, len(segments))
	for _, s := range segments {
		set[s] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path

			switch {
			// Double prefix: /api/api/... → /api/...
			case strings.HasPrefix(path, "/api/api"):
				r.URL.Path = strings.TrimPrefix(path, "/api")
			// Exact /api → / (the /api subrouter matches nothing at its own root).
			case path == "/api":
				r.URL.Path = "/"
			// Stripped prefix: a path whose first segment is one the API owns.
			case !strings.HasPrefix(path, "/api"):
				if _, ok := set[firstSegment(path)]; ok {
					r.URL.Path = "/api" + path
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}

// APISegments returns the distinct first path segments the router registers under
// /api, sorted — the re-prefix set for StripAPIPrefix. Walking the composed
// routing tree is the whole point: whatever v4 mounts falls out automatically.
func APISegments(routes chi.Routes) []string {
	seen := map[string]struct{}{}
	_ = chi.Walk(routes, func(_ string, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		rest, ok := strings.CutPrefix(route, "/api/")
		if !ok {
			return nil
		}
		if seg := firstSegment("/" + rest); seg != "" {
			seen[seg] = struct{}{}
		}
		return nil
	})
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// firstSegment returns the first path segment of an absolute path ("/sites/x" →
// "sites"), or "" if there is none.
func firstSegment(path string) string {
	rest, ok := strings.CutPrefix(path, "/")
	if !ok {
		return ""
	}
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		rest = rest[:i]
	}
	return rest
}

// clientIP extracts a best-effort client IP for logging and login rate-limit
// keying. Behind a reverse proxy the connecting peer (r.RemoteAddr) is the proxy,
// so the real client is read from X-Forwarded-For.
//
// XFF is client-appendable and therefore spoofable to its left; only entries
// appended by proxies we operate are trustworthy. trustedProxies is how many
// proxies sit in front of this service and append to XFF: Coolify's lone Traefik
// is 1 (it appends the real client, the rightmost entry); add 1 for each extra
// trusted hop such as a CDN (e.g. Cloudflare → Traefik is 2). The real client is
// that many entries counted from the right — NOT unconditionally the rightmost,
// which under a CDN would be the shared edge IP and collapse every user into one
// rate-limit bucket. trustedProxies == 0 disables XFF entirely and keys on the
// direct peer.
//
// A residual assumption remains (inherent to hop counting): if a request reaches
// Traefik without traversing all trustedProxies hops, an attacker can forge the
// entry we read. Keep the proxy chain closed (Traefik reachable only via the CDN)
// for the count to hold.
//
// If the header carries FEWER entries than trustedProxies (unexpected topology, or
// a direct connection that skipped a proxy), the request did not traverse the full
// trusted chain, so no entry is provably proxy-appended and every value is
// client-forgeable. Do NOT fall back to the leftmost entry (that would let an
// attacker set the rate-limit key by hand and rotate it) — fall through to the
// direct peer (r.RemoteAddr), which cannot be spoofed at the TCP layer.
func clientIP(r *http.Request, trustedProxies int) string {
	if trustedProxies > 0 {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if idx := len(parts) - trustedProxies; idx >= 0 {
				if ip := strings.TrimSpace(parts[idx]); ip != "" {
					return ip
				}
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
