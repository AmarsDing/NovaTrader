package biz

import (
	"testing"
	"time"

	"server/pkg/market"
)

func TestCheckSnapshotsStale(t *testing.T) {
	now := time.Date(2026, 10, 8, 10, 0, 3, 0, shanghai())
	items := []market.Snapshot{
		{Symbol: "600519.SH", Time: now.Add(-20 * time.Second), Last: 100, High: 101, Low: 99, Volume: 1},
	}
	_, _, err := CheckSnapshots(items, SnapshotRules{Now: now, StaleAfter: 10 * time.Second, Trading: true, MinCount: 1})
	var se *StaleError
	if err == nil || !isStale(err, &se) {
		t.Fatalf("want stale, got %v", err)
	}
}

func isStale(err error, se **StaleError) bool {
	e, ok := err.(*StaleError)
	if ok {
		*se = e
	}
	return ok
}

func TestCheckSnapshotsDropBad(t *testing.T) {
	now := time.Date(2026, 10, 8, 10, 0, 0, 0, shanghai())
	items := []market.Snapshot{
		{Symbol: "bad", Time: now, Last: 1},
		{Symbol: "600519.SH", Time: now, Last: 100, High: 90, Low: 110, Volume: 1},
		{Symbol: "000001.SZ", Time: now, Last: 10, High: 11, Low: 9, Volume: 100},
	}
	out, issues, err := CheckSnapshots(items, SnapshotRules{Now: now, Trading: true, StaleAfter: time.Minute, MinCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Symbol != "000001.SZ" {
		t.Fatalf("out=%+v issues=%v", out, issues)
	}
}

func TestCheckBarsAndCompleteness(t *testing.T) {
	d := time.Date(2026, 10, 8, 0, 0, 0, 0, shanghai())
	pc := 10.0
	bars := []Bar{
		{Symbol: "600519.SH", Time: d, Freq: "1d", Open: 10, High: 11, Low: 9, Close: 10.5, Volume: 1, Amount: 1, PreClose: &pc},
		{Symbol: "000001.SZ", Time: d, Freq: "1d", Open: 0, High: 1, Low: 1, Close: 1, Volume: 1},
	}
	clean, issues := CheckBars(DomainDailyBar, bars, BarQuery{Start: d, End: d}, 0.31)
	if len(clean) != 1 || len(issues) == 0 {
		t.Fatalf("clean=%d issues=%d", len(clean), len(issues))
	}
	cov, missing := Completeness([]string{"600519.SH"}, []string{"600519.SH", "000001.SZ"})
	if cov < 0.49 || cov > 0.51 || len(missing) != 1 {
		t.Fatalf("cov=%v missing=%v", cov, missing)
	}
}

func TestPickSampleStable(t *testing.T) {
	stored := map[string]Bar{"a": {}, "b": {}, "c": {}, "d": {}}
	s1 := pickSample(stored, 2)
	s2 := pickSample(stored, 2)
	if len(s1) != 2 || s1[0] != s2[0] || s1[1] != s2[1] {
		t.Fatalf("%v vs %v", s1, s2)
	}
}
