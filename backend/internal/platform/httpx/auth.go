package httpx

import (
	"context"
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

// IsAdmin reports whether the request's actor holds the admin role.
//
// It is the read-only companion to RequireAdmin, for the case a middleware
// cannot express: a route open to any session that carries ONE field only an
// admin may see. Gating such a route entirely would refuse the caller the rest
// of the response they are entitled to.
func IsAdmin(ctx context.Context) bool {
	actor, ok := reqctx.ActorFrom(ctx)
	return ok && reqctx.HasRole(actor.Roles, "admin")
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
