package notify

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/kareltilcer/ws-tilcer-status/backend/internal/crash"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/feedback"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/monitoring"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/sites"
)

// Notifier implements crash.Notifier, feedback.Notifier and monitoring.Notifier.
// It runs inside the producer's transaction, reads and writes only through it,
// and never touches the network: all it does is decide, and queue.
//
// ⚠ It is wired even on a deployment with no mail provider. The crash and
// downtime state machines must keep running regardless — otherwise switching a
// provider on later would find every old group still "armed" and mail its
// history as news. Only the queueing is gated on a provider.
type Notifier struct {
	available bool
	logger    *slog.Logger
}

var (
	_ crash.Notifier      = (*Notifier)(nil)
	_ feedback.Notifier   = (*Notifier)(nil)
	_ monitoring.Notifier = (*Notifier)(nil)
)

// savepoint names the nested scope every hook runs in.
const savepoint = "notify_enqueue"

// guard runs fn inside a SAVEPOINT on tx, so a failed or panicking hook undoes
// its own writes and nothing else: the crash, the report or the check the
// producer wrote before calling is always committed.
//
// ⚠ Its error is returned ONLY when the transaction itself is no longer usable —
// the savepoint could not be opened, rolled back to, or released. That happens
// when SQLite has already rolled the whole transaction back (SQLITE_FULL, an I/O
// error), and then the producer's commit would have failed anyway. Every other
// failure is logged and swallowed; in particular a notify error can never reach
// feedback's insert loop, which reads any UNIQUE failure as a ref collision.
func (n *Notifier) guard(ctx context.Context, tx *sql.Tx, what string, fn func() error) (err error) {
	if _, err := tx.ExecContext(ctx, "SAVEPOINT "+savepoint); err != nil {
		return fmt.Errorf("notify: open savepoint: %w", err)
	}
	undo := func(cause any) error {
		n.logger.Error("notify: could not queue a notification; the triggering write is kept",
			"what", what, "err", cause)
		if _, err := tx.ExecContext(ctx, "ROLLBACK TO "+savepoint); err != nil {
			return fmt.Errorf("notify: roll back savepoint: %w", err)
		}
		return nil
	}
	defer func() {
		if p := recover(); p != nil {
			if err = undo(p); err == nil {
				err = release(ctx, tx)
			}
		}
	}()
	if ferr := fn(); ferr != nil {
		if err := undo(ferr); err != nil {
			return err
		}
	}
	return release(ctx, tx)
}

// release pops the savepoint. After a ROLLBACK TO it is still on the stack, so
// the failure path releases it too.
func release(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, "RELEASE "+savepoint); err != nil {
		return fmt.Errorf("notify: release savepoint: %w", err)
	}
	return nil
}

// deliverable reports whether an event of kind about siteID would be mailed now:
// a provider exists, the master switch is on, there is somebody to mail, the
// kind's toggle is on, and the site is not muted.
func (n *Notifier) deliverable(ctx context.Context, q querier, kind, siteID string) (bool, error) {
	if !n.available {
		return false, nil
	}
	st, err := loadSettings(ctx, q)
	if err != nil {
		return false, err
	}
	if !st.deliverable(kind, false) {
		return false, nil
	}
	muted, err := isMuted(ctx, q, siteID)
	if err != nil {
		return false, err
	}
	return !muted, nil
}

