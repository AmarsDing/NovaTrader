package market

import (
	"testing"
)

/* P3-10 首个真实 missing 路径（0.数据纪律 §6）：封/开板 pct 基准 = 建 tracker 时锁定的当日昨收（limBase）；
   后续快照 pre_close 缺失不回退伪 0；当日昨收全程缺失 → 不产生封/开板警报（没有判据就不发警，而不是发 pct=0 的假警）。 */

func TestLimitAlertBaseLockedAtTrackerBuild(t *testing.T) {
	e := NewEngine(engineDay(), EngineConfig{})
	e.Load(StockInfo{Symbol: "000001.SZ", Adj: 1, Name: "测试股"}, nil)
	const sym = "000001.SZ"
	// 首帧带昨收 → tracker 建成并锁定 limBase=10（封板价 11）。
	up := e.Process([]Snapshot{alertSnap(sym, "09:31:00", 10.2, 1000, 10)}, at("09:31:00"))
	if len(up.Alerts) != 0 {
		t.Fatalf("预热帧不该发警：%+v", up.Alerts)
	}
	// 之后帧 PreClose 缺失（源未下发昨收）：封板仍命中，pct 按锁定昨收算 = +10%，不落伪 0。
	up = e.Process([]Snapshot{alertSnap(sym, "09:40:00", 11, 2000, 0)}, at("09:40:00"))
	var seal, brk *Alert
	for i := range up.Alerts {
		if up.Alerts[i].Kind == AlertLimitUp {
			seal = &up.Alerts[i]
		}
	}
	if seal == nil {
		t.Fatalf("seal alert missing: %+v", up.Alerts)
	}
	if seal.Pct != 10 {
		t.Fatalf("pct 应按锁定昨收 10 算 = 10，got %v", seal.Pct)
	}
	// 开板同样按锁定基准：10.5 → +5%。
	up = e.Process([]Snapshot{alertSnap(sym, "09:41:00", 10.5, 3000, 0)}, at("09:41:00"))
	for i := range up.Alerts {
		if up.Alerts[i].Kind == AlertBreak {
			brk = &up.Alerts[i]
		}
	}
	if brk == nil || brk.Pct != 5 {
		t.Fatalf("break alert = %+v", up.Alerts)
	}
}

func TestLimitAlertNoBaseNoAlert(t *testing.T) {
	e := NewEngine(engineDay(), EngineConfig{})
	e.Load(StockInfo{Symbol: "000001.SZ", Adj: 1, Name: "新股"}, nil)
	const sym = "000001.SZ"
	// 当日昨收全程缺失（新股首日/源缺 pre_close）：即使价格行为酷似封板也不发涨跌停警报。
	for i, ts := range []string{"09:31:00", "09:40:00", "09:45:00"} {
		up := e.Process([]Snapshot{alertSnap(sym, ts, 11, int64(1000*(i+1)), 0)}, at(ts))
		for _, a := range up.Alerts {
			if a.Kind == AlertLimitUp || a.Kind == AlertBreak {
				t.Fatalf("昨收缺失不应发封/开板警报：%+v", a)
			}
		}
	}
	if e.stocks[sym].lim != nil {
		t.Fatal("昨收全程缺失不应建 tracker")
	}
}


func alertSnap(sym, hhmmss string, last float64, vol int64, preClose float64) Snapshot {
	ts := at(hhmmss)
	return Snapshot{Symbol: sym, Time: ts, AsOf: ts, PreClose: preClose, Open: 10, High: last, Low: 9.5, Last: last, Volume: vol, Amount: float64(vol) * last,
		Bid: [][2]float64{{last, 100}}, Ask: [][2]float64{{last, 0}}}
}

