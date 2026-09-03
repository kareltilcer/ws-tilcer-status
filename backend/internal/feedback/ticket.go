package feedback

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/idgen"
)

// ticketTTL is how long a ticket stays usable. The widget fetches its config on
// page load, so this bounds how stale a page may be when someone finally files
// something from it.
const ticketTTL = 30 * time.Minute

// errTicketInvalid covers every ticket failure the caller reports as one 422:
// malformed, wrong signature, wrong site, too young, too old, or already spent.
// They are deliberately indistinguishable to the client — a caller learning
// which of those it hit is a caller learning how to satisfy the check.
var errTicketInvalid = errors.New("feedback: invalid ticket")

// ticket is the signed, single-use submission permit issued by the config route.
//
// It is what makes the minimum-dwell check mean anything: a dwell_ms field sent
// by the client is a number the client chooses, whereas the issue time here is
// signed by the server and cannot be moved. The id also gives the row that makes
// the ticket single-use (V3-D31).
type ticket struct {
	ID       string
	SiteID   string
	IssuedAt time.Time
}

// signTicket renders t as "payload.mac", both url-safe base64. The MAC covers the
// id, the issue time AND the site id, so a ticket minted for one site cannot be
// spent on another.
func signTicket(t ticket, secret string) string {
	payload := t.ID + "|" + strconv.FormatInt(t.IssuedAt.UTC().UnixMilli(), 10) + "|" + t.SiteID
	enc := base64.RawURLEncoding.EncodeToString([]byte(payload))
	return enc + "." + base64.RawURLEncoding.EncodeToString(ticketMAC(enc, secret))
}

// parseTicket verifies the signature and returns the ticket it carries. It does
// NOT check timing or single use — the caller does, because those need the clock
// and the database.
func parseTicket(token, secret string) (ticket, error) {
	enc, mac, ok := strings.Cut(token, ".")
	if !ok {
		return ticket{}, errTicketInvalid
	}
	got, err := base64.RawURLEncoding.DecodeString(mac)
	if err != nil || !hmac.Equal(got, ticketMAC(enc, secret)) {
		return ticket{}, errTicketInvalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		return ticket{}, errTicketInvalid
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 3 {
		return ticket{}, errTicketInvalid
	}
	ms, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return ticket{}, errTicketInvalid
	}
	return ticket{ID: parts[0], SiteID: parts[2], IssuedAt: time.UnixMilli(ms).UTC()}, nil
}

func ticketMAC(payload, secret string) []byte {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(payload))
	return m.Sum(nil)
}

// newTicket mints a ticket for siteID issued at now.
func newTicket(siteID string, now time.Time) ticket {
	return ticket{ID: idgen.New(), SiteID: siteID, IssuedAt: now.UTC()}
}

// checkTiming enforces the dwell floor and the expiry ceiling: a submission that
// arrives sooner than minDwell after the ticket was issued is a script, and one
// that arrives after ticketTTL is a page that has been open long enough that its
// configuration may no longer hold.
func (t ticket) checkTiming(now time.Time, minDwell time.Duration) error {
	age := now.UTC().Sub(t.IssuedAt)
	if age < minDwell || age > ticketTTL {
		return errTicketInvalid
	}
	return nil
}
