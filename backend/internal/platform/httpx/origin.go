package httpx

import (
	"net/http"
	"net/url"
	"strings"
)

// Origin matching lives here, in the one package both callers already import:
// the CSRF gate in platform/auth (which imports httpx) and the CORS middleware
// below it. A second matcher with different semantics in one binary is how one
// of them ends up wrong (V3-D50) — STATUS_ALLOWED_ORIGINS is the only origin
// allow-list this service has.

// OriginAllowed reports whether a request's Origin — or, absent that, the origin
// implied by its Referer — is within allowed. The Referer fallback exists for
// cookie-authenticated mutations, where an unverifiable origin must fail closed;
// CORS deliberately does not use it (a preflight always carries Origin, and a
// Referer is not the thing a browser enforces the response against).
func OriginAllowed(r *http.Request, allowed []string) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		if ref := r.Header.Get("Referer"); ref != "" {
			if u, err := url.Parse(ref); err == nil {
				origin = u.Scheme + "://" + u.Host
			}
		}
	}
	if origin == "" {
		return false // cannot verify a cookie-authenticated mutation
	}
	return MatchOrigin(origin, allowed...)
}

// MatchOrigin reports whether origin satisfies any of the patterns. A pattern is
// either an exact origin ("https://status.tilcer.cz") or a single-label wildcard
// ("https://*.tilcer.cz", which matches a subdomain but not the bare apex).
func MatchOrigin(origin string, patterns ...string) bool {
	for _, p := range patterns {
		if matchOrigin(origin, p) {
			return true
		}
	}
	return false
}

func matchOrigin(origin, pattern string) bool {
	if origin == pattern {
		return true
	}
	scheme, host, ok := splitOrigin(origin)
	pScheme, pHost, ok2 := splitOrigin(pattern)
	if !ok || !ok2 || scheme != pScheme {
		return false
	}
	if strings.HasPrefix(pHost, "*.") {
		suffix := pHost[1:] // ".tilcer.cz"
		return strings.HasSuffix(host, suffix) && host != suffix[1:]
	}
	return false
}

func splitOrigin(o string) (scheme, host string, ok bool) {
	i := strings.Index(o, "://")
	if i < 0 {
		return "", "", false
	}
	return o[:i], o[i+3:], true
}