// CrashRecorded implements crash.Notifier.
//
// A group is due an email while it is ARMED: from its creation until its first
// qualifying event (error or fatal, in production), and again from a regression
// reopen until the next one. The first announcement says "new crash", any later
// one "crash came back". So a group first seen in a developer's browser is still
// news the first time it hits production, the 500th repeat is not, and a manual
// reopen through triage — which never comes through here — is Karel's own doing.
// Only an OPEN group's announcement is mailed: one Karel has ignored (or
// resolved, with regressions not reopening it) consumes it silently.
//
// The state machine runs whether or not anything is mailed: an event that
// happens while notifications are off consumes the announcement, so switching
// them on later does not deliver the backlog.
func (n *Notifier) CrashRecorded(ctx context.Context, tx *sql.Tx, s crash.Signal) error {
	q := qualifies(s.Level, s.Environment)
	if !q && !s.Reopened {
		// The common case — a warning, a dev build, a repeat below error — costs
		// nothing: no savepoint, no query.
		return nil
	}
	return n.guard(ctx, tx, "crash", func() error {
		armed, announced, err := loadCrashState(ctx, tx, s.GroupID)
		if err != nil {
			return err
		}
		rearmed := s.Reopened && !armed
		if s.Reopened {
			armed = true
		}
		if !q || !armed {
			if rearmed {
				return saveCrashState(ctx, tx, s.GroupID, true, announced)
			}
			return nil
		}
		kind := KindCrashNew
		if announced {
			kind = KindCrashRegression
		}
		if err := saveCrashState(ctx, tx, s.GroupID, false, true); err != nil {
			return err
		}
		// ⚠ A group that is not open — ignored, or resolved while regressions do
		// not reopen it — is one Karel has already triaged. Its first qualifying
		// event consumes the announcement all the same, exactly as the upgrade seed
		// does for every group that has had one; left armed, setting it back to
		// open would mail its next error as a "new crash".
		if s.GroupStatus != crash.StatusOpen {
			return nil
		}
		ok, err := n.deliverable(ctx, tx, kind, s.SiteID)
		if err != nil || !ok {
			return err
		}
		return insertEvent(ctx, tx, s.SiteID, kind, strconv.FormatInt(s.GroupID, 10), payload{
			GroupID:     s.GroupID,
			Title:       excerpt(s.Title, 200),
			Level:       s.Level,
			Environment: strings.TrimSpace(s.Environment),
			Release:     excerpt(s.Release, 100),
			Message:     excerpt(s.Message, excerptRunes),
			At:          ts(s.At),
		}, ts(s.At))
	})
}

// ReportSubmitted implements feedback.Notifier. Every accepted report is news;
// there is no state to keep.
func (n *Notifier) ReportSubmitted(ctx context.Context, tx *sql.Tx, s feedback.ReportSignal) error {
	if !n.available {
		return nil
	}
	return n.guard(ctx, tx, "feedback", func() error {
		ok, err := n.deliverable(ctx, tx, KindFeedback, s.SiteID)
		if err != nil || !ok {
			return err
		}
		return insertEvent(ctx, tx, s.SiteID, KindFeedback, s.Ref, payload{
			Ref:         s.Ref,
			ReportKind:  s.Kind,
			Attachments: s.Attachments,
			Message:     excerpt(s.Message, excerptRunes),
			At:          ts(s.At),
		}, ts(s.At))
	})
}

// CheckRecorded implements monitoring.Notifier.
//
// A site is DOWN from the check that turns it red until the next SUCCESSFUL
// check. Recovery is read off a passing check, not off the color leaving red:
// editing a red site's URL resets its color to unknown without anything having
// recovered, and a "back up" email for that would be a lie.
//
// "Back up" is sent only for an outage whose "down" was queued — an outage that
// began while notifications were off, or muted, stays silent at both ends.
func (n *Notifier) CheckRecorded(ctx context.Context, tx *sql.Tx, c monitoring.CheckSignal) error {
	return n.guard(ctx, tx, "downtime", func() error {
		st, down, err := loadSiteState(ctx, tx, c.SiteID)
		if err != nil {
			return err
		}
		switch {
		case !down && c.Color == sites.Red:
			ok, err := n.deliverable(ctx, tx, KindSiteDown, c.SiteID)
			if err != nil {
				return err
			}
			if err := saveSiteDown(ctx, tx, c.SiteID, ts(c.At), ok); err != nil {
				return err
			}
			if !ok {
				return nil
			}
			return insertEvent(ctx, tx, c.SiteID, KindSiteDown, "", payload{
				URL:        c.URL,
				StatusCode: c.StatusCode,
				Error:      excerpt(c.Error, 200),
				DownSince:  ts(c.At),
				At:         ts(c.At),
			}, ts(c.At))
		case down && c.OK:
			if err := clearSiteDown(ctx, tx, c.SiteID); err != nil {
				return err
			}
			if !st.Announced {
				return nil
			}
			ok, err := n.deliverable(ctx, tx, KindSiteRecovered, c.SiteID)
			if err != nil || !ok {
				return err
			}
			return insertEvent(ctx, tx, c.SiteID, KindSiteRecovered, "", payload{
				URL:       c.URL,
				DownSince: st.DownSince,
				At:        ts(c.At),
			}, ts(c.At))
		}
		return nil
	})
}
