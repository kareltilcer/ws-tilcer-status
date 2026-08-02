// Package statusreport is a tiny, dependency-free crash reporter for the
// status.tilcer.cz ingest API. It is designed to fail safe: Report never blocks
// the caller and never panics, and an ingest error is silently dropped — a
// monitoring client must never take down the app it monitors.
//
// Quick start:
//
//	sr, _ := statusreport.NewFromEnv()   // reads STATUS_INGEST_URL + STATUS_INGEST_KEY
//	defer sr.Recover()                   // report panics as fatal, then re-panic
//	...
//	if err != nil {
//	    sr.Report(err, statusreport.WithContext(map[string]any{"route": "/api/summary"}))
//	}
package statusreport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"runtime/debug"
	"time"
)

// Client posts crash events to a single site's ingest endpoint.
type Client struct {
	url         string // full ingest URL, e.g. https://status.tilcer.cz/api/ingest/fin
	key         string // per-site ingest key (X-Ingest-Key)
	environment string
	release     string
	http        *http.Client
}

// Option configures a Client.
type Option func(*Client)

// WithEnvironment sets the environment tag sent on every event (e.g. "prod").
func WithEnvironment(env string) Option { return func(c *Client) { c.environment = env } }

// WithRelease sets the release tag sent on every event (e.g. "fin@2026.31.2").
func WithRelease(rel string) Option { return func(c *Client) { c.release = rel } }

// WithHTTPClient overrides the HTTP client (default: 5s timeout).
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// New builds a client for a fully-qualified ingest URL and per-site key.
func New(url, key string, opts ...Option) *Client {
	c := &Client{url: url, key: key, http: &http.Client{Timeout: 5 * time.Second}}
	for _, o := range opts {
		o(c)
	}
	return c
}

// NewFromEnv builds a client from STATUS_INGEST_URL and STATUS_INGEST_KEY.
// STATUS_ENVIRONMENT and STATUS_RELEASE, when set, become default tags. It
// returns an error only when the required variables are missing.
func NewFromEnv(opts ...Option) (*Client, error) {
	url := os.Getenv("STATUS_INGEST_URL")
	key := os.Getenv("STATUS_INGEST_KEY")
	if url == "" || key == "" {
		return nil, fmt.Errorf("statusreport: STATUS_INGEST_URL and STATUS_INGEST_KEY must be set")
	}
	base := []Option{WithEnvironment(os.Getenv("STATUS_ENVIRONMENT")), WithRelease(os.Getenv("STATUS_RELEASE"))}
	return New(url, key, append(base, opts...)...), nil
}

// event is the ingest payload (matches openapi CrashReport).
type event struct {
	Message     string         `json:"message"`
	Level       string         `json:"level,omitempty"`
	Stack       string         `json:"stack,omitempty"`
	Environment string         `json:"environment,omitempty"`
	Release     string         `json:"release,omitempty"`
	Fingerprint string         `json:"fingerprint,omitempty"`
	Context     map[string]any `json:"context,omitempty"`
	OccurredAt  string         `json:"occurred_at,omitempty"`
}

// EventOption customizes a single reported event.
type EventOption func(*event)

// WithLevel overrides the level (fatal|error|warning). Default: error.
func WithLevel(level string) EventOption { return func(e *event) { e.Level = level } }

// WithStack attaches a stack trace.
func WithStack(stack string) EventOption { return func(e *event) { e.Stack = stack } }

// WithFingerprint overrides server-side grouping.
func WithFingerprint(fp string) EventOption { return func(e *event) { e.Fingerprint = fp } }

// WithContext attaches free-form context (tags, request info). Size-capped by the
// server (default 64 KB total payload).
func WithContext(ctx map[string]any) EventOption { return func(e *event) { e.Context = ctx } }

// Report sends err as a crash event. It is fire-and-forget: it returns
// immediately and never blocks or panics. A nil Client or nil err is a no-op.
func (c *Client) Report(err error, opts ...EventOption) {
	if c == nil || err == nil {
		return
	}
	c.Capture(err.Error(), opts...)
}

// Capture sends a message as a crash event. Fire-and-forget.
func (c *Client) Capture(message string, opts ...EventOption) {
	if c == nil || message == "" {
		return
	}
	e := &event{Message: message, Environment: c.environment, Release: c.release}
	for _, o := range opts {
		o(e)
	}
	go c.send(e) // never block the caller
}

// send posts one event, dropping any error (fail safe).
func (c *Client) send(e *event) {
	defer func() { _ = recover() }() // never let the reporter itself crash the app
	body, err := json.Marshal(e)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Ingest-Key", c.key)
	resp, err := c.http.Do(req)
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}

// Recover is a deferred panic handler: `defer sr.Recover()`. It reports the panic
// as a fatal event SYNCHRONOUSLY (so the report goes out before the process may
// exit), then re-panics to preserve normal crash semantics. Use it in main or at
// the top of a request handler.
func (c *Client) Recover() {
	r := recover()
	if r == nil {
		return
	}
	if c != nil {
		e := &event{
			Message:     fmt.Sprintf("panic: %v", r),
			Level:       "fatal",
			Stack:       string(debug.Stack()),
			Environment: c.environment,
			Release:     c.release,
		}
		c.send(e) // synchronous, best-effort
	}
	panic(r) // preserve the original crash
}

// RecoverAndContinue is like Recover but swallows the panic instead of
// re-panicking — for a long-lived background goroutine you want to keep alive.
func (c *Client) RecoverAndContinue() {
	r := recover()
	if r == nil {
		return
	}
	if c != nil {
		c.send(&event{
			Message:     fmt.Sprintf("panic: %v", r),
			Level:       "fatal",
			Stack:       string(debug.Stack()),
			Environment: c.environment,
			Release:     c.release,
		})
	}
}
