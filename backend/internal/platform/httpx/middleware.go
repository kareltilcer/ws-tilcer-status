package httpx

import (
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

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

// StripAPIPrefix normalizes the request path to handle reverse proxy
// misconfigurations. It handles two common scenarios:
//
// 1. Double prefix: upstream includes "/api" (e.g. Coolify upstream = :112/api)
//    /api/api/auth/login → /api/auth/login
//
// 2. Stripped prefix: "Strip Prefix" is enabled on a /api path rule
//    /auth/login → /api/auth/login
//    /sites → /api/sites
//
// Health probes (/healthz, /readyz) are never rewritten.
func StripAPIPrefix(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// Health probes: never rewrite
		if path == "/healthz" || path == "/readyz" {
			next.ServeHTTP(w, r)
			return
		}

		// Double prefix case: /api/api/... → /api/...
		if strings.HasPrefix(path, "/api/api") {
			r.URL.Path = strings.TrimPrefix(path, "/api")
			next.ServeHTTP(w, r)
			return
		}

		// Exact /api → /
		if path == "/api" {
			r.URL.Path = "/"
			next.ServeHTTP(w, r)
			return
		}

		// Stripped prefix case: path doesn't start with /api but looks like
		// an API endpoint that was stripped by the proxy
		if !strings.HasPrefix(path, "/api") {
			// Common API path prefixes that might be stripped
			apiPaths := []string{"/auth/", "/sites", "/crashes", "/ingest/"}
			for _, prefix := range apiPaths {
				if strings.HasPrefix(path, prefix) || path == strings.TrimSuffix(prefix, "/") {
					r.URL.Path = "/api" + path
					break
				}
			}
		}

		next.ServeHTTP(w, r)
	})
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
