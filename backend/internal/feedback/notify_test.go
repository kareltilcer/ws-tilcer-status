package feedback

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"testing"
)

// recordingNotifier collects the report signals submit sends, and answers with
// err — which, by the Notifier contract, means "the transaction is unusable".
type recordingNotifier struct {
	got []ReportSignal
	err error
}

func (r *recordingNotifier) ReportSubmitted(_ context.Context, _ *sql.Tx, s ReportSignal) error {
	r.got = append(r.got, s)
	return r.err
}

// TestSubmitTellsTheNotifier: the notifier learns the ref the reporter is shown,
// how many files were declared, and the message — inside the insert
// transaction, so it can never announce a report that was not kept.
func TestSubmitTellsTheNotifier(t *testing.T) {
	h := newHarness(t, testConfig())
	rec := &recordingNotifier{}
	h.mod.SetNotifier(rec)

	code, body := h.do(http.MethodPost, "/api/ingest/"+testSite+"/feedback",
		h.submission(DeclaredFile{ContentType: "image/png", ByteSize: 1024}), widgetHeaders(h.key))
	if code != http.StatusAccepted {
		t.Fatalf("submit: %d %s", code, body)
	}
	var acc Accepted
	mustJSON(t, body, &acc)
	if len(rec.got) != 1 {
		t.Fatalf("notifier saw %d reports, want 1", len(rec.got))
	}
	s := rec.got[0]
	if s.Ref != acc.Ref || s.SiteID != testSite || s.Kind != KindBug || s.Attachments != 1 ||
		s.Message != "The board does not load on my phone." || s.At.IsZero() {
		t.Fatalf("signal = %+v, want the accepted report %s", s, acc.Ref)
	}
}

// TestAnUnusableTransactionFailsTheSubmit: the notifier's error means the
// transaction is gone, so the report is not kept and the reporter is told so —
// rather than being handed a reference to nothing.
func TestAnUnusableTransactionFailsTheSubmit(t *testing.T) {
	h := newHarness(t, testConfig())
	h.mod.SetNotifier(&recordingNotifier{err: errors.New("transaction rolled back")})

	code, body := h.do(http.MethodPost, "/api/ingest/"+testSite+"/feedback", h.submission(), widgetHeaders(h.key))
	if code != http.StatusInternalServerError {
		t.Fatalf("submit: %d %s, want 500", code, body)
	}
	var n int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM feedback_report`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("%d reports kept (%v), want none", n, err)
	}
}
