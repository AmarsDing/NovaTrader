package market

import (
	"testing"
	"time"
)

func TestSnapshotSourceTimeCannotBeRefreshedByWriteTime(t *testing.T) {
	now := at("10:00:00")
	tests := []struct {
		name string
		snap Snapshot
		want bool
	}{
		{"fresh", Snapshot{Time: now.Add(-3 * time.Second), AsOf: now}, false},
		{"fresh write old source", Snapshot{Time: now.Add(-20 * time.Second), AsOf: now}, true},
		{"fresh source old write", Snapshot{Time: now, AsOf: now.Add(-20 * time.Second)}, true},
		{"missing source", Snapshot{AsOf: now}, true},
		{"legacy missing write", Snapshot{Time: now}, false},
		{"legacy old source", Snapshot{Time: now.Add(-20 * time.Second)}, true},
		{"upstream stale", Snapshot{Time: now, AsOf: now, Stale: true}, true},
		{"future source", Snapshot{Time: now.Add(2 * time.Minute), AsOf: now}, true},
		{"no trades fresh quote", Snapshot{Time: now, AsOf: now, Volume: 0}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.snap.Expired(now, 10*time.Second); got != tt.want {
				t.Fatalf("Expired=%v want %v: %+v", got, tt.want, tt.snap)
			}
		})
	}
}

func TestEngineIgnoresOldSourceWithFreshWriteTime(t *testing.T) {
	now := at("10:00:00")
	e := NewEngine(engineDay(), EngineConfig{StaleAfter: 10 * time.Second})
	e.Load(StockInfo{Symbol: "000001.SZ", Adj: 1}, nil)
	s := Snapshot{Symbol: "000001.SZ", Time: now.Add(-time.Minute), AsOf: now, Last: 11, PreClose: 10, High: 11, Low: 10, Volume: 100}
	up := e.Process([]Snapshot{s}, now)
	if up.Expired != 1 || len(up.Changed) != 0 || len(up.Alerts) != 0 || len(up.Bars) != 0 {
		t.Fatalf("old quote was processed: %+v", up)
	}
}
