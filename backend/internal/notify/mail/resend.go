package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// resendEndpoint is Resend's send API. status talks to it with the standard
// library alone, as ws-tilcer-karel does: one POST does not earn an SDK, and the
// SDK's Send ignores the caller's context.
const resendEndpoint = "https://api.resend.com/emails"

// resendTimeout bounds one HTTP exchange. The worker's own context is the outer
// bound; this one keeps a test-send handler from waiting on a stalled socket.
const resendTimeout = 10 * time.Second

// maxErrorBody caps how much of a refusal is read and kept: it ends up in a log
// line and on the deliveries list, and a provider error page is not either's
// business.
const maxErrorBody = 512

// Resend sends through Resend's HTTP API.
type Resend struct {
	apiKey string
	url    string // the endpoint; a field so a test can point it at httptest
	client *http.Client
}

// NewResend returns a Resend mailer authenticated by apiKey.
func NewResend(apiKey string) *Resend {
	return &Resend{apiKey: apiKey, url: resendEndpoint, client: &http.Client{Timeout: resendTimeout}}
}

// Provider implements Mailer.
func (r *Resend) Provider() string { return "resend" }

type resendRequest struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	Text    string   `json:"text"`
	HTML    string   `json:"html,omitempty"`
}

type resendResponse struct {
	ID string `json:"id"`
}

type resendError struct {
	Name    string `json:"name"`
	Message string `json:"message"`
}

// Send implements Mailer.
//
// ⚠ The API key travels in a header and never in an error: a refusal is built
// from the response alone, and a transport error from net/http names the URL,
// not the headers. The deliveries list shows last_error to the dashboard.
func (r *Resend) Send(ctx context.Context, m Message) (Result, error) {
	body, err := json.Marshal(resendRequest{From: m.From, To: m.To, Subject: m.Subject, Text: m.Text, HTML: m.HTML})
	if err != nil {
		return Result{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.url, bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Authorization", "Bearer "+r.apiKey)
	req.Header.Set("Content-Type", "application/json")
	if m.IdempotencyKey != "" {
		req.Header.Set("Idempotency-Key", m.IdempotencyKey)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		var out resendResponse
		if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&out); err != nil {
			// The provider accepted the message; an unreadable body does not undo
			// that, and reporting it as a failure would schedule a retry that the
			// idempotency key then answers with the same acceptance anyway.
			return Result{}, nil
		}
		return Result{ID: out.ID}, nil
	}

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	se := &SendError{Status: resp.StatusCode, RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"))}
	var parsed resendError
	if json.Unmarshal(raw, &parsed) == nil && (parsed.Name != "" || parsed.Message != "") {
		se.Code, se.Detail = parsed.Name, parsed.Message
	} else {
		se.Detail = strings.TrimSpace(string(raw))
	}
	if se.Detail == "" {
		se.Detail = http.StatusText(resp.StatusCode)
	}
	return Result{}, se
}

// parseRetryAfter reads a delay-seconds Retry-After. The HTTP-date form is not
// something Resend sends; it reads as absent rather than as an error.
func parseRetryAfter(v string) time.Duration {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n <= 0 {
		return 0
	}
	return time.Duration(n) * time.Second
}

// String keeps the key out of any %v a caller might print.
func (r *Resend) String() string { return fmt.Sprintf("resend(%s)", r.url) }
