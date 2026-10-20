package biz

// PlanClosure 纯函数金样矩阵：单轮回/多轮回窗口、外来批 FIFO 占位、残余滚窗、
// 卖超 loud、Σ<EV 跳过、Gate-D 跳过、净口径复算（含买入费）、符号按未舍入、
// 非有限账本行拒、reason 降级保键。合同：M06-闭仓归属-2026-10-21.md §1/§3。

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

var closureT0 = time.Date(2026, 10, 20, 15, 0, 0, 0, time.UTC)

func closureSignal(id int, ev int) Signal {
	v := ev
	return Signal{ID: id, Book: "paper", Symbol: "600519.SH", Side: "buy", Status: StatusDone, ExecutedVolume: &v}
}

func closureFill(id int, signalID, side string, qty int, price, fee float64) FillFact {
	return FillFact{ID: id, SignalID: signalID, Side: side, Qty: qty, Price: price, Amount: price * float64(qty), Fee: fee, CreatedAt: closureT0.Add(time.Duration(id) * time.Minute)}
}

var closureEvidenceKeys = []string{"v", "window", "signal_id", "qty", "buy_amount", "buy_fee", "sell_amount", "sell_fee", "exit_price", "pnl_pct", "closed_fill_id", "replay_head", "buys", "sells"}

func closureReasonJSON(t *testing.T, reason string) map[string]any {
	t.Helper()
	if !strings.HasPrefix(reason, closureReasonPrefix) {
		t.Fatalf("reason missing 闭仓归并 prefix: %q", reason)
	}
	body := strings.TrimPrefix(reason, closureReasonPrefix)
	if strings.ContainsAny(body, "\n\t") {
		t.Fatalf("evidence is not compact JSON: %q", body)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("evidence JSON: %v", err)
	}
	if len(m) != len(closureEvidenceKeys) {
		t.Fatalf("evidence keys %v", m)
	}
	for _, k := range closureEvidenceKeys {
		if _, ok := m[k]; !ok {
			t.Fatalf("evidence missing key %s: %v", k, m)
		}
	}
	return m
}

// 单轮回：一笔候选买入 + 一笔卖出归零，毛口径出场价与净口径收益率全钉。
func TestClosureSingleWindowAttributionAndEvidence(t *testing.T) {
	facts := &ClosureFacts{
		Signals: []Signal{closureSignal(101, 1000)},
		Fills: []FillFact{
			closureFill(1, "101", "buy", 1000, 10, 10),
			closureFill(2, "", "sell", 1000, 11, 15),
		},
		PendingIntent: map[int]bool{},
	}
	res, err := PlanClosure(facts)
	if err != nil || len(res.Plans) != 1 || len(res.Over) != 0 {
		t.Fatalf("plans=%v over=%v err=%v", res.Plans, res.Over, err)
	}
	p := res.Plans[0]
	if p.SignalID != 101 || p.Qty != 1000 || p.ExitPrice != 11 || p.Window != [2]int{1, 2} ||
		p.ClosedFillID != 2 || p.ReplayHead != 2 || len(p.Buys) != 1 || len(p.Sells) != 1 ||
		p.BuyAmount != 10000 || p.BuyFee != 10 || p.SellAmount != 11000 || p.SellFee != 15 {
		t.Fatalf("plan %+v", p)
	}
	// 净口径复算（含买入费）：(11000−15 − (10000+10)) / 10010 × 100。
	want := round4(975.0 / 10010.0 * 100)
	if p.PnlPct != 9.7403 || p.PnlPct != want {
		t.Fatalf("pnl %v want %v", p.PnlPct, want)
	}
	if !p.At.Equal(closureT0.Add(2 * time.Minute)) {
		t.Fatalf("closed_at must come from the last attributed sell fill created_at: %v", p.At)
	}
	m := closureReasonJSON(t, p.Reason)
	if m["v"] != 1.0 || m["signal_id"] != 101.0 || m["qty"] != 1000.0 ||
		m["buy_amount"] != 10000.0 || m["buy_fee"] != 10.0 || m["sell_amount"] != 11000.0 || m["sell_fee"] != 15.0 ||
		m["exit_price"] != 11.0 || m["pnl_pct"] != 9.7403 || m["closed_fill_id"] != 2.0 || m["replay_head"] != 2.0 {
		t.Fatalf("evidence %v", m)
	}
	if w := m["window"].([]any); w[0] != 1.0 || w[1] != 2.0 {
		t.Fatalf("window %v", w)
	}
	if buys := m["buys"].([]any); len(buys) != 1 || buys[0] != 1.0 {
		t.Fatalf("buys %v", buys)
	}
	if _, ok := m["sells"].([]any); !ok {
		t.Fatalf("sells not a list under 4KB: %v", m["sells"])
	}
}

