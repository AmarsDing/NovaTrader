package data

import (
	"fmt"
	"testing"
	"time"

	"server/pkg/rules"
	"server/pkg/tradecal"

	"github.com/go-kratos/kratos/v2/log"
)

// dayBarsCache 纯层：最多留两个交易日（旧→新挤出）、读取一律浅拷贝——
// 浅拷贝兜的是调用方对 map 的增删；切片本体按只读合同共享（pkg/rules 只读已核实）。
// dailyBars 未命中路径不回读缓存（cloneDayBars 直克隆），并发挤出也不交付 nil——
// 这条防线是结构性的（路径上无 get），本用例钉的是克隆/淘汰的组成件。
func TestDayBarsCacheRetentionAndClone(t *testing.T) {
	c := dayBarsCache{}
	m1 := map[string][]rules.Bar{"600000.SH": {{Close: 10}}}
	c.put(1, m1)
	got, ok := c.get(1)
	if !ok || len(got) != 1 || got["600000.SH"][0].Close != 10 {
		t.Fatalf("round-trip: %v %v", got, ok)
	}
	delete(got, "600000.SH")
	got["EVIL"] = nil
	again, ok := c.get(1)
	if !ok || len(again) != 1 || again["600000.SH"] == nil {
		t.Fatalf("caller tamper must not corrupt the store: %v", again)
	}

	// 最多两天：插入第 3 个 key，最旧的被挤出。
	c.put(2, m1)
	c.put(3, m1)
	if _, ok := c.get(1); ok {
		t.Fatal("oldest day must be evicted (keep 2)")
	}
	if _, ok := c.get(2); !ok {
		t.Fatal("middle day must survive")
	}

	// 同 key 覆盖不换位置不重复计数：覆盖 3 后再插 4，被挤的是 2。
	mNew := map[string][]rules.Bar{"600001.SH": {{Close: 20}}}
	c.put(3, mNew)
	if cur, _ := c.get(3); cur["600001.SH"] == nil {
		t.Fatalf("overwrite must replace: %v", cur)
	}
	c.put(4, m1)
	if _, ok := c.get(2); ok {
		t.Fatal("overwrite must keep insertion order: 2 should be evicted by 4")
	}
	if _, ok := c.get(3); !ok {
		t.Fatal("overwritten 3 must survive")
	}
	if _, ok := c.get(4); !ok {
		t.Fatal("newest 4 must survive")
	}
}

// 全市场调研（代码数 > symbolFilter、省略 IN 的分支）按交易日缓存：同一天第二次调研
// 不再查库——窗口内后写入的日线对缓存不可见；换天即重查。小批量 IN 分支不缓存，随时可见新行。
func TestDailyBarsCacheFullMarketBranch(t *testing.T) {
	d := newTestData(t)
	ms := NewMarketSource(d, log.DefaultLogger).(*marketSource)
	sh := tradecal.Shanghai()
	asOf := time.Date(2026, 10, 9, 10, 0, 0, 0, sh) // 2026-10-09 周五
	prev := time.Date(2026, 10, 8, 0, 0, 0, 0, sh)
	seed := func(sym string, day time.Time, close float64) {
		d.client.MarketData.Create().SetSymbol(sym).SetFreq("1d").SetBarTime(day).
			SetOpen(close).SetHigh(close).SetLow(close).SetClose(close).
			SetVolume(1000).SetAmount(1e6).SetAdjFactor(1).ExecX(ctx)
	}
	seed("600000.SH", prev, 10)
	seed("600001.SH", prev, 20)

	// 假代码只为越过 symbolFilter 触发全市场分支：无 IN 条件的查询结果与代码集无关。
	many := make([]string, symbolFilter+1)
	for i := range many {
		many[i] = fmt.Sprintf("60%04d.SH", i)
	}

	got, err := ms.dailyBars(ctx, many, asOf)
	if err != nil || len(got) != 2 {
		t.Fatalf("first scan = %d, err %v", len(got), err)
	}

	// 窗口内补写一根（10-07）：同日缓存命中，新行必须不可见（这次没有查库）。
	seed("600002.SH", prev.AddDate(0, 0, -1), 30)
	again, err := ms.dailyBars(ctx, many, asOf)
	if err != nil || len(again) != 2 {
		t.Fatalf("cached day must not re-query, late row must stay invisible: %d %v", len(again), err)
	}

	// 浅拷贝：清空返回的 map，缓存本体不受影响。
	for k := range again {
		delete(again, k)
	}
	third, err := ms.dailyBars(ctx, many, asOf)
	if err != nil || len(third) != 2 {
		t.Fatalf("caller tamper must not corrupt cache: %d %v", len(third), err)
	}

	// 换天：重查库，10-07 那根进来了（3 只）。
	nextDay := time.Date(2026, 10, 13, 10, 0, 0, 0, sh) // 周二
	fresh, err := ms.dailyBars(ctx, many, nextDay)
	if err != nil || len(fresh) != 3 {
		t.Fatalf("new day must re-query: %d %v", len(fresh), err)
	}

	// 小批量 IN 分支不缓存：再补一根立刻可见（600000.SH 变 2 根）。
	seed("600000.SH", prev.AddDate(0, 0, -2), 9)
	small, err := ms.dailyBars(ctx, []string{"600000.SH"}, nextDay)
	if err != nil || len(small["600000.SH"]) != 2 {
		t.Fatalf("IN branch must not be cached: %v %v", small, err)
	}
}
