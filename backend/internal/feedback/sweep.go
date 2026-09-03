package feedback

import (
	"context"
	"time"
)

// Sweep is the nightly feedback job. It runs LAST in the daily chain —
// rollup → purge → sweep (V3-D25) — because it is the only step that talks to the
// network and must not be able to delay the two that keep the database honest.
//
// It does three things (V3-D26):
//  1. drops expired tickets;
//  2. marks pending attachments older than UNCLAIMED_TTL as missing and deletes
//     their objects;
//  3. deletes objects under the feedback/ prefix that are older than the TTL and
//     match no live row.
//
// ⚠ Step 3 aborts on any listing error and deletes NOTHING. A listing that comes
// back empty because of a credential error, followed by "delete everything with
// no live row", is how a bucket is quietly emptied — and this bucket is
// deliberately not backed up (PRD §V3-8). It also never touches an object younger
// than the TTL: a GC that can outrun an in-flight upload is a data-loss bug
// wearing a maintenance-job costume.
//
// ⚠ RETENTION_DAYS does not apply to reports. A report is a hand-written artifact
// and is kept until deleted; see TestRetentionDoesNotPurgeFeedback, which exists
// because the purge is a natural place for someone to add a fourth table by
// symmetry.
func (m *Module) Sweep(ctx context.Context, now time.Time) {
	var (
		expiredTickets int64
		markedMissing  int
		deleted        int
		failures       int
	)

	// ⚠ Expiring tickets is a pure database step and therefore runs OUTSIDE the
	// storage gate below. A deployment whose object storage was removed after
	// tickets had been issued has no other collector for those rows, and dropping
	// them needs no bucket.
	if n, err := m.store.PurgeExpiredTickets(ctx, now); err != nil {
		m.logger.Error("feedback sweep: purge tickets", "err", err)
		failures++
	} else {
		expiredTickets = n
	}

	// Everything below talks to the bucket. With none configured there is nothing
	// to examine, and an empty world must not be mistaken for the truth.
	if !m.storageReady() {
		m.logSweep(expiredTickets, 0, 0, 0, failures)
		return
	}
	cutoff := now.Add(-m.cfg.UnclaimedTTL)

	// 1) Unclaimed attachments: the row is settled first, then the object is
	// deleted. The rows are fully drained before the first delete — these are
	// network calls on the service's only connection.
	unclaimed, err := m.store.UnclaimedBefore(ctx, cutoff)
	if err != nil {
		m.logger.Error("feedback sweep: list unclaimed", "err", err)
		failures++
	}
	for _, u := range unclaimed {
		if err := m.store.SettleAttachment(ctx, u.ID, AttachMissing, u.Declared, now); err != nil {
			m.logger.Error("feedback sweep: mark missing", "attachment", u.ID, "err", err)
			failures++
			continue
		}
		markedMissing++
		if err := m.blobs.Delete(ctx, u.ObjectKey); err != nil {
			m.logger.Error("feedback sweep: delete unclaimed object", "key", u.ObjectKey, "err", err)
			failures++
			continue
		}
		deleted++
	}

	// 2) Orphan objects. The bucket is listed FIRST and the live set read after,
	// so a report accepted while the sweep runs is covered by rows that were
	// already committed when they were read. (The age check below is the other
	// half of that guarantee, and the one that matters: an object younger than the
	// TTL is never touched whatever the rows say.)
	objects, err := m.blobs.List(ctx, objectPrefix)
	if err != nil {
		// ⚠ Abort. An incomplete listing must never be read as "these objects do
		// not exist" — that is how a bucket is quietly emptied.
		m.logger.Error("feedback sweep: listing failed — aborting, deleted nothing", "err", err)
		m.logSweep(expiredTickets, 0, markedMissing, deleted, failures+1)
		return
	}
	live, err := m.store.LiveObjectKeys(ctx)
	if err != nil {
		m.logger.Error("feedback sweep: read live keys — aborting, deleted nothing", "err", err)
		m.logSweep(expiredTickets, 0, markedMissing, deleted, failures+1)
		return
	}
	examined := 0
	for _, o := range objects {
		examined++
		if _, ok := live[o.Key]; ok {
			continue
		}
		// ⚠ An object whose listing carries no last-modified time has an UNKNOWN
		// age, and unknown is not old: the zero time is before every cutoff, so
		// reading it literally would delete the object on the first sweep that saw
		// it. Unverifiable listing data is left alone, for the same reason a failed
		// listing deletes nothing (V3-D27).
		if o.LastModified.IsZero() {
			m.logger.Warn("feedback sweep: object has no last-modified time, leaving it", "key", o.Key)
			continue
		}
		// Never younger than the TTL: an object whose upload is still in flight has
		// no live row yet only because the browser has not finished.
		if !o.LastModified.Before(cutoff) {
			continue
		}
		if err := m.blobs.Delete(ctx, o.Key); err != nil {
			m.logger.Error("feedback sweep: delete orphan", "key", o.Key, "err", err)
			failures++
			continue
		}
		deleted++
	}
	m.logSweep(expiredTickets, examined, markedMissing, deleted, failures)
}

// logSweep emits the run summary in the shape the poller and the purge already
// use. A sweep that aborted logs why at error level and reports its deletions
// rather than falling silent.
func (m *Module) logSweep(tickets int64, examined, missing, deleted, failures int) {
	m.logger.Info("feedback sweep",
		"tickets_expired", tickets,
		"objects_examined", examined,
		"marked_missing", missing,
		"objects_deleted", deleted,
		"errors", failures,
	)
}