// 多轮回：两个窗口各闭一条信号，窗口归属互不影响，按窗口序产出。
func TestClosureMultipleWindowsAttributeIndependently(t *testing.T) {
	facts := &ClosureFacts{
		Signals: []Signal{closureSignal(101, 300), closureSignal(102, 200)},
		Fills: []FillFact{
			closureFill(1, "101", "buy", 300, 10, 5),
			closureFill(2, "", "sell", 300, 11, 5),
			closureFill(3, "102", "buy", 200, 20, 5),
			closureFill(4, "", "sell", 200, 19, 5),
		},
	}
	res, err := PlanClosure(facts)
	if err != nil || len(res.Plans) != 2 {
		t.Fatalf("plans=%v err=%v", res.Plans, err)
	}
	a, b := res.Plans[0], res.Plans[1]
	if a.SignalID != 101 || a.Window != [2]int{1, 2} || a.ExitPrice != 11 || a.PnlPct != 9.6506 {
		t.Fatalf("window1 plan %+v", a)
	}
	if b.SignalID != 102 || b.Window != [2]int{3, 4} || b.ExitPrice != 19 || b.PnlPct != -5.2434 {
		t.Fatalf("window2 plan %+v", b)
	}
	if a.ReplayHead != 4 || b.ReplayHead != 4 {
		t.Fatalf("replay head must witness the ledger tail at decision time: %v %v", a.ReplayHead, b.ReplayHead)
	}
}

// 外来批 FIFO 占位：匿名买入占队头、参与分摊，但永不产出闭仓结果；
// 窗口恒等式：各批次净 pnl 合计 == 账户现金窗口增量。
func TestClosureForeignBatchOccupiesFIFOAndIdentityHolds(t *testing.T) {
	facts := &ClosureFacts{
		Signals: []Signal{closureSignal(101, 100)},
		Fills: []FillFact{
			closureFill(1, "", "buy", 100, 9, 5), // 外来批：signal_id 为空
			closureFill(2, "101", "buy", 100, 10, 5),
			closureFill(3, "", "sell", 150, 11, 5), // 跨批分摊：外来批 100 + 候选 50
			closureFill(4, "", "sell", 50, 12, 5),
		},
	}
	res, err := PlanClosure(facts)
	if err != nil || len(res.Plans) != 1 {
		t.Fatalf("plans=%v err=%v", res.Plans, err)
	}
	p := res.Plans[0]
	// 候选 100 股分两笔卖出摊回：550 + 600（最后一份取余，恒等精确守恒）。
	if p.SellAmount != 1150 || p.ExitPrice != 11.5 || len(p.Sells) != 2 || p.ClosedFillID != 4 {
		t.Fatalf("plan %+v", p)
	}
	if want := round4((1143.3333333333335 - 1005) / 1005 * 100); p.PnlPct != 13.7645 || math.Abs(p.PnlPct-want) > 1e-9 {
		t.Fatalf("pnl %v want %v", p.PnlPct, want)
	}
	// 窗口恒等式：候选净 pnl + 外来批净 pnl == Σ卖(amount−fee) − Σ买(amount+fee)。
	signalNet := (p.SellAmount - p.SellFee) - (p.BuyAmount + p.BuyFee)
	foreignNet := (1100.0 - 5.0*100.0/150.0) - 905.0
	cash := (1650.0 - 5.0 + 600.0 - 5.0) - (900.0 + 5.0 + 1000.0 + 5.0)
	if math.Abs(signalNet+foreignNet-cash) > 1e-6 {
		t.Fatalf("window identity broken: %v + %v != %v", signalNet, foreignNet, cash)
	}
}

// 残余滚窗：尾段未零交叉不产出；补齐卖出后窗口闭合才归属。
func TestClosureResidualRollsIntoNextWindow(t *testing.T) {
	fills := []FillFact{
		closureFill(1, "101", "buy", 100, 10, 5),
		closureFill(2, "", "sell", 60, 11, 3),
	}
	res, err := PlanClosure(&ClosureFacts{Signals: []Signal{closureSignal(101, 100)}, Fills: fills})
	if err != nil || len(res.Plans) != 0 {
		t.Fatalf("incomplete window must not close: plans=%v err=%v", res.Plans, err)
	}
	fills = append(fills, closureFill(3, "", "sell", 40, 12, 3))
	res, err = PlanClosure(&ClosureFacts{Signals: []Signal{closureSignal(101, 100)}, Fills: fills})
	if err != nil || len(res.Plans) != 1 {
		t.Fatalf("completed window must close: plans=%v err=%v", res.Plans, err)
	}
	p := res.Plans[0]
	if p.ExitPrice != 11.4 || p.PnlPct != 12.8358 || p.SellFee != 6 {
		t.Fatalf("plan %+v", p)
	}
}

