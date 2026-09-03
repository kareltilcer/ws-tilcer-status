package sites

import (
	"context"
	"database/sql"
)

// The feedback module reaches into the board and into the site cascade, and
// `sites` may not import it — a registry that imports a feature module is not a
// registry. So the dependency is inverted here: `sites` declares what it needs,
// `feedback` implements it, and composition (cmd/status) injects it.
//
// ⚠ Never a package-level global. home's §V5-12 correction, applied here from the
// start: a global makes the wiring invisible, order-dependent, and impossible to
// vary per test.

// ReportCounter supplies the board's unread badge: the number of reports in state
// `new` per site.
//
// ⚠ With no counter registered the field is null, NOT zero (V3-D53), and the card
// renders no badge at all. Zero is a claim that there is nothing to read; null is
// the truth, which is that nobody asked.
type ReportCounter interface {
	ReportCounts(ctx context.Context, siteIDs []string) (map[string]int, error)
}

// ObjectPurger is the object-storage half of a site delete. `ON DELETE CASCADE`
// removes rows; it cannot remove objects from R2.
//
// ⚠ The two halves are separate on purpose (V3-D05). SiteObjectKeys runs INSIDE
// the deleting transaction, so the set is exactly what the cascade is about to
// orphan; DeleteObjects runs AFTER it commits, because the reverse order destroys
// the attachments of a site that still exists if the commit fails. DeleteObjects
// is also a network call, and the pool is one connection.
type ObjectPurger interface {
	SiteObjectKeys(ctx context.Context, tx *sql.Tx, siteID string) ([]string, error)
	DeleteObjects(ctx context.Context, keys []string)
}

// ObjectCollector is the in-transaction half of ObjectPurger, in the shape
// Store.Delete takes it.
type ObjectCollector func(ctx context.Context, tx *sql.Tx, siteID string) ([]string, error)

// SetReportCounter registers the board's report counter. It must be called at
// composition, before the server starts serving.
func (m *Module) SetReportCounter(c ReportCounter) { m.store.reports = c }

// SetObjectPurger registers the object-storage half of the site cascade. It must
// be called at composition, before the server starts serving.
func (m *Module) SetObjectPurger(p ObjectPurger) { m.objects = p }

// openReports returns the per-site `new` report counts, or nil when no counter is
// registered — which is what makes open_reports null rather than 0.
func (s *Store) openReports(ctx context.Context, ids []string) map[string]int {
	if s.reports == nil || len(ids) == 0 {
		return nil
	}
	counts, err := s.reports.ReportCounts(ctx, ids)
	if err != nil {
		// The board is a monitoring surface: a failed badge count must not fail the
		// board. Absent counts render as no badge, which is the same as an
		// unregistered counter.
		return nil
	}
	return counts
}
