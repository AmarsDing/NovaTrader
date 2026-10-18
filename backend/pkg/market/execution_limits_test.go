package market

import (
	"math"
	"testing"
	"time"

	"server/pkg/tradecal"
)

func TestExecutionLimitsRequireLoadedSecurityAndKnownListingWindow(t *testing.T) {
	day := time.Date(2026, 10, 12, 0, 0, 0, 0, tradecal.Shanghai())
	eng := NewEngine(day, EngineConfig{StaleAfter: 10 * time.Second})
	eng.Load(StockInfo{Symbol: "600519.SH", ListDate: day.AddDate(-5, 0, 0)}, nil)
	eng.Load(StockInfo{Symbol: "000002.SZ"}, nil)
	eng.Load(StockInfo{Symbol: "000003.SZ", ListDate: day.AddDate(0, 0, -1)}, nil)
	ts := day.Add(10 * time.Hour)
	for _, s := range []string{"600519.SH", "000001.SZ", "000002.SZ", "000003.SZ"} {
		eng.Process([]Snapshot{{Symbol: s, Time: ts, AsOf: ts, PreClose: 10, Open: 10, High: 10, Low: 10, Last: 10, Volume: 1000}}, ts)
	}
	bar := Bar{Symbol: "600519.SH", Time: ts, Open: 10, High: 10, Low: 10, Close: 10, Volume: 1000}
	got := eng.WithExecutionLimits(bar)
	up, down, ok := got.ExecutionPrices()
	if !ok || up != 11 || down != 9 || got.NoLimit {
		t.Fatalf("ordinary=%+v", got)
	}
	for _, s := range []string{"000001.SZ", "000002.SZ"} {
		bar.Symbol = s
		if got := eng.WithExecutionLimits(bar); got.LimitKnown {
			t.Fatalf("missing basic identity allowed: %+v", got)
		}
	}
	bar.Symbol = "000003.SZ"
	got = eng.WithExecutionLimits(bar)
	if _, _, ok := got.ExecutionPrices(); !ok || !got.NoLimit || got.LimitUp != nil || got.LimitDown != nil {
		t.Fatalf("explicit IPO no-limit=%+v", got)
	}
	bar.Time = bar.Time.AddDate(0, 0, -1)
	if got := eng.WithExecutionLimits(bar); got.LimitKnown {
		t.Fatal("wrong engine day permitted")
	}
}

func TestExecutionPricesRejectMissingContradictoryAndInvalidMetadata(t *testing.T) {
	up, down := 11., 9.
	base := Bar{LimitKnown: true, LimitUp: &up, LimitDown: &down, Open: 10, High: 10, Low: 10, Close: 10}
	for _, mutate := range []func(*Bar){
		func(b *Bar) { b.LimitKnown = false },
		func(b *Bar) { b.LimitDown = nil },
		func(b *Bar) { b.NoLimit = true },
		func(b *Bar) { b.High = 11.01 },
		func(b *Bar) { b.Low = 8.99 },
		func(b *Bar) { b.Open = math.NaN() },
	} {
		b := base
		mutate(&b)
		if _, _, ok := b.ExecutionPrices(); ok {
			t.Fatalf("invalid metadata accepted: %+v", b)
		}
	}
	base.NoLimit = true
	base.LimitUp = nil
	base.LimitDown = nil
	if _, _, ok := base.ExecutionPrices(); !ok {
		t.Fatal("known no-limit denied")
	}
}
