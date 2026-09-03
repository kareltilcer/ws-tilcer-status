// Package blobtest is an in-memory blob.Store for tests.
//
// ⚠ It TRUNCATES an oversized upload rather than refusing it, because that is
// what R2 actually does (V3-D54, probe 3: R2 reads exactly Content-Length bytes
// off the wire, discards the rest, and answers 200). A fake that returned 403 for
// an oversized body would encode behaviour the bucket does not have, and every
// claim-step test would then pass against a fiction.
package blobtest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kareltilcer/ws-tilcer-status/backend/internal/feedback/blob"
)

// object is one stored object plus the size its upload URL was signed for.
type object struct {
	data    []byte
	created time.Time
}

// Fake is an in-memory blob.Store.
type Fake struct {
	mu      sync.Mutex
	objects map[string]*object
	signed  map[string]signedPut // presigned PUT URL → what it will accept

	// ListErr, when set, makes List fail. The sweep must then delete NOTHING: a
	// listing that came back empty because of a credential error, followed by
	// "delete everything with no live row", is how a bucket is quietly emptied.
	ListErr error
	// HeadErr, when set, makes Head fail with a transport-style error (distinct
	// from blob.ErrNotFound, which is a decision the caller acts on).
	HeadErr error
	// DeleteErr, when set, makes Delete fail — the orphan path.
	DeleteErr error

	// Calls counts every store operation, in order, for assertions about what ran
	// and when.
	Calls []string

	// TxProbe, when set, is queried on every call: if the service's single
	// connection is held by an open transaction the probe blocks and times out,
	// which records a Violation. This is how "no R2 call inside a transaction"
	// (V3-D05a) is asserted structurally rather than by review.
	TxProbe *sql.DB
	// Violations names every call that ran while the connection was held.
	Violations []string
}

type signedPut struct {
	key         string
	contentType string
	size        int64
	expires     time.Time
}

// New returns an empty fake store.
func New() *Fake {
	return &Fake{objects: map[string]*object{}, signed: map[string]signedPut{}}
}

// txProbeTimeout bounds how long the probe waits for the single connection
// before calling it held. Only a violating call ever waits this long.
const txProbeTimeout = 500 * time.Millisecond

func (f *Fake) enter(ctx context.Context, name string) {
	// Probe BEFORE taking the fake's own lock: the point is to observe the
	// database connection, not to serialize the store.
	if f.TxProbe != nil {
		pctx, cancel := context.WithTimeout(ctx, txProbeTimeout)
		err := f.TxProbe.QueryRowContext(pctx, "SELECT 1").Scan(new(int))
		cancel()
		if err != nil {
			f.mu.Lock()
			f.Violations = append(f.Violations, name)
			f.mu.Unlock()
		}
	}
	f.mu.Lock()
	f.Calls = append(f.Calls, name)
	f.mu.Unlock()
}

// PresignPut records what the URL is signed for and returns an opaque URL the
// test can PUT to via Upload.
func (f *Fake) PresignPut(ctx context.Context, key, contentType string, size int64, ttl time.Duration) (blob.Upload, error) {
	f.enter(ctx, "PresignPut "+key)
	f.mu.Lock()
	defer f.mu.Unlock()
	u := "https://fake.r2.invalid/" + url.PathEscape(key) +
		"?X-Amz-SignedHeaders=" + url.QueryEscape("content-length;content-type;host") +
		"&X-Amz-Expires=" + fmt.Sprintf("%d", int(ttl.Seconds()))
	f.signed[u] = signedPut{key: key, contentType: contentType, size: size, expires: time.Now().UTC().Add(ttl)}
	return blob.Upload{
		URL:       u,
		Headers:   map[string]string{"Content-Type": contentType, "Content-Length": fmt.Sprintf("%d", size)},
		ExpiresAt: time.Now().UTC().Add(ttl),
	}, nil
}

func (f *Fake) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, time.Time, error) {
	f.enter(ctx, "PresignGet "+key)
	// R2 signs a URL for an absent key happily; so does this.
	return "https://fake.r2.invalid/get/" + url.PathEscape(key), time.Now().UTC().Add(ttl), nil
}

func (f *Fake) Head(ctx context.Context, key string) (int64, error) {
	f.enter(ctx, "Head "+key)
	if f.HeadErr != nil {
		return 0, f.HeadErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	o, ok := f.objects[key]
	if !ok {
		return 0, blob.ErrNotFound
	}
	return int64(len(o.data)), nil
}

func (f *Fake) Delete(ctx context.Context, key string) error {
	f.enter(ctx, "Delete "+key)
	if f.DeleteErr != nil {
		return f.DeleteErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.objects, key)
	return nil
}

func (f *Fake) List(ctx context.Context, prefix string) ([]blob.Object, error) {
	f.enter(ctx, "List "+prefix)
	if f.ListErr != nil {
		return nil, f.ListErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []blob.Object
	for k, o := range f.objects {
		if strings.HasPrefix(k, prefix) {
			out = append(out, blob.Object{Key: k, Size: int64(len(o.data)), LastModified: o.created})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// Upload simulates a browser PUT to a presigned URL, with R2's real semantics:
//   - a declared length that differs from the signed one is refused, as R2
//     refuses it (403 SignatureDoesNotMatch);
//   - a content type that differs from the signed one is refused likewise;
//   - a body longer than the declared length is TRUNCATED and the PUT succeeds.
func (f *Fake) Upload(putURL, contentType string, declaredLen int64, body []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.signed[putURL]
	if !ok {
		return errors.New("blobtest: no such presigned URL")
	}
	if time.Now().UTC().After(s.expires) {
		return errors.New("blobtest: presigned URL expired")
	}
	if declaredLen != s.size || contentType != s.contentType {
		return errors.New("blobtest: SignatureDoesNotMatch")
	}
	if int64(len(body)) > declaredLen {
		body = body[:declaredLen] // R2 reads exactly Content-Length bytes and drops the rest
	}
	f.objects[s.key] = &object{data: append([]byte(nil), body...), created: time.Now().UTC()}
	return nil
}

// Put stores an object directly, bypassing the presign flow (for seeding the
// bucket in sweep tests). at is the object's last-modified time.
func (f *Fake) Put(key string, size int, at time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[key] = &object{data: make([]byte, size), created: at}
}

// Size returns the stored size of key and whether it exists.
func (f *Fake) Size(key string) (int, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	o, ok := f.objects[key]
	if !ok {
		return 0, false
	}
	return len(o.data), true
}

// Keys returns every stored key, sorted.
func (f *Fake) Keys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.objects))
	for k := range f.objects {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
