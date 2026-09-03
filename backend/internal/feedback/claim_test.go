package feedback

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// errFakeUnreachable stands in for a bucket that cannot be reached.
var errFakeUnreachable = errors.New("r2 unreachable")

// upload runs the browser's half of the flow against the fake bucket, with the
// exact headers the slot was signed for.
func (h *harness) upload(slot UploadSlot, body []byte) {
	h.t.Helper()
	declared := int64(len(body))
	if v, ok := slot.Headers["Content-Length"]; ok {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			h.t.Fatalf("signed Content-Length %q is not a number: %v", v, err)
		}
		declared = n
	}
	if err := h.blobs.Upload(slot.URL, slot.Headers["Content-Type"], declared, body); err != nil {
		h.t.Fatalf("upload: %v", err)
	}
}

// submitWithFile submits a report declaring one PNG of size bytes and returns the
// accepted response.
func (h *harness) submitWithFile(size int64) Accepted {
	h.t.Helper()
	body := h.submission(DeclaredFile{ContentType: "image/png", ByteSize: size})
	code, raw := h.do(http.MethodPost, "/api/ingest/"+testSite+"/feedback", body, widgetHeaders(h.key))
	if code != http.StatusAccepted {
		h.t.Fatalf("submit = %d, want 202 (%s)", code, raw)
	}
	var out Accepted
	mustJSON(h.t, raw, &out)
	if len(out.Uploads) != 1 {
		h.t.Fatalf("want one upload slot, got %d", len(out.Uploads))
	}
	return out
}

func (h *harness) claim(ref string) []AttachmentSummary {
	h.t.Helper()
	code, raw := h.do(http.MethodPost, "/api/ingest/"+testSite+"/feedback/"+ref+"/claim", nil, widgetHeaders(h.key))
	if code != http.StatusOK {
		h.t.Fatalf("claim = %d, want 200 (%s)", code, raw)
	}
	var out ClaimResult
	mustJSON(h.t, raw, &out)
	return out.Attachments
}

// TestClaimRecordsTheSizeR2Reports covers the honest path and, with it, the
// truncation semantics: R2 reads exactly Content-Length bytes off the wire and
// answers 200 (V3-D54, probe 3), so an object matching its declared length is
// present and within the cap — and nothing more.
func TestClaimRecordsTheSizeR2Reports(t *testing.T) {
	h := newHarness(t, testConfig())

	t.Run("honest upload is stored", func(t *testing.T) {
		acc := h.submitWithFile(1024)
		h.upload(acc.Uploads[0], bytes.Repeat([]byte("a"), 1024))
		att := h.claim(acc.Ref)
		if len(att) != 1 || att[0].State != AttachStored {
			t.Fatalf("attachment = %+v, want one stored", att)
		}
		if att[0].ByteSize == nil || *att[0].ByteSize != 1024 {
			t.Fatalf("byte_size = %v, want the size R2 reported (1024)", att[0].ByteSize)
		}
	})

	t.Run("a body that lies about its length is truncated, not refused", func(t *testing.T) {
		acc := h.submitWithFile(1024)
		// The client declares the signed 1 KiB and sends 64 KiB.
		h.upload(acc.Uploads[0], bytes.Repeat([]byte("b"), 64*1024))
		if size, ok := h.blobs.Size(objectKeyOf(t, h, acc.Ref)); !ok || size != 1024 {
			t.Fatalf("stored %d bytes (present=%v), want exactly the signed 1024 — the bucket must not be fillable beyond the signed size", size, ok)
		}
		att := h.claim(acc.Ref)
		// ⚠ It is `stored` because the object is present at its declared length.
		// That is a presence and cap check, NOT an integrity check.
		if att[0].State != AttachStored {
			t.Fatalf("state = %s, want stored", att[0].State)
		}
	})

	t.Run("an object that never arrived is missing and the report survives", func(t *testing.T) {
		acc := h.submitWithFile(2048)
		att := h.claim(acc.Ref)
		if att[0].State != AttachMissing {
			t.Fatalf("state = %s, want missing", att[0].State)
		}
		if att[0].ByteSize != nil {
			t.Fatalf("a missing attachment must publish no size, got %v", *att[0].ByteSize)
		}
		code, raw := h.do(http.MethodGet, "/api/reports/"+acc.Ref, nil, nil)
		if code != http.StatusOK {
			t.Fatalf("report after a failed upload = %d, want 200", code)
		}
		var rep Report
		mustJSON(t, raw, &rep)
		if rep.Message == "" {
			t.Fatal("a report is never rejected because an attachment failed — the text is the thing worth keeping")
		}
	})

	t.Run("a stalled upload is missing", func(t *testing.T) {
		acc := h.submitWithFile(4096)
		// Signed for 4096; the fake refuses a mismatched declaration exactly as R2
		// does, so simulate the stall by storing a short object directly.
		h.blobs.Put(objectKeyOf(t, h, acc.Ref), 100, time.Now().UTC())
		att := h.claim(acc.Ref)
		if att[0].State != AttachMissing {
			t.Fatalf("state = %s, want missing for an object smaller than declared", att[0].State)
		}
	})

	t.Run("claim is idempotent", func(t *testing.T) {
		acc := h.submitWithFile(512)
		h.upload(acc.Uploads[0], bytes.Repeat([]byte("c"), 512))
		first := h.claim(acc.Ref)
		second := h.claim(acc.Ref)
		if len(first) != 1 || len(second) != 1 || first[0].State != second[0].State {
			t.Fatalf("claim is not idempotent: %+v then %+v", first, second)
		}
	})
}

