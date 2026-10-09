package market

import (
	"testing"
	"time"

	"server/pkg/tradecal"
)

func limitSnap(hhmmss string, last, high float64, bid, ask [][2]float64) Snapshot {
	return Snapshot{Symbol: "000001.SZ", Time: at(hhmmss), PreClose: 10, Open: 10, High: high, Low: 10, Last: last, Volume: 1, Bid: bid, Ask: ask}
}

func TestLimitTrackerSealBreakReseal(t *testing.T) {
	tr := NewLimitTracker("000001.SZ", at("09:30:00"), 10, 0.10, 2, false)
	up, down := tr.Prices()
	if up != 11 || down != 9 {
		t.Fatal(up, down)
	}
	if ev := tr.Push(limitSnap("09:40:00", 10.99, 11, [][2]float64{{10.99, 100}}, [][2]float64{{11, 500}})); len(ev) != 0 {
		t.Fatal("touched but not sealed", ev)
	}
	ev := tr.Push(limitSnap("09:41:00", 11, 11, [][2]float64{{11, 20000}}, nil))
	if len(ev) != 1 || ev[0].Kind != EventSeal || ev[0].SealAmount != 220000 {
		t.Fatalf("%+v", ev)
	}
	ev = tr.Push(limitSnap("10:00:00", 10.95, 11, [][2]float64{{10.95, 100}}, [][2]float64{{10.96, 100}}))
	if len(ev) != 1 || ev[0].Kind != EventBreak {
		t.Fatalf("%+v", ev)
	}
	ev = tr.Push(limitSnap("10:30:00", 11, 11, [][2]float64{{11, 5000}}, nil))
	if len(ev) != 1 || ev[0].Kind != EventReseal {
		t.Fatalf("%+v", ev)
	}
	boards := tr.Boards()
	if len(boards) != 1 {
		t.Fatalf("%+v", boards)
	}
	b := boards[0]
	if b.Status != StatusSealed || b.Consecutive != 3 || b.OpenCount != 1 ||
		b.FirstSealAt.Format("15:04") != "09:41" || b.LastSealAt.Format("15:04") != "10:30" || b.SealAmount != 55000 {
		t.Fatalf("%+v", b)
	}
	tr.Push(limitSnap("14:59:00", 10.8, 11, [][2]float64{{10.8, 1}}, [][2]float64{{10.81, 1}}))
	b = tr.Boards()[0]
	if b.Status != StatusBroken || b.Consecutive != 0 || b.OpenCount != 2 {
		t.Fatalf("%+v", b)
	}
}

func TestLimitDownAndNoLimit(t *testing.T) {
	tr := NewLimitTracker("000001.SZ", at("09:30:00"), 10, 0.10, 0, false)
	s := Snapshot{Symbol: "000001.SZ", Time: at("10:00:00"), PreClose: 10, High: 10, Low: 9, Last: 9, Ask: [][2]float64{{9, 1000}}}
	tr.Push(s)
	b := tr.Boards()
	if len(b) != 1 || b[0].Direction != DirDown || b[0].Status != StatusSealed || b[0].SealAmount != 9000 {
		t.Fatalf("%+v", b)
	}
	off := NewLimitTracker("000001.SZ", at("09:30:00"), 10, 0.10, 0, true)
	off.Push(limitSnap("09:41:00", 11, 11, [][2]float64{{11, 1}}, nil))
	if off.Enabled() || off.Boards() != nil {
		t.Fatal("new listing has no limit")
	}
}

func TestBoardsFromDaily(t *testing.T) {
	b := DayBar{Time: at("15:00:00"), PreClose: 1.15, High: 1.27, Low: 1.2, Close: 1.27}
	got := BoardsFromDaily("600000.SH", b, 0.10, 1, false)
	if len(got) != 1 || got[0].Status != StatusSealed || got[0].Consecutive != 2 || got[0].LimitPrice != 1.27 {
		t.Fatalf("%+v", got)
	}
	b.Close = 1.25
	got = BoardsFromDaily("600000.SH", b, 0.10, 1, false)
	if got[0].Status != StatusBroken || got[0].Consecutive != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestNoLimitFirstFiveTradingDays(t *testing.T) {
	list := time.Date(2026, 9, 28, 0, 0, 0, 0, tradecal.Shanghai()) // 周一
	cases := []struct {
		day  string
		want bool
	}{
		{"2026-09-28", true}, {"2026-09-30", true},
		// 国庆休市 10-01 至 10-07，第 4、5 个交易日是 10-08、10-09；10-10 调休周六不开市。
		{"2026-10-09", true}, {"2026-10-12", false},
	}
	for _, c := range cases {
		d, _ := time.ParseInLocation("2006-01-02", c.day, tradecal.Shanghai())
		if got := NoLimit(nil, list, d); got != c.want {
			t.Fatalf("%s: %v want %v", c.day, got, c.want)
		}
	}
	if NoLimit(nil, time.Time{}, list) {
		t.Fatal("unknown list date")
	}
}