func TestAlertSameKindDedup5Min(t *testing.T) {
	e := NewEngine(engineDay(), EngineConfig{})
	e.Load(StockInfo{Symbol: "000001.SZ", Adj: 1, Name: "测试股"}, nil)
	const sym = "000001.SZ"
	// 逐分钟收盘三根（09:32/09:33/09:34 的 10.00 一根根封出），急拉基准=09:32 收盘 10。
	for i, ts := range []string{"09:32:05", "09:33:05", "09:34:05", "09:35:05"} {
		e.Process([]Snapshot{alertSnap(sym, ts, 10, int64(1000*(i+1)), 10)}, at(ts))
	}
	up := e.Process([]Snapshot{alertSnap(sym, "09:35:30", 10.2, 6000, 10)}, at("09:35:30"))
	if len(up.Alerts) != 1 || up.Alerts[0].Kind != AlertSurge {
		t.Fatalf("first surge expected, got %+v", up.Alerts)
	}
	if up.Alerts[0].Pct < 1.9 || up.Alerts[0].Pct > 2.1 {
		t.Fatalf("pct = %v, want ~2", up.Alerts[0].Pct)
	}
	if up.Alerts[0].Name != "测试股" {
		t.Fatalf("name = %q", up.Alerts[0].Name)
	}
	// 5 分钟内同类再来一次，幅度也够：被去重压住。
	up = e.Process([]Snapshot{alertSnap(sym, "09:36:05", 10.3, 7000, 10)}, at("09:36:05"))
	if len(up.Alerts) != 0 {
		t.Fatalf("5min 内同类应被去重：%+v", up.Alerts)
	}
	// 越过 5 分钟窗口（最新分钟封出 10.2，基准后移）再放行：09:41:30 的 10.5 vs 09:38 收盘 10.2 = +2.94%。
	closes := []struct {
		ts        string
		last      float64
	}{
		{"09:36:35", 10.2}, {"09:37:05", 10.2}, {"09:38:05", 10.2}, {"09:39:05", 10.2},
		{"09:40:05", 10.2}, {"09:41:05", 10.2},
	}
	for i, c := range closes {
		e.Process([]Snapshot{alertSnap(sym, c.ts, c.last, int64(8000+i*100), 10)}, at(c.ts))
	}
	up = e.Process([]Snapshot{alertSnap(sym, "09:41:30", 10.5, 9000, 10)}, at("09:41:30"))
	if len(up.Alerts) != 1 || up.Alerts[0].Kind != AlertSurge {
		t.Fatalf("5 分钟窗口后应重新放行：%+v", up.Alerts)
	}
}

func TestAuctionClearsDedup(t *testing.T) {
	e := NewEngine(engineDay(), EngineConfig{})
	e.Load(StockInfo{Symbol: "000001.SZ", Adj: 1}, nil)
	const sym = "000001.SZ"
	// 盘前进过一只快照，造出 stockState。
	e.Process([]Snapshot{alertSnap(sym, "09:22:00", 10.5, 0, 10)}, at("09:22:01"))
	st := e.stocks[sym]
	if st == nil {
		t.Fatal("stock state missing")
	}
	st.alertAt[AlertSurge] = at("09:24:50")
	// 竞价落定（09:25~09:30、有量）→ 去重状态清零。
	e.Process([]Snapshot{alertSnap(sym, "09:25:03", 10.5, 1000, 10)}, at("09:25:04"))
	if len(st.alertAt) != 0 {
		t.Fatalf("竞价清零窗口应清空去重状态：%v", st.alertAt)
	}
	if st.auction == nil {
		t.Fatal("auction not captured")
	}
}

func TestLimitAlertDedup(t *testing.T) {
	e := NewEngine(engineDay(), EngineConfig{})
	e.Load(StockInfo{Symbol: "000001.SZ", Adj: 1, Name: "测试股"}, nil)
	const sym = "000001.SZ"
	// 封板（10% 上限 11）。
	up := e.Process([]Snapshot{alertSnap(sym, "09:40:00", 11, 1000, 10)}, at("09:40:00"))
	if len(up.Alerts) != 1 || up.Alerts[0].Kind != AlertLimitUp || up.Alerts[0].Pct != 10 {
		t.Fatalf("seal alert = %+v", up.Alerts)
	}
	// 开板。
	up = e.Process([]Snapshot{alertSnap(sym, "09:40:20", 10.9, 1100, 10)}, at("09:40:20"))
	if len(up.Alerts) != 1 || up.Alerts[0].Kind != AlertBreak {
		t.Fatalf("break alert = %+v", up.Alerts)
	}
	// 5 分钟内再封再开（重新触发同类）：同类去重压掉，不齐发。
	up = e.Process([]Snapshot{alertSnap(sym, "09:40:40", 11, 1200, 10)}, at("09:40:40"))
	if len(up.Alerts) != 0 {
		t.Fatalf("5min 内重封应去重：%+v", up.Alerts)
	}
	// 已过 5 分钟但状态没有新迁移（仍在封板）：不重复发。
	up = e.Process([]Snapshot{alertSnap(sym, "09:45:10", 11, 1300, 10)}, at("09:45:10"))
	if len(up.Alerts) != 0 {
		t.Fatalf("无状态迁移不应再发同类异动：%+v", up.Alerts)
	}
	// 5 分钟后开板 → 新事件、超过去重窗，应放行。
	up = e.Process([]Snapshot{alertSnap(sym, "09:45:20", 10.8, 1400, 10)}, at("09:45:20"))
	if len(up.Alerts) != 1 || up.Alerts[0].Kind != AlertBreak {
		t.Fatalf("5 分钟后的开板应放行：%+v", up.Alerts)
	}
}
