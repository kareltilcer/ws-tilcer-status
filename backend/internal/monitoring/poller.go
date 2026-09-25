package monitoring

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	appdb "github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/db"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/timeutil"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/sites"
)

// Poller actively checks the reachability of every monitor-enabled site.
type Poller struct {
	db               *sql.DB
	store            *Store
	client           *http.Client // follows redirects (default policy)
	noRedirectClient *http.Client // stops at the first response (3xx expected_status)
	concurrency      int
	redThreshold     int
	logger           *slog.Logger
	// notifier is told about every written check (Module.SetNotifier); nil tells
	// nobody.
	notifier Notifier
}

// NewPoller builds a poller. timeout bounds each HTTP GET. Most checks follow
// redirects (an expected_status of 200 wants the final 2xx), but a site whose
// expected_status is itself a 3xx wants the redirect response, so those go
// through noRedirectClient.
func NewPoller(db *sql.DB, store *Store, timeout time.Duration, concurrency, redThreshold int, logger *slog.Logger) *Poller {
	if concurrency < 1 {
		concurrency = 1
	}
	return &Poller{
		db:     db,
		store:  store,
		client: &http.Client{Timeout: timeout},
		noRedirectClient: &http.Client{
			Timeout:       timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		concurrency:  concurrency,
		redThreshold: redThreshold,
		logger:       logger,
	}
}

// RunOnce performs one poll cycle: fan out HTTP checks under a bounded
// semaphore, then write each result in its own transaction (HTTP is kept off the
// single DB writer connection). A failing or slow site never blocks the others.
func (p *Poller) RunOnce(ctx context.Context) {
	sitesToCheck, err := p.store.MonitoredSites(ctx)
	if err != nil {
		p.logger.Error("poller: list sites", "err", err)
		return
	}
	if len(sitesToCheck) == 0 {
		return
	}

	outcomes := make([]checkOutcome, len(sitesToCheck))
	// A fixed worker pool pulling site indices off a channel bounds BOTH the
	// concurrent HTTP work and the goroutine count at p.concurrency (a goroutine
	// per site would leave all but p.concurrency of them blocked on a semaphore,
	// unbounded in stack for a large fleet).
	workers := p.concurrency
	if workers > len(sitesToCheck) {
		workers = len(sitesToCheck)
	}
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				p.runCheck(ctx, sitesToCheck[i], &outcomes[i])
			}
		}()
	}
	for i := range sitesToCheck {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	// Write phase — sequential, one small tx per site.
	now := time.Now().UTC()
	checkedAt := timeutil.Format(now)
	var okCount, failCount int
	for i, o := range outcomes {
		if o.OK {
			okCount++
		} else {
			failCount++
		}
		if err := appdb.WithTx(ctx, p.db, func(tx *sql.Tx) error {
			if err := p.store.WriteCheck(ctx, tx, o, checkedAt); err != nil {
				return err
			}
			color, err := sites.RecomputeAndPersist(ctx, tx, o.SiteID, p.redThreshold, now)
			if err != nil || p.notifier == nil {
				return err
			}
			// ⚠ Last, for the reason crash ingest gives: the notifier runs in a
			// savepoint, and nothing of ours may follow a transaction SQLite has
			// already rolled back.
			return p.notifier.CheckRecorded(ctx, tx, CheckSignal{
				SiteID: o.SiteID, URL: sitesToCheck[i].URL, OK: o.OK, Color: color,
				StatusCode: o.StatusCode, Error: o.Err, At: now,
			})
		}); err != nil {
			p.logger.Error("poller: write check", "site", o.SiteID, "err", err)
		}
	}
	p.logger.Info("poll cycle", "sites", len(sitesToCheck), "ok", okCount, "fail", failCount)
}

// runCheck runs one site's check and stores the result in out. A panic in a check
// must not take the process down (the scheduler's runSafe wraps RunOnce, not the
// worker goroutines): recover it, record the site as failing — a panic is a check
// failure, like a timeout — and let every other site's result still be written.
// Recovering here also guarantees out carries site.ID, so the write phase never
// persists a zero-value outcome with an empty site_id.
func (p *Poller) runCheck(ctx context.Context, site monitoredSite, out *checkOutcome) {
	defer func() {
		if rec := recover(); rec != nil {
			p.logger.Error("poller: check panic", "site", site.ID, "panic", rec)
			*out = checkOutcome{SiteID: site.ID, OK: false, Err: "check panicked"}
		}
	}()
	*out = p.check(ctx, site)
}

// check performs one HTTP GET and classifies the result. Network/timeout/DNS
// failures are recorded as ok=false with the error string — they are data.
func (p *Poller) check(ctx context.Context, site monitoredSite) checkOutcome {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, site.URL, nil)
	if err != nil {
		msg := err.Error()
		return checkOutcome{SiteID: site.ID, OK: false, Err: msg}
	}
	client := p.client
	if site.ExpectedStatus >= 300 && site.ExpectedStatus < 400 {
		// The site expects the redirect itself; following it would report the
		// landing page's status (usually 200) and mark a healthy site down.
		client = p.noRedirectClient
	}
	start := time.Now()
	resp, err := client.Do(req)
	latency := int(time.Since(start).Milliseconds())
	if err != nil {
		return checkOutcome{SiteID: site.ID, OK: false, LatencyMs: &latency, Err: err.Error()}
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	code := resp.StatusCode
	return checkOutcome{
		SiteID:     site.ID,
		OK:         statusOK(code, site.ExpectedStatus),
		StatusCode: &code,
		LatencyMs:  &latency,
	}
}

// statusOK reports whether code counts as healthy: it matches expected_status, or
// (when expected_status is the default 200) is any 2xx.
func statusOK(code, expected int) bool {
	if expected == 200 {
		return code >= 200 && code < 300
	}
	return code == expected
}