// 卖超：库存恒等门，loud error，任何信号不迁。
func TestClosureOversoldLedgerIsLoud(t *testing.T) {
	facts := &ClosureFacts{
		Signals: []Signal{closureSignal(101, 100)},
		Fills: []FillFact{
			closureFill(1, "101", "buy", 100, 10, 5),
			closureFill(2, "", "sell", 150, 11, 5),
		},
	}
	if _, err := PlanClosure(facts); err == nil || !strings.Contains(err.Error(), "oversold") {
		t.Fatalf("oversold ledger must fail loud: %v", err)
	}
}

// 四道门矩阵：Gate-B 滞后静默跳过、超配报 Over、Gate-D 跳过、同信号跨窗口逐窗判定。
func TestClosureGatesSkipWithoutFabricating(t *testing.T) {
	single := []FillFact{closureFill(1, "101", "buy", 100, 10, 5), closureFill(2, "", "sell", 100, 11, 5)}

	// Gate-B 投影滞后：Σ名下买入 100 < ExecutedVolume 200，静默跳过（Over 为空）。
	res, err := PlanClosure(&ClosureFacts{Signals: []Signal{closureSignal(101, 200)}, Fills: single})
	if err != nil || len(res.Plans) != 0 || len(res.Over) != 0 {
		t.Fatalf("lagging projection must skip silently: %+v err=%v", res, err)
	}

	// Gate-B 防御分支：Σ名下买入 120 > ExecutedVolume 100，跳过并报超配。
	over := []FillFact{
		closureFill(1, "101", "buy", 60, 10, 3),
		closureFill(2, "101", "buy", 60, 10, 3),
		closureFill(3, "", "sell", 120, 11, 6),
	}
	res, err = PlanClosure(&ClosureFacts{Signals: []Signal{closureSignal(101, 100)}, Fills: over})
	if err != nil || len(res.Plans) != 0 || len(res.Over) != 1 || res.Over[0] != 101 {
		t.Fatalf("over-allocated signal must be reported: %+v err=%v", res, err)
	}

	// Gate-D 残余意图门：存在未决执行意图，本轮跳过，不冷却（core 侧无 pairState 写入来源）。
	res, err = PlanClosure(&ClosureFacts{Signals: []Signal{closureSignal(101, 100)}, Fills: single, PendingIntent: map[int]bool{101: true}})
	if err != nil || len(res.Plans) != 0 {
		t.Fatalf("pending intent must skip this round: %+v err=%v", res, err)
	}

	// 投影事实缺失防御：ExecutedVolume 为空宁缺毋伪造。
	missing := closureSignal(101, 100)
	missing.ExecutedVolume = nil
	res, err = PlanClosure(&ClosureFacts{Signals: []Signal{missing}, Fills: single})
	if err != nil || len(res.Plans) != 0 {
		t.Fatalf("missing executed volume must not fabricate: %+v err=%v", res, err)
	}

	// 同一信号跨两个窗口：逐窗独立判定，产出两条结论（CAS 门保证首条落库、次条冲突跳过）。
	span := []FillFact{
		closureFill(1, "101", "buy", 100, 10, 5),
		closureFill(2, "", "sell", 100, 11, 5),
		closureFill(3, "101", "buy", 200, 20, 5),
		closureFill(4, "", "sell", 200, 19, 5),
	}
	res, err = PlanClosure(&ClosureFacts{Signals: []Signal{closureSignal(101, 300)}, Fills: span})
	if err != nil || len(res.Plans) != 2 || res.Plans[0].Window != [2]int{1, 2} || res.Plans[1].Window != [2]int{3, 4} ||
		res.Plans[0].Qty != 100 || res.Plans[1].Qty != 200 {
		t.Fatalf("same signal across windows must plan per window in order: %+v err=%v", res, err)
	}
}

