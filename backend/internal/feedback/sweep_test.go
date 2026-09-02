package feedback

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"

	appdb "github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/db"
)

// TestSweepMarksUnclaimedAndDeletesTheirObjects covers the first of the sweep's
// two actions (V3-D26).
func TestSweepMarksUnclaimedAndDeletesTheirObjects(t *testing.T) {
	h := newHarness(t, testConfig())
	acc := h.submitWithFile(1024)
	h.upload(acc.Uploads[0], bytes.Repeat([]byte("a"), 1024)) // uploaded, never claimed
	key := objectKeyOf(t, h, acc.Ref)

	// A day and a bit later, the claim has still not arrived.
	h.mod.Sweep(context.Background(), time.Now().UTC().Add(25*time.Hour))

	if state := h.attachmentState(acc.Ref); state != AttachMissing {
		t.Fatalf("state = %s, want missing after the unclaimed TTL", state)
	}
	if _, ok := h.blobs.Size(key); ok {
		t.Fatal("the unclaimed object should have been deleted")
	}
	// The report itself is kept: it is a hand-written artifact.
	if code, _ := h.do(http.MethodGet, "/api/reports/"+acc.Ref, nil, nil); code != http.StatusOK {
		t.Fatal("the sweep must never remove a report")
	}
}

// TestSweepDeletesNothingWhenTheListingFails is the guard against quietly
// emptying a bucket (V3-D27): a listing that fails — including one that comes
// back empty because of a credential error — must not be read as "these objects
// do not exist".
func TestSweepDeletesNothingWhenTheListingFails(t *testing.T) {
	h := newHarness(t, testConfig())
	old := time.Now().UTC().Add(-48 * time.Hour)
	h.blobs.Put("feedback/home/R-AAAA/0-orphan.png", 10, old) // an orphan the sweep would normally collect
	h.blobs.SetListErr(errors.New("credentials rejected"))

	h.mod.Sweep(context.Background(), time.Now().UTC())

	if _, ok := h.blobs.Size("feedback/home/R-AAAA/0-orphan.png"); !ok {
		t.Fatal("the sweep deleted an object after its listing failed — this is how a bucket is quietly emptied")
	}
}

// TestSweepOnlyCollectsOldOrphansUnderThePrefix covers the two constraints on the
// GC step: it deletes only under feedback/, and never an object younger than the
// TTL (a GC that can outrun an in-flight upload is a data-loss bug).
func TestSweepOnlyCollectsOldOrphansUnderThePrefix(t *testing.T) {
	h := newHarness(t, testConfig())
	now := time.Now().UTC()
	h.blobs.Put("feedback/home/R-AAAA/0-old-orphan.png", 10, now.Add(-48*time.Hour))
	h.blobs.Put("feedback/home/R-BBBB/0-fresh-orphan.png", 10, now.Add(-time.Minute))
	h.blobs.Put("litestream/status/db-000123", 10, now.Add(-48*time.Hour))

	// A live attachment, uploaded and claimed, must survive regardless of age.
	acc := h.submitWithFile(64)
	h.upload(acc.Uploads[0], bytes.Repeat([]byte("b"), 64))
	h.claim(acc.Ref)
	live := objectKeyOf(t, h, acc.Ref)

	h.mod.Sweep(context.Background(), now)

	if _, ok := h.blobs.Size("feedback/home/R-AAAA/0-old-orphan.png"); ok {
		t.Fatal("an orphan older than the TTL should have been collected")
	}
	if _, ok := h.blobs.Size("feedback/home/R-BBBB/0-fresh-orphan.png"); !ok {
		t.Fatal("the sweep deleted an object younger than the TTL — it can outrun an in-flight upload")
	}
	if _, ok := h.blobs.Size("litestream/status/db-000123"); !ok {
		t.Fatal("the sweep deleted outside the feedback/ prefix")
	}
	if _, ok := h.blobs.Size(live); !ok {
		t.Fatal("the sweep deleted a live attachment")
	}
}

