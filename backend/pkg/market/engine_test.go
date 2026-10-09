package market

import (
	"fmt"
	"testing"
	"time"
)

func engineDay() time.Time { return at("00:00:00") }

func loadMarket(e *Engine, n int) []string {
	syms := make([]string, n)
	for i := 0; i < n; i++ {
		sym := fmt.Sprintf("%06d.SZ", i+1)
		if i%2 == 0 {
			sym = fmt.Sprintf("6%05d.SH", i+1)
		}
		syms[i] = sym
		hist := randomBars(250, int64(i))
		last := hist[len(hist)-1]
		e.Load(StockInfo{Symbol: sym, FloatShare: 1e8, Adj: last.Adj, MA5Volume: 1.5e6, PrevAmount: 3e7}, hist)
	}
	return syms
}

func TestEngineEndToEnd(t *testing.T) {
	e := NewEngine(engineDay(), EngineConfig{StaleAfter: 10 * time.Second})
	e.Load(StockInfo{Symbol: "000001.SZ", Adj: 1, PrevConsecutive: 1, FloatShare: 1e6, MA5Volume: 24000}, randomBars(100, 1))
	e.Load(StockInfo{Symbol: "000002.SZ", Adj: 1}, randomBars(100, 2))

	mk := func(sym, hhmmss string, last float64, vol int64, bid, ask [][2]float64) Snapshot {
		ts := at(hhmmss)
		return Snapshot{Symbol: sym, Time: ts, AsOf: ts, PreClose: 10, Open: 10.5, High: last, Low: 10, Last: last, Volume: vol, Amount: float64(vol) * last, Bid: bid, Ask: ask}
	}
	pre := mk("000001.SZ", "09:22:00", 10.5, 0, [][2]float64{{10.5, 3000}}, [][2]float64{{10.5, 1000}})
	e.Process([]Snapshot{pre}, at("09:22:01"))
	auc := mk("000001.SZ", "09:25:03", 10.5, 1000, [][2]float64{{10.49, 10}}, [][2]float64{{10.5, 10}})
	e.Process([]Snapshot{auc}, at("09:25:04"))
	if rows := e.AuctionCross(); len(rows) != 1 || rows[0].Values["auction_unmatched"] != 2000 || rows[0].Values["auction_vol_ratio"] != 10 {
		t.Fatalf("%+v", rows)
	}

	up := e.Process([]Snapshot{
		mk("000001.SZ", "09:31:00", 11, 5000, [][2]float64{{11, 9000}}, nil),
		mk("000002.SZ", "09:31:00", 9.9, 800, [][2]float64{{9.89, 1}}, [][2]float64{{9.9, 1}}),
	}, at("09:31:01"))
	if len(up.Limits) != 1 || up.Limits[0].Kind != EventSeal || len(up.Bars) != 0 {
		t.Fatalf("%+v", up)
	}
	v, stale, ok := e.Factors("000001.SZ")
	if !ok || stale || v["up_sealed"] != 1 || v["consecutive"] != 2 || v["limit_up_price"] != 11 || v["first_seal_min"] != 2 {
		t.Fatalf("%v", v)
	}
	// 过期快照不参与计算。
	old := mk("000002.SZ", "09:32:00", 9, 900, nil, nil)
	old.AsOf = at("09:31:00")
	if up := e.Process([]Snapshot{old}, at("09:32:30")); up.Expired != 1 {
		t.Fatal("expired")
	}
	if _, stale, _ := e.Factors("000002.SZ"); !stale {
		t.Fatal("stale flag")
	}
	if bars := e.Flush(at("09:32:06")); len(bars) != 2 {
		t.Fatalf("%+v", bars)
	}

	s := e.Sentiment(at("09:33:00"))
	if s.UpCount != 1 || s.MaxHeight != 2 || !s.Stale || s.Phase == "" {
		t.Fatalf("%+v", s)
	}
	daily, rows := e.CloseDay()
	daily2, rows2 := e.CloseDay()
	if len(daily) != 2 || len(rows) != 2 || len(daily2) != 2 || rows2[0].Values["ma5"] != rows[0].Values["ma5"] {
		t.Fatal("close day must be idempotent")
	}
	if rows[0].AsOf.Format("15:04") != "15:00" {
		t.Fatal(rows[0].AsOf)
	}
}

func TestSentimentStaleShare(t *testing.T) {
	e := NewEngine(engineDay(), EngineConfig{StaleAfter: 10 * time.Second})
	syms := loadMarket(e, 100)
	if !e.Sentiment(at("09:31:00")).Stale {
		t.Fatal("no quotes yet must be stale")
	}
	now := at("09:31:00")
	snaps := make([]Snapshot, len(syms))
	for i, sym := range syms {
		snaps[i] = Snapshot{Symbol: sym, Time: now, AsOf: now, PreClose: 10, Open: 10, High: 10, Low: 10, Last: 10, Volume: 100, Amount: 1000}
	}
	snaps[0].Stale = true
	e.Process(snaps, now.Add(time.Second))
	if e.Sentiment(now).Stale {
		t.Fatal("one stale quote out of 100 must not mark the market stale")
	}
	for i := 0; i < 10; i++ {
		snaps[i].Stale = true
	}
	e.Process(snaps, now.Add(2*time.Second))
	if !e.Sentiment(now).Stale {
		t.Fatal("10% stale quotes must mark the market stale")
	}
}

