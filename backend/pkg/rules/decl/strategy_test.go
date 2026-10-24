package decl

import (
	"context"
	"testing"
	"time"

	"server/pkg/rules"
)

// barsFixture 20 根已收盘日线：前 10 根 flat 10、后 10 根 flat 12 →
// MA10=12、MA20=11，手算值闭式可核。
func barsFixture() []rules.Bar {
	bars := make([]rules.Bar, 0, 20)
	for i := range 20 {
		c := 10.0
		if i >= 10 {
			c = 12
		}
		bars = append(bars, rules.Bar{Open: c, High: c, Low: c, Close: c, Amount: 1e8, Volume: 1e7})
	}
	return bars
}

func snapFixture() rules.Snapshot {
	return rules.Snapshot{
		Symbol: "600519.SH",
		AsOf:   time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC),
		Bars:   barsFixture(),
		Today:  &rules.Bar{Open: 12.5, High: 12.9, Low: 12.4, Close: 12.8, Amount: 3e8, Volume: 3e7},
	}
}

// dslJSON 是 DSL 版迷你动量策略：趋势多头 + 偏离阈值 + 缩量背书。
const dslJSON = `{
  "version": 1, "name": "dsl_momo", "title": "迷你动量", "hold_days": 5,
  "params": [{"name":"dev_min","default":0.03,"min":0,"max":0.1}],
  "filter": {"op":"and","args":[
    {"op":"gt","args":[{"op":"ma_close","args":[{"op":"num","num":10}]},{"op":"ma_close","args":[{"op":"num","num":20}]}]},
    {"op":"gte","args":[{"op":"field","name":"dev_ma10"},{"op":"param","name":"dev_min"}]},
    {"op":"lt","args":[{"op":"field","name":"volume"},{"op":"mul","args":[{"op":"num","num":0.7},{"op":"ma_amount","args":[{"op":"num","num":5}]}]}]}
  ]},
  "score": {"base": 40, "adds": [
    {"when": {"op":"gt","args":[{"op":"field","name":"amount"},{"op":"num","num":250000000}]}, "num": {"op":"num","num":10}},
    {"num": {"op":"mul","args":[{"op":"clamp","args":[{"op":"abs","args":[{"op":"field","name":"dev_ma10"}]},{"op":"num","num":0},{"op":"num","num":0.05}]},{"op":"num","num":300}]}, "cap": 15}
  ]}
}`

func mustStrategy(t *testing.T, code string) rules.Strategy {
	t.Helper()
	def, err := Parse([]byte(code))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	st, err := def.Strategy(rules.DefaultPrice())
	if err != nil {
		t.Fatalf("strategy: %v", err)
	}
	return st
}

// TestStrategyTieOut DSL 与手写真值对拍：MA10/MA20/缩量/加分逐项工算。
func TestStrategyTieOut(t *testing.T) {
	st := mustStrategy(t, dslJSON)
	ctx := context.Background()
	s := snapFixture()
	if st.Name() != "dsl_momo" {
		t.Fatalf("name %q", st.Name())
	}
	if !st.Filter(ctx, s) {
		t.Fatal("filter should pass: MA10=12>MA20=11, dev=0.0667>=0.03, vol 3e7 < 0.7*1e8=7e7")
	}
	// score = 40 + 10（额 3e8 > 2.5e8）+ min((0.05 封顶)*300=15, cap15) = 65
	if got := st.Score(ctx, s); got != 65 {
		t.Fatalf("score = %v, want 65", got)
	}
	plan, ok := st.EntryPlan(ctx, s)
	if !ok || plan.Price != 12.8 || plan.HoldDays != 5 || plan.Side != rules.SideBuy {
		t.Fatalf("entry %+v ok=%v", plan, ok)
	}
	if plan.Reason != "迷你动量" {
		t.Fatalf("reason %q", plan.Reason)
	}
}

