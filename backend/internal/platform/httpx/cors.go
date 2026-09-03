package httpx

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// corsAllowHeaders is the request-header allow-list echoed on a preflight. None
// of these is CORS-safelisted, so every call carrying one is preflighted: the
// crash client sends Content-Type + X-Ingest-Key, the feedback widget sends
// Content-Type + X-Widget-Key.
var corsAllowHeaders = strings.Join([]string{"Content-Type", "X-Ingest-Key", "X-Widget-Key"}, ", ")

// corsAllowMethods covers the whole public surface: GET (session, widget config),
// POST (login, logout, ingest, submit, claim) and the preflight itself.
var corsAllowMethods = strings.Join([]string{http.MethodGet, http.MethodPost, http.MethodOptions}, ", ")

// corsMaxAge bounds how long a browser may cache a preflight result.
const corsMaxAge = 10 * time.Minute

// corsExposeHeaders are the RESPONSE headers a cross-origin caller may read.
//
// ⚠ Only seven response headers are CORS-safelisted, and Retry-After is not one
// of them: without this, `res.headers.get('Retry-After')` on a 429 is null in
// every browser, and the widget's "try again in N minutes" silently becomes its
// hard-coded fallback no matter what the limiter actually computed.
var corsExposeHeaders = strings.Join([]string{"Retry-After"}, ", ")

// NewCORS returns middleware that answers cross-origin requests for the PUBLIC
// group only — /api/auth/*, crash ingest, and (v3) the widget routes. The gated
// group stays same-origin: the dashboard is served from the status origin and
// nothing else may reach it with credentials.
//
// The matched origin is echoed, never "*", against the service-level
// STATUS_ALLOWED_ORIGINS list (V3-D50). Vary: Origin is set on every response,
// matched or not, so a shared cache can never serve one origin's response to
// another.
//
// ⚠ Access-Control-Allow-Credentials is NEVER sent (V3-D43). These endpoints
// authenticate by key, not by cookie; sending it would turn a public ingest
// endpoint into a cross-origin door into a session.
//
// ⚠ Middleware alone is not enough. Chi attaches group middleware to the matched
// endpoint, so an OPTIONS that matches no route falls through to the router's
// JSON 404 with no CORS headers and the browser blocks the real request. Every
// public path therefore also registers an explicit OPTIONS handler — see
// NewRouter, which derives them from the routes the public mounts registered.
func NewCORS(allowedOrigins []string) func(http.Handler) http.Handler {
	maxAge := strconv.Itoa(int(corsMaxAge.Seconds()))
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Add("Vary", "Origin")
			if r.Method == http.MethodOptions {
				// The preflight response depends on what was asked for as well as
				// on who asked.
				h.Add("Vary", "Access-Control-Request-Method")
				h.Add("Vary", "Access-Control-Request-Headers")
			}

			origin := r.Header.Get("Origin")
			if origin == "" || !MatchOrigin(origin, allowedOrigins...) {
				// Same-origin, a non-browser client, or a foreign origin. No
				// Access-Control-Allow-Origin: the browser blocks it, and a
				// non-browser client never looked.
				next.ServeHTTP(w, r)
				return
			}

			h.Set("Access-Control-Allow-Origin", origin)
			// Expose-Headers belongs on the real response, not the preflight — a
			// preflight carries no body headers to expose — so it is set below the
			// OPTIONS branch, on the responses that have some.
			if r.Method == http.MethodOptions {
				h.Set("Access-Control-Allow-Methods", corsAllowMethods)
				h.Set("Access-Control-Allow-Headers", corsAllowHeaders)
				h.Set("Access-Control-Max-Age", maxAge)
			} else {
				h.Set("Access-Control-Expose-Headers", corsExposeHeaders)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Preflight answers a CORS preflight with 204 and no body. The CORS middleware
// wrapping it writes the Access-Control-* headers; this handler exists only so
// that OPTIONS on a public path matches a route at all instead of falling
// through to the router's JSON 404.
func Preflight(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}
