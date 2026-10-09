//go:build live

// 閼辨梻缍夌€圭偞绁撮崥鍕樆闁劍绨敍姝 test -tags live -run Live -v ./app/datahub/internal/data
package data

import (
	"context"
	"fmt"
	"testing"
	"time"

	"server/app/datahub/internal/biz"
	"server/pkg/tradecal"
)

func liveDay(t *testing.T) time.Time {
	// 2026-09-30 娑撻缚濡崜宥嗘付閸氬簼绔存稉顏冩唉閺勬挻妫╅敍宀€娲忛崥搴㈡殶閹诡噣缍堥崗銊ｂ偓?
	return time.Date(2026, 9, 30, 0, 0, 0, 0, tradecal.Shanghai())
}

func ctx30() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 60*time.Second)
}

func TestLiveEastmoney(t *testing.T) {
	e := NewEastmoney(HTTPOptions{})
	ctx, cancel := ctx30()
	defer cancel()
	d := liveDay(t)

	start := time.Now()
	snap, err := e.Snapshots(ctx, nil)
	if err != nil {
		t.Fatalf("snapshots: %v", err)
	}
	t.Logf("snapshots %d in %v; first %+v", len(snap.Items), time.Since(start), head(snap.Items))

	sec, err := e.Securities(ctx)
	t.Logf("securities %d err=%v first=%+v", len(sec), err, head(sec))
	flows, err := e.IntradayFlows(ctx)
	t.Logf("flows %d err=%v first=%+v", len(flows), err, head(flows))
	sq, err := e.SectorQuotes(ctx)
	t.Logf("sector quotes %d err=%v first=%+v", len(sq), err, head(sq))
	lp, err := e.LimitPool(ctx, d)
	t.Logf("limit pool %d err=%v", len(lp), err)
	for _, p := range []string{biz.PoolUp, biz.PoolDown, biz.PoolBroken} {
		for _, x := range lp {
			if x.Pool == p {
				t.Logf("  %s %+v close=%v first=%v", p, x, deref(x.Close), x.FirstSealAt)
				break
			}
		}
	}
	lhb, err := e.LhbSeats(ctx, d)
	t.Logf("lhb %d err=%v first=%+v", len(lhb), err, head(lhb))
	mg, err := e.Margins(ctx, d)
	t.Logf("margin %d err=%v first=%+v", len(mg), err, head(mg))
	hs, err := e.HsgtTop10(ctx, d)
	t.Logf("hsgt %d err=%v first=%+v", len(hs), err, head(hs))
	fin, err := e.Finance(ctx, d.AddDate(0, 0, -7))
	t.Logf("finance %d err=%v", len(fin), err)
	for _, k := range []string{biz.FinanceForecast, biz.FinanceExpress} {
		for _, f := range fin {
			if f.Kind == k {
				t.Logf("  %s %s %s np=%v yoy=%v type=%s", k, f.Symbol, ymd(f.EndDate), deref(f.NetProfit), deref(f.NetProfitYoY), f.ForecastType)
				break
			}
		}
	}
	hot, err := e.HotRank(ctx)
	t.Logf("hot %d err=%v first=%+v", len(hot), err, head(hot))
	for _, k := range []string{biz.KindFlash, biz.KindAnnouncement, biz.KindReport} {
		it, err := e.Intel(ctx, k, biz.IntelQuery{Since: time.Now().Add(-6 * time.Hour), Limit: 50})
		t.Logf("intel %s %d err=%v first=%+v", k, len(it), err, head(it))
	}
}

func TestLiveEastmoneySectors(t *testing.T) {
	e := NewEastmoney(HTTPOptions{})
	ctx, cancel := ctx30()
	defer cancel()
	start := time.Now()
	s, err := e.Sectors(ctx)
	n := 0
	for _, x := range s {
		n += len(x.Members)
	}
	t.Logf("sectors %d members %d in %v err=%v", len(s), n, time.Since(start), err)
}

func TestLiveCninfo(t *testing.T) {
	ctx, cancel := ctx30()
	defer cancel()
	c := NewCninfo(HTTPOptions{})
	start := time.Now()
	items, err := c.Intel(ctx, biz.KindAnnouncement, biz.IntelQuery{Since: time.Now().Add(-8 * time.Hour)})
	by := map[string]int{}
	for _, it := range items {
		if len(it.Codes) > 0 {
			by[it.Codes[0][7:]]++
		}
	}
	t.Logf("by market %v", by)
	t.Logf("cninfo %d in %v err=%v first=%+v", len(items), time.Since(start), err, head(items))
}

func fakeUniverse(ctx context.Context) ([]string, error) {
	var out []string
	for _, r := range []struct {
		from, to int
		mkt      string
	}{{600000, 605999, "SH"}, {688000, 688999, "SH"}, {1, 3999, "SZ"}, {300001, 301999, "SZ"}} {
		for c := r.from; c <= r.to; c++ {
			out = append(out, fmt.Sprintf("%06d.%s", c, r.mkt))
		}
	}
	return out, nil
}

func TestLiveWebQuotes(t *testing.T) {
	for _, s := range []biz.SnapshotSource{NewSina(HTTPOptions{}, fakeUniverse), NewTencent(HTTPOptions{}, fakeUniverse)} {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		start := time.Now()
		b, err := s.Snapshots(ctx, nil)
		cancel()
		t.Logf("%s: %d in %v err=%v first=%+v", s.(biz.Source).Name(), len(b.Items), time.Since(start), err, head(b.Items))
	}
}

func head[T any](s []T) any {
	if len(s) == 0 {
		return nil
	}
	return s[0]
}