// TestFilterFalse 趋势破坏 → 不通过。
func TestFilterFalse(t *testing.T) {
	st := mustStrategy(t, dslJSON)
	flat := make([]rules.Bar, 20)
	for i := range flat {
		flat[i] = rules.Bar{Open: 10, High: 10, Low: 10, Close: 10, Amount: 1e8, Volume: 1e7}
	}
	s := rules.Snapshot{Bars: flat, Today: &rules.Bar{Open: 10, High: 10, Low: 10, Close: 10, Amount: 3e8, Volume: 3e7}}
	if st.Filter(context.Background(), s) {
		t.Fatal("MA10=MA20=10 不应通过趋势条件")
	}
}

// TestMissingPropagation 缺失即不通过 / 加分缺失跳过 / 除零缺失——零 0 冒充。
func TestMissingPropagation(t *testing.T) {
	ctx := context.Background()
	st := mustStrategy(t, dslJSON)

	// 窗口不足：MA20 取不出 → Filter false（不是「趋势不满足」而是「没取到」，对外同为不入选）
	short := snapFixture()
	short.Bars = short.Bars[:19] // MA20 需 20 根
	if st.Filter(ctx, short) {
		t.Fatal("MA20 窗口不足应按缺失不通过")
	}

	// 盘前无 Today：field amount/volume/dev 全缺 → Filter false；Score 两个加分项跳过 → 40
	pre := rules.Snapshot{Bars: barsFixture()}
	if st.Filter(ctx, pre) {
		t.Fatal("盘前无当日价应按缺失不通过")
	}
	if got := st.Score(ctx, pre); got != 40 {
		t.Fatalf("盘前 score = %v, want 40（缺失加分跳过）", got)
	}

	// 当日 0 价视为未取到（数据纪律）：Today.Close=0 → Last 回落昨收 12，dev/amount 仍缺？
	// amount 3e8>0 可得分项 1；dev 可算（12/12-1=0）< 0.03 → filter false。
	zero := snapFixture()
	cpy := *zero.Today
	cpy.Close = 0
	zero.Today = &cpy
	if st.Filter(ctx, zero) {
		t.Fatal("当日 0 价 dev<0.03 应不通过")
	}

	// 除零缺失
	divZero := mustStrategy(t, `{"version":1,"name":"dsl_dz","hold_days":3,
	  "filter":{"op":"gt","args":[{"op":"div","args":[{"op":"field","name":"last"},{"op":"sub","args":[{"op":"field","name":"prev_close"},{"op":"field","name":"prev_close"}]}]},{"op":"num","num":1}]},
	  "score":{"base":10}}`)
	if divZero.Filter(ctx, snapFixture()) {
		t.Fatal("除零应按缺失不通过")
	}
}

// TestEntryCustom 自定义参考价表达式与缺失回退。
func TestEntryCustom(t *testing.T) {
	ctx := context.Background()
	custom := mustStrategy(t, `{"version":1,"name":"dsl_px","hold_days":2,
	  "filter":{"op":"bool","bool":true},
	  "score":{"base":50},
	  "entry":{"price":{"op":"mul","args":[{"op":"field","name":"prev_close"},{"op":"num","num":0.99}]}}}`)
	s := snapFixture()
	plan, ok := custom.EntryPlan(ctx, s)
	if want := s.PrevClose() * 0.99; !ok || plan.Price != want {
		t.Fatalf("custom entry %+v ok=%v, want %v", plan, ok, want)
	}
	// 入场价表达式取不到（盘前用 prev_close*0.99 仍可得；改用 field close 盘前必缺）
	missing := mustStrategy(t, `{"version":1,"name":"dsl_pm","hold_days":2,
	  "filter":{"op":"bool","bool":true},
	  "score":{"base":50},
	  "entry":{"price":{"op":"field","name":"close"}}}`)
	if _, ok := missing.EntryPlan(ctx, rules.Snapshot{Bars: barsFixture()}); ok {
		t.Fatal("盘前 close 缺失应不出计划")
	}
	// 参考价 ≤0 / NaN 递伪拦截
	bad := mustStrategy(t, `{"version":1,"name":"dsl_pn","hold_days":2,
	  "filter":{"op":"bool","bool":true},
	  "score":{"base":50},
	  "entry":{"price":{"op":"sub","args":[{"op":"field","name":"last"},{"op":"field","name":"last"}]}}}`)
	if _, ok := bad.EntryPlan(ctx, snapFixture()); ok {
		t.Fatal("参考价 0 应不出计划")
	}
}