// TestClaimIsScopedToItsSite: a claim can only ever touch the report it names,
// and only for the site whose key it presented.
func TestClaimIsScopedToItsSite(t *testing.T) {
	h := newHarness(t, testConfig())
	acc := h.submitWithFile(256)

	h.seedSite("fin", "Fin")
	finKey := h.enable("fin")
	code, _ := h.do(http.MethodPost, "/api/ingest/fin/feedback/"+acc.Ref+"/claim", nil,
		map[string]string{"X-Widget-Key": finKey, "Origin": testOrigin})
	if code != http.StatusNotFound {
		t.Fatalf("claiming another site's report = %d, want 404", code)
	}
	if code, _ := h.do(http.MethodPost, "/api/ingest/"+testSite+"/feedback/R-0000/claim", nil, widgetHeaders(h.key)); code != http.StatusNotFound {
		t.Fatalf("claiming an unknown ref = %d, want 404", code)
	}
	if code, _ := h.do(http.MethodPost, "/api/ingest/"+testSite+"/feedback/not-a-ref/claim", nil, widgetHeaders(h.key)); code != http.StatusNotFound {
		t.Fatalf("claiming a malformed ref = %d, want 404", code)
	}
}

// TestAbandonedDialogLeavesZeroBytes: a report whose files are never uploaded
// leaves nothing in the bucket at all.
func TestAbandonedDialogLeavesZeroBytes(t *testing.T) {
	h := newHarness(t, testConfig())
	h.submitWithFile(1024) // slot issued, nothing uploaded
	if keys := h.blobs.Keys(); len(keys) != 0 {
		t.Fatalf("an abandoned dialog left %d objects in the bucket: %v", len(keys), keys)
	}
}

// TestAttachmentURLOnlyForStored: a pending or missing attachment yields 404
// rather than a signed link to nothing.
func TestAttachmentURLOnlyForStored(t *testing.T) {
	h := newHarness(t, testConfig())
	acc := h.submitWithFile(1024)
	id := acc.Uploads[0].AttachmentID
	path := func(ref string, id int64) string {
		return "/api/reports/" + ref + "/attachments/" + strconv.FormatInt(id, 10) + "/url"
	}

	if code, _ := h.do(http.MethodGet, path(acc.Ref, id), nil, nil); code != http.StatusNotFound {
		t.Fatalf("view URL for a pending attachment = %d, want 404", code)
	}
	h.upload(acc.Uploads[0], bytes.Repeat([]byte("d"), 1024))
	h.claim(acc.Ref)

	code, raw := h.do(http.MethodGet, path(acc.Ref, id), nil, nil)
	if code != http.StatusOK {
		t.Fatalf("view URL for a stored attachment = %d, want 200 (%s)", code, raw)
	}
	var out AttachmentURL
	mustJSON(t, raw, &out)
	if out.URL == "" || out.ExpiresAt == "" || out.ContentType != "image/png" {
		t.Fatalf("view URL response is incomplete: %+v", out)
	}
	if code, _ := h.do(http.MethodGet, path(acc.Ref, id+999), nil, nil); code != http.StatusNotFound {
		t.Fatalf("view URL for an unknown attachment = %d, want 404", code)
	}
}

// TestDeleteReportRemovesRowThenObjects covers FR-22's normative order and the
// orphan path when the bucket is unreachable.
func TestDeleteReportRemovesRowThenObjects(t *testing.T) {
	h := newHarness(t, testConfig())
	acc := h.submitWithFile(1024)
	h.upload(acc.Uploads[0], bytes.Repeat([]byte("e"), 1024))
	h.claim(acc.Ref)
	key := objectKeyOf(t, h, acc.Ref)

	if code, raw := h.do(http.MethodDelete, "/api/reports/"+acc.Ref, nil, nil); code != http.StatusNoContent {
		t.Fatalf("delete = %d, want 204 (%s)", code, raw)
	}
	// ⚠ The response deliberately does not wait on R2 (FR-22), so the objects are
	// observed after Drain rather than immediately — which is also the assertion
	// that the delete is detached at all.
	h.mod.Drain()
	if _, ok := h.blobs.Size(key); ok {
		t.Fatal("the object outlived its report")
	}
	if code, _ := h.do(http.MethodGet, "/api/reports/"+acc.Ref, nil, nil); code != http.StatusNotFound {
		t.Fatalf("deleted report = %d, want 404", code)
	}

	// A delete issued while the bucket is unreachable still removes the row and
	// leaves an orphan for the sweep — the response does not wait on R2.
	acc2 := h.submitWithFile(1024)
	h.upload(acc2.Uploads[0], bytes.Repeat([]byte("f"), 1024))
	h.claim(acc2.Ref)
	orphan := objectKeyOf(t, h, acc2.Ref)
	h.blobs.SetDeleteErr(errFakeUnreachable)
	if code, _ := h.do(http.MethodDelete, "/api/reports/"+acc2.Ref, nil, nil); code != http.StatusNoContent {
		t.Fatal("a delete must not fail because object storage is unreachable")
	}
	h.mod.Drain()
	h.blobs.SetDeleteErr(nil)
	if _, ok := h.blobs.Size(orphan); !ok {
		t.Fatal("expected the object to survive as an orphan for the sweep")
	}
}

