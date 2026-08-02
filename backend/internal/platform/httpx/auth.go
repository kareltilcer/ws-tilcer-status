package httpx

import (
	"net/http"

	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/reqctx"
)

// The role gates read the actor placed in context by the session middleware
// (platform/auth). They are auth-mechanism-agnostic — they only inspect roles —
// so they live in httpx and carry no dependency on the auth package (which keeps
// the router free of an auth import cycle).

// RequireWrite allows editor or admin (or the "*" superuser). Must run after the
// session middleware.
func RequireWrite(next http.Handler) http.Handler {
	return requireRole(next, "editor", "admin")
}

// RequireAdmin allows only admin (or the "*" superuser). Must run after the
// session middleware.
func RequireAdmin(next http.Handler) http.Handler {
	return requireRole(next, "admin")
}

func requireRole(next http.Handler, allowed ...string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actor, ok := reqctx.ActorFrom(r.Context())
		if !ok {
			// Session middleware did not run / no actor — treat as unauthenticated.
			WriteError(w, ErrUnauthorized("authentication required"))
			return
		}
		if !reqctx.HasRole(actor.Roles, allowed...) {
			WriteError(w, ErrForbidden("insufficient role"))
			return
		}
		next.ServeHTTP(w, r)
	})
}
