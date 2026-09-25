// Package txprobe asserts, structurally rather than by review, that a network
// call never runs while the service's single database connection is held.
//
// ⚠ It works because appdb.Open caps the pool at ONE connection. A call site that
// runs inside a transaction, or with an outer rows cursor still open, holds that
// connection — so the probe's own `SELECT 1` has nowhere to run, blocks in the
// database/sql pool, times out, and records a violation. The block happens at the
// pool level, not in SQLite's locking, which is why an open cursor is caught as
// well as a transaction.
//
// It is imported only by test fakes (blobtest, mailtest) so the two object-storage
// and mail assertions share ONE detector: two copies of a probe are two chances
// for one of them to stop detecting anything.
package txprobe

import (
	"context"
	"database/sql"
	"sync"
	"time"
)

// Timeout bounds how long the probe waits for the single connection before
// calling it held. Only a violating call ever waits this long.
const Timeout = 500 * time.Millisecond

// Probe records the calls that ran while the connection was held. The zero value
// is usable and probes nothing until Set is called.
type Probe struct {
	mu         sync.Mutex
	db         *sql.DB
	violations []string
}

// Set arms the probe against db (nil disarms it). It is a setter under a lock
// because the fakes that embed a probe are called from detached goroutines.
func (p *Probe) Set(db *sql.DB) { p.mu.Lock(); p.db = db; p.mu.Unlock() }

// Check queries the armed database and records name as a violation when the
// connection could not be had within Timeout.
//
// ⚠ It runs WITHOUT the probe's own lock held: the point is to observe the
// database connection, not to serialize the caller.
func (p *Probe) Check(ctx context.Context, name string) {
	p.mu.Lock()
	db := p.db
	p.mu.Unlock()
	if db == nil {
		return
	}
	pctx, cancel := context.WithTimeout(ctx, Timeout)
	err := db.QueryRowContext(pctx, "SELECT 1").Scan(new(int))
	cancel()
	if err != nil {
		p.mu.Lock()
		p.violations = append(p.violations, name)
		p.mu.Unlock()
	}
}

// Violations returns the calls recorded while the connection was held, copied
// under the lock.
func (p *Probe) Violations() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.violations...)
}
