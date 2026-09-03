package ratelimit

import (
	"testing"
	"time"
)

// clock is a hand-wound time source, so the refill arithmetic is asserted rather
// than slept through.
type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }

func TestAllowSpendsAndRefills(t *testing.T) {
	c := &clock{t: time.Unix(1_800_000_000, 0)}
	l := New(1, 2, c.now) // one token per second, two in hand

	for i := 0; i < 2; i++ {
		if ok, _ := l.Allow("k"); !ok {
			t.Fatalf("request %d refused with a full bucket", i+1)
		}
	}
	ok, retry := l.Allow("k")
	if ok {
		t.Fatal("third request allowed with an empty bucket")
	}
	if retry <= 0 || retry > time.Second {
		t.Fatalf("Retry-After = %s, want (0, 1s] at one token per second", retry)
	}

	c.add(time.Second)
	if ok, _ := l.Allow("k"); !ok {
		t.Fatal("a second later one token must have accrued")
	}
}

// TestRefundReturnsTheToken covers the case a single bucket cannot express: a
// request that has to satisfy two limiters, where the first one charged is not
// the one that refuses.
func TestRefundReturnsTheToken(t *testing.T) {
	c := &clock{t: time.Unix(1_800_000_000, 0)}
	l := New(0.001, 1, c.now) // one token, and effectively no refill

	if ok, _ := l.Allow("k"); !ok {
		t.Fatal("the first request must be allowed")
	}
	if ok, _ := l.Allow("k"); ok {
		t.Fatal("the bucket is empty: the second request must be refused")
	}

	l.Refund("k")
	if ok, _ := l.Allow("k"); !ok {
		t.Fatal("a refunded token must be spendable again")
	}
}

// TestRefundNeverExceedsBurst keeps a refund from turning into free capacity —
// a caller that refunds more than it charged would otherwise widen the bucket.
func TestRefundNeverExceedsBurst(t *testing.T) {
	c := &clock{t: time.Unix(1_800_000_000, 0)}
	l := New(0.001, 2, c.now)

	if ok, _ := l.Allow("k"); !ok {
		t.Fatal("first request refused with a full bucket")
	}
	for i := 0; i < 5; i++ {
		l.Refund("k")
	}
	// Two is the capacity, so two is all that may come back out.
	for i := 0; i < 2; i++ {
		if ok, _ := l.Allow("k"); !ok {
			t.Fatalf("request %d refused, want the burst capacity to be available", i+1)
		}
	}
	if ok, _ := l.Allow("k"); ok {
		t.Fatal("refunds accumulated past the burst capacity")
	}
}

// TestRefundOfAnUnknownKeyIsHarmless: a bucket that was swept, or never existed,
// is already full — there is nothing to give back and nothing to panic over.
func TestRefundOfAnUnknownKeyIsHarmless(t *testing.T) {
	c := &clock{t: time.Unix(1_800_000_000, 0)}
	l := New(1, 1, c.now)

	l.Refund("never-seen")
	if ok, _ := l.Allow("never-seen"); !ok {
		t.Fatal("a fresh bucket must still start full")
	}
	if ok, _ := l.Allow("never-seen"); ok {
		t.Fatal("the refund of an unknown key must not have added capacity")
	}
}
