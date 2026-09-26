package notify

import (
	"strings"
	"time"

	"github.com/kareltilcer/ws-tilcer-status/backend/internal/crash"
)

// qualifies reports whether a crash event is worth an email: an error or worse,
// in production.
//
// "Production" is the fleet's `prod` convention (docs/integration.md), its long
// spelling, or no environment at all — a client that never set one is most
// likely the deployed app, and missing a real outage is the worse mistake. A
// warning, or anything from dev or staging, still colours the board; it just does
// not interrupt anyone.
func qualifies(level, environment string) bool {
	if level != crash.LevelError && level != crash.LevelFatal {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(environment)) {
	case "", "prod", "production":
		return true
	}
	return false
}

// backoffSteps is the retry schedule after a failed send, indexed by attempts so
// far. The last step repeats.
var backoffSteps = []time.Duration{
	time.Minute, 2 * time.Minute, 5 * time.Minute, 15 * time.Minute, 30 * time.Minute, time.Hour,
}

// maxRetryWait is the longest a digest ever waits for its next attempt — the
// last backoff step, and the ceiling on a provider's Retry-After too.
var maxRetryWait = backoffSteps[len(backoffSteps)-1]

// backoff returns how long to wait after the given number of failed attempts.
func backoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	if attempts > len(backoffSteps) {
		return maxRetryWait
	}
	return backoffSteps[attempts-1]
}

// giveUpAfter is how long a digest may keep being retried. ⚠ It is inside
// Resend's 24-hour idempotency window on purpose: every retry reuses the digest's
// key, and a retry after the window closed could deliver a message that an
// earlier, timed-out attempt had in fact already delivered.
const giveUpAfter = 23 * time.Hour

// sendTimeout bounds one send, whether from the worker or the test button, for
// any mailer. The Resend client's own bound (10 s) is shorter and fires first;
// this one is the ceiling notify guarantees whatever the provider does.
const sendTimeout = 15 * time.Second

// maxEventsPerDigest bounds one digest's assembly; anything beyond it waits for
// the next digest. maxDueDigests bounds one delivery pass — a safety bound only,
// since no digest is assembled while another is pending.
const (
	maxEventsPerDigest = 1000
	maxDueDigests      = 10
)

// excerptRunes is how much of a crash message or a report the email quotes.
const excerptRunes = 300

// wants maps an event kind to its toggle.
func (s settings) wants(kind string) bool {
	switch kind {
	case KindCrashNew, KindCrashRegression:
		return s.OnCrash
	case KindFeedback:
		return s.OnFeedback
	case KindSiteDown, KindSiteRecovered:
		return s.OnDowntime
	}
	return false
}

// deliverable reports whether settings would email an event of kind about a site
// that is (or is not) muted.
func (s settings) deliverable(kind string, muted bool) bool {
	return s.Enabled && len(s.Recipients) > 0 && s.wants(kind) && !muted
}
