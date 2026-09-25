package notify

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/notify/mail"
	appdb "github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/db"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/timeutil"
)

// Worker turns queued events into emails. The scheduler calls RunOnce every few
// seconds; it is the only thing in the service that sends a digest, so no row
// ever needs a "sending" claim.
type Worker struct {
	db     *sql.DB
	mailer mail.Mailer
	cfg    Config
	logger *slog.Logger
	// gap is the pause between two sends in one pass (sendGap; tests zero it).
	gap time.Duration

	mu         sync.Mutex
	lastCapLog time.Time
}

// RunOnce is one pass: assemble a digest if one is due, then deliver whatever is
// due. now is the pass's clock, as the rest of the scheduler passes it.
func (w *Worker) RunOnce(ctx context.Context, now time.Time) {
	if w.mailer == nil {
		return
	}
	if err := w.assemble(ctx, now); err != nil && ctx.Err() == nil {
		w.logger.Error("notify: assemble digest", "err", err)
	}
	w.deliver(ctx, now)
}

// assemble folds every pending event into one digest once the oldest of them has
// waited out the digest window — so a burst (a bad deploy opening fifteen groups,
// a site flapping) becomes one email, and a lone event still goes out within the
// window.
//
// ⚠ The hourly cap only ever DELAYS: while it is reached, events keep collecting
// and go out together in the next digest the cap allows. Nothing is dropped for
// volume.
//
// The whole assembly is one transaction with no network in it — rendering is
// string work — so it holds the single connection for milliseconds.
func (w *Worker) assemble(ctx context.Context, now time.Time) error {
	oldest, ok, err := oldestPending(ctx, w.db)
	if err != nil || !ok {
		return err
	}
	if t, err := timeutil.Parse(oldest); err == nil && now.Sub(t) < w.cfg.DigestWindow {
		return nil
	}
	return appdb.WithTx(ctx, w.db, func(tx *sql.Tx) error {
		n, err := digestsSince(ctx, tx, ts(now.Add(-time.Hour)))
		if err != nil {
			return err
		}
		if n >= w.cfg.MaxPerHour {
			w.logCapped(now, n)
			return nil
		}
		st, err := loadSettings(ctx, tx)
		if err != nil {
			return err
		}
		events, err := pendingEvents(ctx, tx, maxEventsPerDigest)
		if err != nil {
			return err
		}
		muted, err := mutedSites(ctx, tx)
		if err != nil {
			return err
		}
		mutedSet := make(map[string]bool, len(muted))
		for _, id := range muted {
			mutedSet[id] = true
		}
		// Re-check each event against the settings as they are NOW: a site muted,
		// a toggle switched off or the recipients cleared since the event was
		// queued means it is no longer wanted.
		var keep []event
		var keepIDs, dropIDs []int64
		for _, e := range events {
			if st.deliverable(e.Kind, mutedSet[e.SiteID]) {
				keep = append(keep, e)
				keepIDs = append(keepIDs, e.ID)
			} else {
				dropIDs = append(dropIDs, e.ID)
			}
		}
		if err := deleteEvents(ctx, tx, dropIDs); err != nil {
			return err
		}
		if len(keep) == 0 {
			return nil
		}
		r := render(keep, w.cfg.PublicURL, now)
		id, err := insertDigest(ctx, tx, digest{
			CreatedAt:  ts(now),
			Sender:     w.cfg.From,
			Recipients: st.Recipients,
			Subject:    r.Subject,
			Text:       r.Text,
			HTML:       r.HTML,
			EventCount: len(keep),
			// ⚠ Random, not derived from the row id: a database restored from a
			// Litestream snapshot reuses ids, and a reused key with a different
			// body is refused by the provider as a conflict.
			IdempotencyKey: "status-digest-" + uuid.NewString(),
			NextAttemptAt:  ts(now),
		})
		if err != nil {
			return err
		}
		return assignEvents(ctx, tx, id, keepIDs)
	})
}