// TestExitDelegate 退出与内置模板同链 rules.Exit：止损→利空→止盈→情绪→时间。
func TestExitDelegate(t *testing.T) {
	ctx := context.Background()
	st := mustStrategy(t, dslJSON)
	s := snapFixture()

	// 无触发：止损 12.0 < 现价 12.8、无利空、无止盈、非 FADE、持有 0 天
	plan, ok := st.ExitPlan(ctx, s, rules.Position{Symbol: "X", Available: 100, AvgCost: 12.5, StopLoss: 12.0, TakeProfit: 14.0})
	if ok {
		t.Fatalf("不应出退出计划 %+v", plan)
	}
	// 触发止损
	plan, ok = st.ExitPlan(ctx, s, rules.Position{Symbol: "X", Available: 100, AvgCost: 13.2, StopLoss: 13.0})
	if !ok || plan.ExitKind != rules.ExitStopLoss {
		t.Fatalf("止损链 %+v ok=%v", plan, ok)
	}
	// 时间到（HoldDays>=5）
	plan, ok = st.ExitPlan(ctx, s, rules.Position{Symbol: "X", Available: 100, AvgCost: 12.5, StopLoss: 12.0, TakeProfit: 14.0, HoldDays: 5})
	if !ok || plan.ExitKind != rules.ExitTime {
		t.Fatalf("时间止损 %+v ok=%v", plan, ok)
	}
	// 盘前（无 Today）不出计划
	if _, ok := st.ExitPlan(ctx, rules.Snapshot{Bars: barsFixture()}, rules.Position{Available: 100, StopLoss: 13.0}); ok {
		t.Fatal("盘前不应出退出计划")
	}
}

// TestScoreClamp 综合分封顶 100；param 引用取默认值。
func TestScoreClamp(t *testing.T) {
	st := mustStrategy(t, `{"version":1,"name":"dsl_top","hold_days":3,
	  "params":[{"name":"boost","default":15,"min":0,"max":20}],
	  "filter":{"op":"bool","bool":true},
	  "score":{"base":95,"adds":[{"num":{"op":"param","name":"boost"}}]}}`)
	if got := st.Score(context.Background(), snapFixture()); got != 100 {
		t.Fatalf("score = %v, want 100 封顶", got)
	}
}

// TestHighLowOps high_n/low_n 取窗最值与缺失口径。
func TestHighLowOps(t *testing.T) {
	ctx := context.Background()
	st := mustStrategy(t, `{"version":1,"name":"dsl_hl","hold_days":3,
	  "filter":{"op":"and","args":[
	    {"op":"eq","args":[{"op":"high_n","args":[{"op":"num","num":10}]},{"op":"num","num":12}]},
	    {"op":"eq","args":[{"op":"low_n","args":[{"op":"num","num":10}]},{"op":"num","num":12}]}
	  ]},
	  "score":{"base":50}}`)
	if !st.Filter(ctx, snapFixture()) {
		t.Fatal("high_n(10)=low_n(10)=12（后十窗全 12）应通过")
	}
	short := rules.Snapshot{Bars: barsFixture()[:9]}
	if st.Filter(ctx, short) {
		t.Fatal("窗口不足 high_n 缺失应不通过")
	}
}

// TestStrategyFromZeroValue 未 Parse 的零值 Definition 拒绝构造（崩不上的护栏）。
func TestStrategyFromZeroValue(t *testing.T) {
	var d Definition
	if _, err := d.Strategy(rules.DefaultPrice()); err == nil {
		t.Fatal("零值定义应拒绝构造")
	}
}