// TestDeleteDoesNotWaitOnObjectStorage — FR-22 and openapi 0.3.0 both say the
// response does not wait on R2. With SetMaxOpenConns(1) the transaction is
// already committed by then, so a bucket that will not answer must cost the admin
// a slow 204 rather than a hung one.
func TestDeleteDoesNotWaitOnObjectStorage(t *testing.T) {
	h := newHarness(t, testConfig())
	acc := h.submitWithFile(1024)
	h.upload(acc.Uploads[0], bytes.Repeat([]byte("g"), 1024))
	h.claim(acc.Ref)
	key := objectKeyOf(t, h, acc.Ref)

	release := h.blobs.BlockDeletes()
	done := make(chan int, 1)
	go func() {
		code, _ := h.do(http.MethodDelete, "/api/reports/"+acc.Ref, nil, nil)
		done <- code
	}()
	select {
	case code := <-done:
		if code != http.StatusNoContent {
			t.Fatalf("delete = %d, want 204", code)
		}
	case <-time.After(5 * time.Second):
		release()
		t.Fatal("the delete response waited on object storage — an unreachable bucket must not hold an admin request open")
	}

	// The row is already gone; the object follows once the bucket answers.
	if code, _ := h.do(http.MethodGet, "/api/reports/"+acc.Ref, nil, nil); code != http.StatusNotFound {
		t.Fatal("the row must be gone before the object is")
	}
	release()
	h.mod.Drain()
	if _, ok := h.blobs.Size(key); ok {
		t.Fatal("the object outlived its report")
	}
}

// TestClaimDoesNotSpendTheReportBudget — STATUS_FEEDBACK_RATE and
// STATUS_FEEDBACK_IP_RATE are denominated in reports (PRD §V3-9), so a report
// must cost exactly one token.
//
// ⚠ Charging the claim to the same buckets made every report cost two, which at
// the shipped defaults (IP burst 3) refused the claim of a reporter's second
// report — and a claim that never lands leaves its attachments pending until the
// sweep deletes the bytes they already uploaded.
func TestClaimDoesNotSpendTheReportBudget(t *testing.T) {
	cfg := testConfig()
	cfg.RatePerSec, cfg.Burst = 0.0056, 2 // the shipped rate, two reports of headroom
	cfg.IPRatePerSec, cfg.IPBurst = 0.0014, 2
	h := newHarness(t, cfg)

	for i := 0; i < 2; i++ {
		acc := h.submitWithFile(64) // fatals on a 429
		h.upload(acc.Uploads[0], bytes.Repeat([]byte("h"), 64))
		if att := h.claim(acc.Ref); att[0].State != AttachStored {
			t.Fatalf("report %d: attachment state %s, want stored", i, att[0].State)
		}
	}
}

// objectKeyOf returns the single attachment key of a report.
func objectKeyOf(t *testing.T, h *harness, ref string) string {
	t.Helper()
	var key string
	err := h.db.QueryRow(
		`SELECT a.object_key FROM feedback_attachment a JOIN feedback_report r ON r.id = a.report_id WHERE r.ref = ?`,
		ref).Scan(&key)
	if err != nil {
		t.Fatalf("object key for %s: %v", ref, err)
	}
	return key
}

// TestClaimAlwaysAnswersAnAttachmentArray — openapi requires `attachments` to be
// a present array, and a widget that iterates a null throws into the host page,
// which V3-D37 forbids absolutely. A report deleted between the claim's lookup
// and its final read must therefore still answer [].
func TestClaimAlwaysAnswersAnAttachmentArray(t *testing.T) {
	h := newHarness(t, testConfig())

	out, err := h.mod.store.AttachmentsForRef(context.Background(), "R-ZZZZ")
	if err != nil {
		t.Fatalf("attachments for a vanished report: %v", err)
	}
	if out == nil {
		t.Fatal("a vanished report must yield an empty array, not nil — nil renders as attachments: null")
	}
	if len(out) != 0 {
		t.Fatalf("want no attachments, got %d", len(out))
	}
	raw, err := json.Marshal(ClaimResult{Attachments: out})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"attachments":[]`) {
		t.Fatalf("claim body = %s, want an empty array", raw)
	}
}