// TestSweepPurgesExpiredTickets keeps the ticket table from growing without bound.
func TestSweepPurgesExpiredTickets(t *testing.T) {
	h := newHarness(t, testConfig())
	ctx := context.Background()
	now := time.Now().UTC()
	stale := newTicket(testSite, now.Add(-2*time.Hour))
	if err := h.mod.store.InsertTicket(ctx, stale, now.Add(-90*time.Minute)); err != nil {
		t.Fatalf("insert: %v", err)
	}
	fresh := newTicket(testSite, now)
	if err := h.mod.store.InsertTicket(ctx, fresh, now.Add(ticketTTL)); err != nil {
		t.Fatalf("insert: %v", err)
	}

	h.mod.Sweep(ctx, now)

	if h.ticketCount() != 1 {
		t.Fatalf("ticket rows = %d, want only the unexpired one", h.ticketCount())
	}
}

// TestSweepDoesNothingWithoutStorage: a deployment with no bucket has nothing to
// sweep, and must not act as though an empty world were the truth.
func TestSweepDoesNothingWithoutStorage(t *testing.T) {
	cfg := testConfig()
	cfg.Enabled = false
	h := newHarness(t, cfg)
	h.blobs.Put("feedback/home/R-AAAA/0-orphan.png", 10, time.Now().UTC().Add(-48*time.Hour))

	h.mod.Sweep(context.Background(), time.Now().UTC())

	if _, ok := h.blobs.Size("feedback/home/R-AAAA/0-orphan.png"); !ok {
		t.Fatal("a deployment without object storage must not delete objects")
	}
}

// TestNoObjectStorageCallInsideATransaction asserts V3-D05a structurally rather
// than by review: the fake probes the database on every call, and with
// SetMaxOpenConns(1) a probe that cannot get the connection means a transaction
// (or an open cursor) was holding it — which in production would stall every
// other write for the length of someone else's TCP timeout.
func TestNoObjectStorageCallInsideATransaction(t *testing.T) {
	h := newHarness(t, testConfig())
	h.blobs.TxProbe = h.db

	acc := h.submitWithFile(1024) // presign
	h.upload(acc.Uploads[0], bytes.Repeat([]byte("c"), 1024))
	h.claim(acc.Ref) // head
	id := acc.Uploads[0].AttachmentID
	if code, _ := h.do(http.MethodGet, "/api/reports/"+acc.Ref+"/attachments/"+strconv.FormatInt(id, 10)+"/url", nil, nil); code != http.StatusOK {
		t.Fatal("view URL should succeed") // presign get
	}
	if code, _ := h.do(http.MethodDelete, "/api/reports/"+acc.Ref, nil, nil); code != http.StatusNoContent {
		t.Fatal("delete should succeed") // delete
	}
	h.mod.Drain()                                                         // the delete is detached; wait for it before reading the probe
	h.mod.Sweep(context.Background(), time.Now().UTC().Add(48*time.Hour)) // list + delete

	if len(h.blobs.Violations) > 0 {
		t.Fatalf("object-storage calls ran while the single connection was held: %v", h.blobs.Violations)
	}
	if len(h.blobs.Calls) == 0 {
		t.Fatal("the probe never observed a call — the assertion would pass vacuously")
	}
}

// TestTxProbeDetectsACallInsideATransaction proves the detector above can fail.
// A structural assertion that cannot fail is worth nothing, so this deliberately
// makes the mistake the previous test forbids and checks that it is caught.
func TestTxProbeDetectsACallInsideATransaction(t *testing.T) {
	h := newHarness(t, testConfig())
	h.blobs.TxProbe = h.db

	err := appdb.WithTx(context.Background(), h.db, func(tx *sql.Tx) error {
		// Exactly what V3-D05a forbids: a network call while the transaction holds
		// the service's only connection.
		_, _ = h.blobs.Head(context.Background(), "feedback/home/R-AAAA/0-x.png")
		return nil
	})
	if err != nil {
		t.Fatalf("tx: %v", err)
	}
	if len(h.blobs.Violations) == 0 {
		t.Fatal("the probe missed a call made inside a transaction — TestNoObjectStorageCallInsideATransaction would pass vacuously")
	}
}

// --- small helpers ----------------------------------------------------------

func (h *harness) attachmentState(ref string) string {
	h.t.Helper()
	var state string
	if err := h.db.QueryRow(
		`SELECT a.state FROM feedback_attachment a JOIN feedback_report r ON r.id = a.report_id WHERE r.ref = ?`,
		ref).Scan(&state); err != nil {
		h.t.Fatalf("attachment state: %v", err)
	}
	return state
}

func (h *harness) ticketCount() int {
	h.t.Helper()
	var n int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM feedback_ticket`).Scan(&n); err != nil {
		h.t.Fatalf("ticket count: %v", err)
	}
	return n
}
