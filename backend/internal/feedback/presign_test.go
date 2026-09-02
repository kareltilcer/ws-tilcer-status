package feedback

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kareltilcer/ws-tilcer-status/backend/internal/feedback/blob"
)

// testR2 builds a real R2 client against a fake endpoint. Presigning is pure
// local computation — no request is made — so this exercises the actual SDK path
// the production code uses.
func testR2(t *testing.T) *blob.R2 {
	t.Helper()
	r2, err := blob.NewR2(blob.R2Config{
		Endpoint:  "https://account.r2.cloudflarestorage.com",
		Bucket:    "ws-tilcer-status-feedback",
		AccessKey: "AKIAEXAMPLE",
		SecretKey: "secret",
	})
	if err != nil {
		t.Fatalf("new r2: %v", err)
	}
	return r2
}

// TestPresignPutSignsContentLength is the assertion that stands between this
// design and an unbounded public write endpoint.
//
// ⚠ V3-D54's probe 4: drop ContentLength from the presign call — as a
// "simplification", or through an SDK bump — and R2 accepts a body of any size,
// stores all of it, and reports no error anywhere. The signed content-length is
// the ONLY size enforcement a presigned PUT can have, so this runs before any
// upload test rather than after one.
func TestPresignPutSignsContentLength(t *testing.T) {
	up, err := testR2(t).PresignPut(context.Background(), "feedback/home/R-7QK2/0-abc.png", "image/png", 1024, time.Minute)
	if err != nil {
		t.Fatalf("presign: %v", err)
	}
	u, err := url.Parse(up.URL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	signed := u.Query().Get("X-Amz-SignedHeaders")
	if signed == "" {
		t.Fatalf("presigned URL carries no X-Amz-SignedHeaders: %s", up.URL)
	}
	for _, want := range []string{"content-length", "content-type"} {
		if !strings.Contains(signed, want) {
			t.Fatalf("X-Amz-SignedHeaders = %q, must contain %q — without it the bucket is an open upload endpoint that reports no error", signed, want)
		}
	}
	// The headers handed to the client must be exactly what was signed, or every
	// upload fails with a signature mismatch the reporter never sees.
	if got := up.Headers["Content-Length"]; got != "1024" {
		t.Fatalf("Content-Length header = %q, want 1024", got)
	}
	if got := up.Headers["Content-Type"]; got != "image/png" {
		t.Fatalf("Content-Type header = %q, want image/png", got)
	}
}

// TestPresignGetIsSignedAndExpiring covers the view URL: R2 serves the range
// requests, so no attachment byte passes through the droplet.
func TestPresignGetIsSignedAndExpiring(t *testing.T) {
	url1, expires, err := testR2(t).PresignGet(context.Background(), "feedback/home/R-7QK2/0-abc.png", 5*time.Minute)
	if err != nil {
		t.Fatalf("presign get: %v", err)
	}
	if !strings.Contains(url1, "X-Amz-Signature=") {
		t.Fatalf("view URL is not signed: %s", url1)
	}
	if d := time.Until(expires); d <= 0 || d > 6*time.Minute {
		t.Fatalf("view URL expiry %s is outside the configured TTL", d)
	}
}

// TestDeclaredSizeIsClampedBeforeSigning: an over-declared file is signed at the
// cap, so the bucket cannot be filled beyond it. The client that declared more
// then gets a URL that refuses the file it holds — the intended outcome.
func TestDeclaredSizeIsClampedBeforeSigning(t *testing.T) {
	h := newHarness(t, testConfig())
	body := h.submission(DeclaredFile{ContentType: "video/mp4", ByteSize: 500 << 20}) // 500 MB, cap is 50
	code, raw := h.do(http.MethodPost, "/api/ingest/"+testSite+"/feedback", body, widgetHeaders(h.key))
	if code != http.StatusAccepted {
		t.Fatalf("submit = %d, want 202 (%s)", code, raw)
	}
	var out Accepted
	mustJSON(t, raw, &out)
	if len(out.Uploads) != 1 {
		t.Fatalf("want one upload slot, got %d", len(out.Uploads))
	}
	want := strconv.FormatInt(h.mod.cfg.MaxVideoBytes, 10)
	if got := out.Uploads[0].Headers["Content-Length"]; got != want {
		t.Fatalf("signed Content-Length = %q, want the cap %q", got, want)
	}
}

// TestRealBucketPresignAcceptsExactSize is the one opt-in integration test: it
// runs against the real bucket only when STATUS_R2_TEST_* is set, and is skipped
// otherwise. It is deliberately not a CI job — the point is a reproducer for the
// V3-D54 probes, not a container in the pipeline.
func TestRealBucketPresignAcceptsExactSize(t *testing.T) {
	endpoint := os.Getenv("STATUS_R2_TEST_ENDPOINT")
	bucket := os.Getenv("STATUS_R2_TEST_BUCKET")
	key := os.Getenv("STATUS_R2_TEST_ACCESS_KEY_ID")
	secret := os.Getenv("STATUS_R2_TEST_SECRET_ACCESS_KEY")
	if endpoint == "" || bucket == "" || key == "" || secret == "" {
		t.Skip("set STATUS_R2_TEST_ENDPOINT/BUCKET/ACCESS_KEY_ID/SECRET_ACCESS_KEY to run against the real bucket")
	}
	r2, err := blob.NewR2(blob.R2Config{Endpoint: endpoint, Bucket: bucket, AccessKey: key, SecretKey: secret})
	if err != nil {
		t.Fatalf("new r2: %v", err)
	}
	ctx := context.Background()
	objKey := "feedback/_test/" + strconv.FormatInt(time.Now().UnixNano(), 10) + ".png"
	payload := make([]byte, 1024)

	up, err := r2.PresignPut(ctx, objKey, "image/png", int64(len(payload)), 5*time.Minute)
	if err != nil {
		t.Fatalf("presign: %v", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, up.URL, strings.NewReader(string(payload)))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	for k, v := range up.Headers {
		req.Header.Set(k, v)
	}
	req.ContentLength = int64(len(payload))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put = %d, want 200", resp.StatusCode)
	}
	t.Cleanup(func() { _ = r2.Delete(ctx, objKey) })

	size, err := r2.Head(ctx, objKey)
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	if size != int64(len(payload)) {
		t.Fatalf("stored %d bytes, want %d", size, len(payload))
	}
}

// TestUploadSlotsAreAllOrNothing — openapi's FeedbackAccepted promises "one slot
// per declared file, in the order declared", and the widget pairs the slots with
// its own File list by index. A short list would therefore not drop one file; it
// would shift every file after the gap onto the wrong slot, so a presign that
// fails part-way issues no slots at all.
//
// ⚠ The report itself survives regardless: the text is the thing worth keeping,
// its attachment rows stay pending, and the nightly sweep resolves them.
func TestUploadSlotsAreAllOrNothing(t *testing.T) {
	h := newHarness(t, testConfig())
	h.blobs.SetPresignErr(errFakeUnreachable)

	body := h.submission(
		DeclaredFile{ContentType: "image/png", ByteSize: 1024},
		DeclaredFile{ContentType: "image/png", ByteSize: 2048},
	)
	code, raw := h.do(http.MethodPost, "/api/ingest/"+testSite+"/feedback", body, widgetHeaders(h.key))
	if code != http.StatusAccepted {
		t.Fatalf("submit = %d, want 202 — a bucket that will not sign must not lose the report (%s)", code, raw)
	}
	var out Accepted
	mustJSON(t, raw, &out)
	if len(out.Uploads) != 0 {
		t.Fatalf("got %d upload slots for 2 declared files; a partial list shifts every later file onto the wrong slot", len(out.Uploads))
	}
	if out.Ref == "" {
		t.Fatal("the report must still be accepted and named")
	}

	// The rows are there, pending, for the sweep to resolve.
	h.blobs.SetPresignErr(nil)
	if code, raw := h.do(http.MethodGet, "/api/reports/"+out.Ref, nil, nil); code != http.StatusOK {
		t.Fatalf("the report should be readable in the inbox: %d %s", code, raw)
	} else {
		var rep Report
		mustJSON(t, raw, &rep)
		if len(rep.Attachments) != 2 {
			t.Fatalf("want 2 pending attachment rows, got %d", len(rep.Attachments))
		}
		for _, a := range rep.Attachments {
			if a.State != AttachPending {
				t.Fatalf("attachment %d state = %s, want pending", a.ID, a.State)
			}
		}
	}
}