func TestEngineAlertsAndState(t *testing.T) {
	e := NewEngine(engineDay(), EngineConfig{})
	e.Load(StockInfo{Symbol: "000001.SZ", Adj: 1}, nil)
	mk := func(sym, hhmmss string, last float64, vol int64, ask [][2]float64) Snapshot {
		return Snapshot{Symbol: sym, Time: at(hhmmss), PreClose: 10, Open: 10, High: last, Low: 9.5, Last: last, Volume: vol, Amount: float64(vol) * last,
			Bid: [][2]float64{{last, 100}}, Ask: ask}
	}
	idx := func(hhmmss string, last float64) Snapshot {
		return Snapshot{Symbol: "000001.SH", Time: at(hhmmss), PreClose: 3000, Open: 3000, High: 3000, Low: last, Last: last, Volume: 1}
	}
	ask := [][2]float64{{0, 0}}
	var alerts []Alert
	for i := 0; i < 6; i++ {
		ts := fmt.Sprintf("09:%02d:10", 30+i)
		up := e.Process([]Snapshot{mk("000001.SZ", ts, 10, int64(100*(i+1)), [][2]float64{{10.01, 1}}), idx(ts, 3000-float64(i)*10)}, at(ts))
		alerts = append(alerts, up.Alerts...)
	}
	up := e.Process([]Snapshot{mk("000001.SZ", "09:36:20", 10.5, 900, [][2]float64{{10.51, 1}})}, at("09:36:20"))
	if len(up.Alerts) != 1 || up.Alerts[0].Kind != AlertSurge {
		t.Fatalf("%+v %+v", alerts, up.Alerts)
	}
	up = e.Process([]Snapshot{mk("000001.SZ", "09:36:40", 11, 1000, ask)}, at("09:36:40"))
	if len(up.Alerts) != 1 || up.Alerts[0].Kind != AlertLimitUp {
		t.Fatalf("%+v", up.Alerts)
	}
	up = e.Process([]Snapshot{mk("000001.SZ", "09:36:50", 10.9, 1100, [][2]float64{{10.95, 1}})}, at("09:36:50"))
	if len(up.Alerts) != 1 || up.Alerts[0].Kind != AlertBreak {
		t.Fatalf("%+v", up.Alerts)
	}
	st := e.State(at("09:37:00"))
	// 指数现价 2950，前收 3000、5 分钟前（09:30 收盘）3000：两项都是 -1.67%，5 分钟跌幅超过 -1.5%，风险度 3。
	if st.RiskLevel != 3 || st.IndexPct > -1.6 || st.Index5m > -1.6 || st.Stale {
		t.Fatalf("%+v", st)
	}
}

// 验收：5000 只股票单轮全量刷新 ≤ 30 秒。
func TestFullMarketRefreshWithin30s(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
	e := NewEngine(engineDay(), EngineConfig{})
	syms := loadMarket(e, 5000)
	batch := make([]Snapshot, len(syms))
	start := time.Now()
	for round := 0; round < 3; round++ {
		ts := at("10:00:00").Add(time.Duration(round*3) * time.Second)
		for i, sym := range syms {
			px := 10 + float64(i%100)/100
			batch[i] = Snapshot{Symbol: sym, Time: ts, AsOf: ts, PreClose: 10, Open: 10, High: px, Low: 9.9, Last: px,
				Volume: int64(1e6 + round*1000 + i), Amount: 1e7, Bid: [][2]float64{{px - 0.01, 100}}, Ask: [][2]float64{{px, 100}}}
		}
		e.Process(batch, ts)
		e.Sentiment(ts)
		e.Cross(nil)
	}
	per := time.Since(start) / 3
	t.Logf("5000 stocks per round: %v", per)
	if per > 30*time.Second {
		t.Fatalf("too slow: %v", per)
	}
}

func BenchmarkProcess5000(b *testing.B) {
	e := NewEngine(engineDay(), EngineConfig{})
	syms := loadMarket(e, 5000)
	batch := make([]Snapshot, len(syms))
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		ts := at("10:00:00").Add(time.Duration(n*3) * time.Second)
		for i, sym := range syms {
			batch[i] = Snapshot{Symbol: sym, Time: ts, AsOf: ts, PreClose: 10, Open: 10, High: 10.5, Low: 9.9, Last: 10.2, Volume: int64(1e6 + n + 1), Amount: 1e7}
		}
		e.Process(batch, ts)
	}
}
