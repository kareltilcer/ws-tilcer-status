// Package blob is the object-storage half of the feedback module: presigned
// PUT/GET URLs, HEAD, delete and list, behind one small interface so the unit
// tests never need a bucket (blob/blobtest holds the fake).
//
// ⚠ No call in this package may run inside a transaction or while an outer rows
// cursor is open (V3-D05a). The service runs SetMaxOpenConns(1), so a network
// round-trip holding the only connection stalls every other write for the length
// of someone else's TCP timeout. Collect what you need, close the cursor, commit,
// then talk to R2.
package blob

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound is returned by Head when the object does not exist. Every other
// failure is a real error and must not be read as "absent".
var ErrNotFound = errors.New("blob: object not found")

// Object is one listed object.
type Object struct {
	Key          string
	Size         int64
	LastModified time.Time
}

// Upload is a presigned PUT: the URL plus the exact headers the client must
// send. Content-Type and Content-Length are SIGNED, so any deviation is refused
// by R2 (403 SignatureDoesNotMatch) — that signature is the only size
// enforcement a presigned PUT can have.
type Upload struct {
	URL       string
	Headers   map[string]string
	ExpiresAt time.Time
}

// Store is the object-storage surface the feedback module uses.
type Store interface {
	// PresignPut mints an upload URL for exactly size bytes of contentType. The
	// caller clamps size to the configured cap BEFORE calling: what is signed is
	// what the bucket will accept.
	PresignPut(ctx context.Context, key, contentType string, size int64, ttl time.Duration) (Upload, error)
	// PresignGet mints a short-lived view URL. ⚠ It is a bearer token for its
	// lifetime — anyone it is forwarded to can open it until it expires.
	PresignGet(ctx context.Context, key string, ttl time.Duration) (url string, expiresAt time.Time, err error)
	// Head returns the stored size of an object, or ErrNotFound.
	Head(ctx context.Context, key string) (size int64, err error)
	// Delete removes an object. Deleting an absent object is not an error.
	Delete(ctx context.Context, key string) error
	// List returns every object under prefix. It either returns the complete
	// listing or an error — a partial listing must never be reported as complete,
	// because the sweep treats "not in the listing" as "safe to keep" and
	// "not in the database" as "safe to delete".
	List(ctx context.Context, prefix string) ([]Object, error)
}