// 符号按未舍入值判定：未舍入 +0.00004% 舍 4 位归零时，存 +0.0001 保护 win/flat 不翻转。
func TestClosurePnlSignFollowsUnroundedValue(t *testing.T) {
	facts := &ClosureFacts{
		Signals: []Signal{closureSignal(101, 100)},
		Fills: []FillFact{
			closureFill(1, "101", "buy", 100, 10, 0),
			{ID: 2, SignalID: "", Side: "sell", Qty: 100, Price: 10.000004, Amount: 1000.0004, Fee: 0, CreatedAt: closureT0.Add(2 * time.Minute)},
		},
	}
	res, err := PlanClosure(facts)
	if err != nil || len(res.Plans) != 1 || res.Plans[0].PnlPct != 0.0001 {
		t.Fatalf("tiny positive pnl must keep its sign: %+v err=%v", res, err)
	}
	facts.Fills[1].Price, facts.Fills[1].Amount = 9.999996, 999.9996
	res, err = PlanClosure(facts)
	if err != nil || len(res.Plans) != 1 || res.Plans[0].PnlPct != -0.0001 {
		t.Fatalf("tiny negative pnl must keep its sign: %+v err=%v", res, err)
	}
}

// 非有限/脏账本行一律拒（+Inf/NaN/<=0 向量在账本校验显式堵死，planner 不信间接不变量）。
func TestClosureRejectsContradictoryLedger(t *testing.T) {
	good := []FillFact{closureFill(1, "101", "buy", 100, 10, 5), closureFill(2, "", "sell", 100, 11, 5)}
	for _, tc := range []struct {
		name string
		fill FillFact
	}{
		{"nan_price", FillFact{ID: 1, SignalID: "101", Side: "buy", Qty: 100, Price: math.NaN(), Amount: 1000, Fee: 5}},
		{"inf_price", FillFact{ID: 1, SignalID: "101", Side: "buy", Qty: 100, Price: math.Inf(1), Amount: 1000, Fee: 5}},
		{"zero_price", FillFact{ID: 1, SignalID: "101", Side: "buy", Qty: 100, Price: 0, Amount: 0, Fee: 5}},
		{"zero_qty", FillFact{ID: 1, SignalID: "101", Side: "buy", Qty: 0, Price: 10, Amount: 0, Fee: 5}},
		{"bad_amount", FillFact{ID: 1, SignalID: "101", Side: "buy", Qty: 100, Price: 10, Amount: 1001, Fee: 5}},
		{"negative_fee", FillFact{ID: 1, SignalID: "101", Side: "buy", Qty: 100, Price: 10, Amount: 1000, Fee: -1}},
		{"nan_fee", FillFact{ID: 1, SignalID: "101", Side: "buy", Qty: 100, Price: 10, Amount: 1000, Fee: math.NaN()}},
		{"unknown_side", FillFact{ID: 1, SignalID: "101", Side: "hold", Qty: 100, Price: 10, Amount: 1000, Fee: 5}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fills := append([]FillFact{tc.fill}, good[1])
			if _, err := PlanClosure(&ClosureFacts{Signals: []Signal{closureSignal(101, 100)}, Fills: fills}); err == nil {
				t.Fatal("contradictory ledger accepted")
			}
		})
	}
}

// reason 降级保键：fill id 列表超 4KB 时 buys/sells 降级为区间摘要，JSON 键一个不少。
func TestClosureEvidenceDegradesValuesNotKeys(t *testing.T) {
	fills := make([]FillFact, 0, 1200)
	for i := 1; i <= 600; i++ {
		fills = append(fills, closureFill(i, "101", "buy", 1, 10, 0.01))
	}
	for i := 601; i <= 1200; i++ {
		fills = append(fills, closureFill(i, "", "sell", 1, 11, 0.01))
	}
	res, err := PlanClosure(&ClosureFacts{Signals: []Signal{closureSignal(101, 600)}, Fills: fills})
	if err != nil || len(res.Plans) != 1 {
		t.Fatalf("plans=%v err=%v", res.Plans, err)
	}
	p := res.Plans[0]
	if p.ExitPrice != 11 || len(p.Buys) != 600 || len(p.Sells) != 600 {
		t.Fatalf("plan %+v", p)
	}
	m := closureReasonJSON(t, p.Reason) // 键集合在此断言：一个不少
	buys, ok := m["buys"].(map[string]any)
	if !ok || buys["count"] != 600.0 || buys["first"] != 1.0 || buys["last"] != 600.0 {
		t.Fatalf("degraded buys %v", m["buys"])
	}
	sells, ok := m["sells"].(map[string]any)
	if !ok || sells["count"] != 600.0 || sells["first"] != 601.0 || sells["last"] != 1200.0 {
		t.Fatalf("degraded sells %v", m["sells"])
	}
	if m["signal_id"] != 101.0 || m["closed_fill_id"] != 1200.0 || m["replay_head"] != 1200.0 {
		t.Fatalf("degraded evidence loses payload: %v", m)
	}
}
