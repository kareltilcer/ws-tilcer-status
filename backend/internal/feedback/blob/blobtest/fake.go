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

	// The injected failures. ⚠ They are read and written under mu like every other
	// field: object deletes run detached from the request that ordered them
	// (FR-22), so a test can be setting one while a call is in flight.
	//
	//   listErr    — makes List fail. The sweep must then delete NOTHING: a listing
	//                that came back empty because of a credential error, followed
	//                by "delete everything with no live row", is how a bucket is
	//                quietly emptied.
	//   headErr    — makes Head fail with a transport-style error (distinct from
	//                blob.ErrNotFound, which is a decision the caller acts on).
	//   deleteErr  — makes Delete fail: the orphan path.
	//   presignErr — makes PresignPut fail, which is the only way to reach the
	//                submit handler's all-or-nothing slot path.
	listErr    error
	headErr    error
	deleteErr  error
	presignErr error
	// presignFailAt is which PresignPut call presignErr applies to: -1 for every
	// one, otherwise the zero-based index of the single call that fails. See
	// FailPresignOn for why failing exactly one in the middle is the case that
	// matters. presigns counts the calls served.
	presignFailAt int
	presigns      int
	// deleteGate, when non-nil, holds every Delete until it is closed. See
	// BlockDeletes.
	deleteGate chan struct{}

	// calls records every store operation, in order, for assertions about what ran
	// and when. Read it with Calls().
	calls []string

	// txProbe, when set, is queried on every call: if the service's single
	// connection is held by an open transaction the probe blocks and times out,
	// which records a violation. This is how "no R2 call inside a transaction"
	// (V3-D05a) is asserted structurally rather than by review. Set it with
	// SetTxProbe — it is read from the goroutine a detached delete runs on.
	txProbe *sql.DB
	// violations names every call that ran while the connection was held. Read it
	// with Violations().
	violations []string
}

type signedPut struct {
	key         string
	contentType string
	size        int64
	expires     time.Time
}

// New returns an empty fake store.
func New() *Fake {
	return &Fake{objects: map[string]*object{}, signed: map[string]signedPut{}, presignFailAt: -1}
}

// SetListErr makes List fail with err (nil clears it).
func (f *Fake) SetListErr(err error) { f.mu.Lock(); f.listErr = err; f.mu.Unlock() }

// SetHeadErr makes Head fail with err (nil clears it).
func (f *Fake) SetHeadErr(err error) { f.mu.Lock(); f.headErr = err; f.mu.Unlock() }

// SetDeleteErr makes Delete fail with err (nil clears it).
func (f *Fake) SetDeleteErr(err error) { f.mu.Lock(); f.deleteErr = err; f.mu.Unlock() }

// SetPresignErr makes every PresignPut fail with err (nil clears it). It exists
// so the submit handler's all-or-nothing slot path is reachable from a test: the
// contract promises "one slot per declared file, in the order declared", and the
// widget pairs slots with its own File list by index.
func (f *Fake) SetPresignErr(err error) {
	f.mu.Lock()
	f.presignErr, f.presignFailAt = err, -1
	f.mu.Unlock()
}

// FailPresignOn makes only the nth PresignPut this fake serves fail with err
// (zero-based, counted over the fake's whole life); every other call succeeds.
//
// ⚠ Failing exactly one call in the MIDDLE is the only way to tell the submit
// handler's all-or-nothing withdrawal from simply dropping the failed slot. With
// the failure on the last declared file both behaviours leave zero slots and an
// assertion cannot separate them; with it on the second of three, dropping the
// gap answers one slot for three files and shifts the widget's third File onto
// the first slot.
func (f *Fake) FailPresignOn(n int, err error) {
	f.mu.Lock()
	f.presignErr, f.presignFailAt = err, n
	f.mu.Unlock()
}

// SetTxProbe makes every call probe db before it runs — see txProbe. It is a
// setter rather than a field because a detached object delete (FR-22) reads it
// from its own goroutine.
func (f *Fake) SetTxProbe(db *sql.DB) { f.mu.Lock(); f.txProbe = db; f.mu.Unlock() }

// BlockDeletes makes every Delete wait until the returned release is called. It
// is how a test proves a delete response does not wait on the bucket (FR-22):
// the 204 has to arrive while the deletes are still held here.
func (f *Fake) BlockDeletes() (release func()) {
	gate := make(chan struct{})
	f.mu.Lock()
	f.deleteGate = gate
	f.mu.Unlock()
	return func() {
		f.mu.Lock()
		f.deleteGate = nil
		f.mu.Unlock()
		close(gate)
	}
}

// txProbeTimeout bounds how long the probe waits for the single connection
// before calling it held. Only a violating call ever waits this long.
const txProbeTimeout = 500 * time.Millisecond

func (f *Fake) enter(ctx context.Context, name string) {
	f.mu.Lock()
	probe := f.txProbe
	f.mu.Unlock()
	// Query the probe WITHOUT the fake's own lock held: the point is to observe
	// the database connection, not to serialize the store.
	if probe != nil {
		pctx, cancel := context.WithTimeout(ctx, txProbeTimeout)
		err := probe.QueryRowContext(pctx, "SELECT 1").Scan(new(int))
		cancel()
		if err != nil {
			f.mu.Lock()
			f.violations = append(f.violations, name)
			f.mu.Unlock()
		}
	}
	f.mu.Lock()
	f.calls = append(f.calls, name)
	f.mu.Unlock()
}

// Calls returns the store operations recorded so far, in order.
//
// ⚠ It copies under the lock, and the field behind it is unexported, because
// object deletes run in a goroutine detached from the request that ordered them
// (FR-22): a test reading the slice directly would race with a delete still in
// flight. Call Drain first when the assertion is about a delete having happened.
func (f *Fake) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// Violations returns the calls that ran while the single database connection was
// held — see TxProbe. Copied under the lock, for the same reason as Calls.
func (f *Fake) Violations() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.violations...)
}

// PresignPut records what the URL is signed for and returns an opaque URL the
// test can PUT to via Upload.
func (f *Fake) PresignPut(ctx context.Context, key, contentType string, size int64, ttl time.Duration) (blob.Upload, error) {
	f.enter(ctx, "PresignPut "+key)
	f.mu.Lock()
	defer f.mu.Unlock()
	n := f.presigns
	f.presigns++
	if f.presignErr != nil && (f.presignFailAt < 0 || f.presignFailAt == n) {
		return blob.Upload{}, f.presignErr
	}
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
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.headErr != nil {
		return 0, f.headErr
	}
	o, ok := f.objects[key]
	if !ok {
		return 0, blob.ErrNotFound
	}
	return int64(len(o.data)), nil
}

func (f *Fake) Delete(ctx context.Context, key string) error {
	f.enter(ctx, "Delete "+key)
	// The gate is waited on OUTSIDE the lock: the point is to hold the delete, not
	// to hold the fake.
	f.mu.Lock()
	gate := f.deleteGate
	f.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deleteErr != nil {
		return f.deleteErr
	}
	delete(f.objects, key)
	return nil
}

func (f *Fake) List(ctx context.Context, prefix string) ([]blob.Object, error) {
	f.enter(ctx, "List "+prefix)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
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
