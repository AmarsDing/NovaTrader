package biz

import (
	"context"
	"testing"
	"time"

	"server/pkg/risk"
)

func TestFreshWriteTimeDoesNotAllowOldOrUnknownQuote(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "old source", true: "missing source"}[missing], func(t *testing.T) {
			r := newRig(t, nil)
			r.account(1_000_000, 500_000)
			s := r.snap(10)
			s.Time = r.clock.Add(-time.Minute)
			if missing {
				s.Time = time.Time{}
			}
			r.q.m[s.Symbol] = s
			d, _ := r.e.Check(context.Background(), buy("stale", 100))
			if d.Approved || d.Rule != "R02" {
				t.Fatalf("old quote approved: %+v", d)
			}
		})
	}
}

func TestOldQuoteDoesNotRefreshHoldingHeartbeatOrEmitExit(t *testing.T) {
	r := newRig(t, nil)
	r.account(1_000_000, 500_000, risk.Holding{Symbol: "000001.SZ", Quantity: 1000, Available: 1000, AvgCost: 10, StopPrice: 9.5})
	s := r.snap(9)
	s.Time = r.clock.Add(-time.Minute)
	r.q.m[s.Symbol] = s
	r.e.OnSnapshot(context.Background(), SnapshotMetaPayload{})
	if len(r.pub.msgs) != 0 {
		t.Fatalf("stale data emitted exit: %+v", r.pub.msgs)
	}
	if got := r.e.State().LastQuoteAt; got.After(s.Time) {
		t.Fatalf("fresh write preserved as heartbeat: %v", got)
	}
	if high := r.e.highs["SIM|000001.SZ"]; high != 0 {
		t.Fatalf("stale data changed trailing high: %v", high)
	}
}