func (w *Worker) logCapped(now time.Time, n int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if now.Sub(w.lastCapLog) < 10*time.Minute {
		return
	}
	w.lastCapLog = now
	w.logger.Warn("notify: hourly email cap reached; pending notifications will go out together in the next digest",
		"digests_last_hour", n, "max_per_hour", w.cfg.MaxPerHour)
}

// deliver sends the digests that are due.
//
// ⚠ Every send runs with NO transaction open and NO cursor open: the due rows are
// read into a slice first, and each outcome is written by its own single
// statement afterwards. The mail fake's probe asserts it.
func (w *Worker) deliver(ctx context.Context, now time.Time) {
	st, err := loadSettings(ctx, w.db)
	if err != nil {
		if ctx.Err() == nil {
			w.logger.Error("notify: load settings", "err", err)
		}
		return
	}
	if !st.Enabled {
		n, err := cancelPending(ctx, w.db, "cancelled: notifications were turned off")
		if err != nil {
			w.logger.Error("notify: cancel pending digests", "err", err)
		} else if n > 0 {
			w.logger.Info("notify: cancelled pending digests", "count", n)
		}
		return
	}
	due, err := dueDigests(ctx, w.db, ts(now), maxDueDigests)
	if err != nil {
		if ctx.Err() == nil {
			w.logger.Error("notify: load due digests", "err", err)
		}
		return
	}
	for i, d := range due {
		if i > 0 && !sleep(ctx, w.gap) {
			return
		}
		if !w.deliverOne(ctx, d, now) {
			return
		}
	}
}

// deliverOne sends one digest and records the outcome. It returns false when the
// pass must stop because the service is shutting down.
func (w *Worker) deliverOne(ctx context.Context, d digest, now time.Time) bool {
	at := ts(now)
	created, err := timeutil.Parse(d.CreatedAt)
	if err == nil && now.Sub(created) > giveUpAfter {
		// Past this point a retry could land outside the provider's idempotency
		// window, and an earlier attempt that timed out may in fact have been
		// delivered.
		w.settle(ctx, d, "failed", markFailed(ctx, w.db, d.ID, "expired: not delivered within 23 hours", at, false))
		return true
	}

	sendCtx, cancel := context.WithTimeout(ctx, sendTimeout)
	res, sendErr := w.mailer.Send(sendCtx, mail.Message{
		From: d.Sender, To: d.Recipients, Subject: d.Subject, Text: d.Text, HTML: d.HTML,
		IdempotencyKey: d.IdempotencyKey,
	})
	cancel()
	if ctx.Err() != nil {
		// Shutting down. Whether the provider got it is unknown; the row stays
		// pending with its attempts unchanged, and the next boot resends it with
		// the same idempotency key — which is exactly what that key is for.
		return false
	}

	switch {
	case sendErr == nil:
		w.settle(ctx, d, "sent", markSent(ctx, w.db, d.ID, res.ID, at))
	case mail.IsPermanent(sendErr):
		w.logger.Error("notify: provider refused a digest; giving up", "digest", d.ID, "err", sendErr)
		w.settle(ctx, d, "failed", markFailed(ctx, w.db, d.ID, sendErr.Error(), at, true))
	default:
		wait := backoff(d.Attempts + 1)
		if ra := mail.RetryAfterOf(sendErr); ra > wait {
			wait = ra
		}
		msg := sendErr.Error()
		if errors.Is(sendErr, context.DeadlineExceeded) {
			msg = "timed out; delivery unknown, retrying with the same idempotency key"
		}
		w.logger.Warn("notify: send failed; will retry", "digest", d.ID, "attempt", d.Attempts+1, "retry_in", wait.String(), "err", sendErr)
		w.settle(ctx, d, "retry", markRetry(ctx, w.db, d.ID, msg, at, ts(now.Add(wait))))
	}
	return true
}

func (w *Worker) settle(ctx context.Context, d digest, outcome string, err error) {
	if err != nil && ctx.Err() == nil {
		w.logger.Error("notify: record digest outcome", "digest", d.ID, "outcome", outcome, "err", err)
	}
}

// sleep waits d or until ctx ends, reporting whether it waited the full time.
func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
