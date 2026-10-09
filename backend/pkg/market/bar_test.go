package market

import (
	"testing"
	"time"

	"server/pkg/tradecal"
)

func at(hhmmss string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04:05", "2026-10-09 "+hhmmss, tradecal.Shanghai())
	if err != nil {
		panic(err)
	}
	return t
}

func snap(hhmmss string, last, high, low float64, vol int64, amt float64) Snapshot {
	return Snapshot{Symbol: "600519.SH", Time: at(hhmmss), PreClose: 10, Open: 10.1, High: high, Low: low, Last: last, Volume: vol, Amount: amt}
}

func TestSlotConvention(t *testing.T) {
	cases := map[string]string{
		"09:30:00": "09:30", "09:30:59": "09:30", "11:29:59": "11:29", "11:30:03": "11:29",
		"12:10:00": "11:29", "13:00:00": "13:00", "14:59:30": "14:59", "15:00:04": "14:59",
	}
	for in, want := range cases {
		s, ok := Slot(at(in))
		if !ok || s.Format("15:04") != want {
			t.Fatalf("%s -> %v %v, want %s", in, s, ok, want)
		}
	}
	if _, ok := Slot(at("09:25:03")); ok {
		t.Fatal("auction is not a bar")
	}
	if SlotIndex(at("09:30:00")) != 1 || SlotIndex(at("11:29:00")) != 120 || SlotIndex(at("13:00:00")) != 121 || SlotIndex(at("14:59:00")) != 240 {
		t.Fatal("slot index")
	}
	n := 1
	for s := at("09:30:00"); ; n++ {
		next, ok := NextSlot(s)
		if !ok {
			break
		}
		s = next
	}
	if n != BarsPerDay {
		t.Fatal("bars per day", n)
	}
}

func TestAggregatorBuildsMinutes(t *testing.T) {
	a := NewAggregator("600519.SH")
	// 竞价成交 1000 股并入 09:30。
	a.Push(snap("09:25:03", 10.1, 10.1, 10.1, 1000, 10100))
	a.Push(snap("09:30:03", 10.2, 10.2, 10.1, 1500, 15200))
	// 3 秒内冲到 10.5 又回落，只能从日内最高价看到。
	a.Push(snap("09:30:30", 10.3, 10.5, 10.1, 2000, 20300))
	out := a.Push(snap("09:31:02", 10.4, 10.5, 10.1, 2600, 26500))
	if len(out) != 1 {
		t.Fatalf("want 1 closed bar, got %d", len(out))
	}
	b := out[0]
	if b.Time.Format("15:04") != "09:30" || b.Open != 10.1 || b.High != 10.5 || b.Low != 10.1 || b.Close != 10.3 || b.Volume != 2000 || b.Amount != 20300 {
		t.Fatalf("%+v", b)
	}
	// 09:32、09:33 没有快照，09:34 来了一条：补两根平线。
	out = a.Push(snap("09:34:01", 10.4, 10.5, 10.1, 2700, 27540))
	if len(out) != 3 || out[0].Volume != 600 || out[1].Volume != 0 || out[2].Volume != 0 || out[2].Close != 10.4 {
		t.Fatalf("%+v", out)
	}
	// 封口靠 Flush。
	if got := a.Flush(at("09:35:03"), 5*time.Second); len(got) != 0 {
		t.Fatal("not due yet")
	}
	got := a.Flush(at("09:35:06"), 5*time.Second)
	if len(got) != 1 || got[0].Volume != 100 {
		t.Fatalf("%+v", got)
	}
	// 再过两分钟没有快照，补平线。
	got = a.Flush(at("09:37:06"), 5*time.Second)
	if len(got) != 2 || got[1].Time.Format("15:04") != "09:36" {
		t.Fatalf("%+v", got)
	}
}

func TestAggregatorLateTradeCorrectsClosedBar(t *testing.T) {
	a := NewAggregator("600519.SH")
	a.Push(snap("11:29:10", 10, 10, 10, 1000, 10000))
	if got := a.Flush(at("11:30:31"), 5*time.Second); len(got) != 1 {
		t.Fatalf("%+v", got)
	}
	out := a.Push(snap("11:30:40", 10.01, 10.01, 10, 1100, 11001))
	if len(out) != 1 || out[0].Volume != 1100 || out[0].Close != 10.01 {
		t.Fatalf("%+v", out)
	}
	// 午休不补平线，下午第一根从 13:00 开始。
	if got := a.Flush(at("12:00:00"), 5*time.Second); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	out = a.Push(snap("13:01:00", 10.02, 10.02, 10, 1200, 12003))
	if len(out) != 1 || out[0].Time.Format("15:04") != "13:00" {
		t.Fatalf("%+v", out)
	}
}

func TestDailyFromMinutes(t *testing.T) {
	d, ok := DailyFromMinutes([]Bar{
		{Time: at("09:30:00"), Open: 10, High: 10.2, Low: 9.9, Close: 10.1, Volume: 100, Amount: 1000},
		{Time: at("09:31:00"), Open: 10.1, High: 10.5, Low: 10, Close: 10.4, Volume: 50, Amount: 520},
	})
	if !ok || d.Open != 10 || d.High != 10.5 || d.Low != 9.9 || d.Close != 10.4 || d.Volume != 150 {
		t.Fatalf("%+v", d)
	}
}
