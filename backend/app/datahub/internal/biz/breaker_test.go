package biz

import (
	"testing"
	"time"
)

func TestBreakerTripAndProbe(t *testing.T) {
	b := NewBreaker(3, time.Minute)
	now := time.Now()
	if !b.Allow(now) {
		t.Fatal("closed should allow")
	}
	for i := 0; i < 2; i++ {
		if tr, changed := b.Record(false, now); changed {
			t.Fatalf("should not open yet: %+v", tr)
		}
	}
	tr, changed := b.Record(false, now)
	if !changed || tr.To != StateOpen {
		t.Fatalf("want open, got %+v changed=%v", tr, changed)
	}
	if b.Allow(now) {
		t.Fatal("open should block")
	}
	if !b.Allow(now.Add(time.Minute)) {
		t.Fatal("cooldown should allow one probe")
	}
	if b.Allow(now.Add(time.Minute)) {
		t.Fatal("second probe should wait")
	}
	tr, changed = b.Record(true, now.Add(time.Minute))
	if !changed || tr.To != StateClosed {
		t.Fatalf("probe success should close: %+v", tr)
	}
}

func TestQuotaDayRollover(t *testing.T) {
	q := NewQuota(2)
	d1 := time.Date(2026, 10, 8, 10, 0, 0, 0, shanghai())
	if !q.Take(d1) || !q.Take(d1) || q.Take(d1) {
		t.Fatal("limit 2")
	}
	d2 := d1.Add(24 * time.Hour)
	if !q.Take(d2) {
		t.Fatal("next day should reset")
	}
	used, limit := q.Usage(d2)
	if used != 1 || limit != 2 {
		t.Fatalf("usage %d/%d", used, limit)
	}
}
