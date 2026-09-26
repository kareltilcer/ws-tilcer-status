// Package mailtest is an in-memory mail.Mailer for tests.
package mailtest

import (
	"context"
	"database/sql"
	"fmt"
	"sync"

	"github.com/kareltilcer/ws-tilcer-status/backend/internal/notify/mail"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/db/txprobe"
)

// Fake records every send attempt and answers from a script: queued outcomes are
// consumed in order, and an empty script accepts the message.
//
// Every field is behind mu: the worker sends from the scheduler's goroutine
// while a test reads what arrived.
type Fake struct {
	mu       sync.Mutex
	script   []outcome
	attempts []mail.Message
	sent     []mail.Message
	gate     chan struct{}
	held     chan struct{}
	ids      int

	// probe, when armed, is queried before every send: a send that runs while
	// the single database connection is held — inside a transaction, or with a
	// rows cursor open — is recorded as a violation. See txprobe.
	probe txprobe.Probe
}

type outcome struct {
	res mail.Result
	err error
}

// New returns a fake that accepts every message.
func New() *Fake { return &Fake{} }

// Provider implements mail.Mailer.
func (f *Fake) Provider() string { return "fake" }

// Fail queues err as the answer to the next unanswered send.
func (f *Fake) Fail(err error) {
	f.mu.Lock()
	f.script = append(f.script, outcome{err: err})
	f.mu.Unlock()
}

// Accept queues an acceptance with the given provider id.
func (f *Fake) Accept(id string) {
	f.mu.Lock()
	f.script = append(f.script, outcome{res: mail.Result{ID: id}})
	f.mu.Unlock()
}

// Block holds every send until release is called or the send's context ends —
// which is how a test proves a shutdown mid-send leaves the digest pending.
// waiting receives once a send is actually being held, so the test cancels
// mid-send rather than before the send began.
func (f *Fake) Block() (waiting <-chan struct{}, release func()) {
	gate := make(chan struct{})
	held := make(chan struct{}, 1)
	f.mu.Lock()
	f.gate, f.held = gate, held
	f.mu.Unlock()
	return held, func() {
		f.mu.Lock()
		f.gate, f.held = nil, nil
		f.mu.Unlock()
		close(gate)
	}
}

// SetTxProbe arms the probe against db.
func (f *Fake) SetTxProbe(db *sql.DB) { f.probe.Set(db) }

// Violations returns the sends that ran while the connection was held.
func (f *Fake) Violations() []string { return f.probe.Violations() }

// Send implements mail.Mailer.
func (f *Fake) Send(ctx context.Context, m mail.Message) (mail.Result, error) {
	f.probe.Check(ctx, "Send "+m.Subject)
	f.mu.Lock()
	gate, held := f.gate, f.held
	f.mu.Unlock()
	if gate != nil {
		select {
		case held <- struct{}{}:
		default:
		}
		select {
		case <-gate:
		case <-ctx.Done():
			return mail.Result{}, ctx.Err()
		}
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.attempts = append(f.attempts, clone(m))
	if len(f.script) > 0 {
		o := f.script[0]
		f.script = f.script[1:]
		if o.err != nil {
			return mail.Result{}, o.err
		}
		f.sent = append(f.sent, clone(m))
		return o.res, nil
	}
	f.ids++
	f.sent = append(f.sent, clone(m))
	return mail.Result{ID: fmt.Sprintf("fake-%d", f.ids)}, nil
}

// Sent returns the messages the fake accepted, in order.
func (f *Fake) Sent() []mail.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]mail.Message(nil), f.sent...)
}

// Attempts returns every send, accepted or refused, in order.
func (f *Fake) Attempts() []mail.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]mail.Message(nil), f.attempts...)
}

func clone(m mail.Message) mail.Message {
	m.To = append([]string(nil), m.To...)
	return m
}
