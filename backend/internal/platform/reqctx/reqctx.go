// Package reqctx carries request-scoped identity and metadata through the
// context. Handlers read the actor from here — never from handler arguments —
// so a handler cannot forge who did what.
package reqctx

import "context"

// Actor is the authenticated principal behind a request.
type Actor struct {
	UserID string   // auth subject id ("" for system/service)
	Type   string   // "user" | "system" | "service"
	Label  string   // human-readable label
	Roles  []string // e.g. ["editor"], ["admin"], or ["*"] for superuser
}

// RequestInfo is the operational metadata for a request, tying handlers to the
// stdout request log via RequestID.
type RequestInfo struct {
	RequestID string
	IP        string
	UserAgent string
	Site      string
	// ClientID is an opaque per-tab identifier the browser may send
	// (X-Client-Id). Empty for non-browser callers.
	ClientID string
}

type ctxKey int

const (
	actorKey ctxKey = iota
	requestKey
)

// WithActor returns a context carrying a.
func WithActor(ctx context.Context, a Actor) context.Context {
	return context.WithValue(ctx, actorKey, a)
}

// ActorFrom returns the actor stored in ctx, if any.
func ActorFrom(ctx context.Context) (Actor, bool) {
	a, ok := ctx.Value(actorKey).(Actor)
	return a, ok
}

// WithRequest returns a context carrying r.
func WithRequest(ctx context.Context, r RequestInfo) context.Context {
	return context.WithValue(ctx, requestKey, r)
}

// RequestFrom returns the request info stored in ctx, if any.
func RequestFrom(ctx context.Context) (RequestInfo, bool) {
	r, ok := ctx.Value(requestKey).(RequestInfo)
	return r, ok
}

// HasRole reports whether roles grants access to any of allowed. The superuser
// token "*" always grants access.
func HasRole(roles []string, allowed ...string) bool {
	for _, r := range roles {
		if r == "*" {
			return true
		}
		for _, a := range allowed {
			if r == a {
				return true
			}
		}
	}
	return false
}
