package backtest

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

// W8c 接线口径钉死：CSCVSegments 阶梯定档、segmentSharpe 分段 Sharpe 手追、
// 数据纪律（非正净值/非有限收益/零波动与尘埃段 → 该段 NaN，整链置 nil 不落 JSON）、
// Point.Segments 的 JSON 兼容（omitempty：旧报告无键不伪造）。审查批 wf_9932525c
// 销号锚：NaN 毒化 marshal、binom 错量闸、尘埃段劫持、注释公式漂移。

// eqFromReturns 按日收益序列折出净值曲线（测试构造；首日基线 initial）。
func eqFromReturns(initial float64, rs []float64) []EquityPoint {
	eq := make([]EquityPoint, len(rs))
	prev := initial
	for i, r := range rs {
		prev *= 1 + r
		eq[i] = EquityPoint{Day: time.Date(2026, 1, 5+i, 0, 0, 0, 0, time.UTC), Equity: prev}
	}
	return eq
}

// CSCVSegments 是唯一硬约束 isDays >= 2*S 的阶梯（CSCV 枚举量只取决于段数，
// 与候选数无关——审查批勘正 binom(候选数,S/2) 错量闸后候选数不再入参）。
func TestCSCVSegmentsLadder(t *testing.T) {
	cases := []struct {
		name         string
		isDays       int
		wantSegments int
	}{
		{"大样本吃满S8", 100, 8},
		{"S8边界恰收", 16, 8},
		{"S8落选S6接住", 15, 6},
		{"S6边界恰收", 12, 6},
		{"S6落选S4接住", 11, 4},
		{"S4边界恰收", 8, 4},
		{"装不下不定档", 7, 0},
		{"零日不定档", 0, 0},
	}
	for _, c := range cases {
		if got := CSCVSegments(c.isDays); got != c.wantSegments {
			t.Errorf("%s：CSCVSegments(%d)=%d want %d", c.name, c.isDays, got, c.wantSegments)
		}
	}
}

// 手工追踪：收益 [.01,.03,0,.02,−.01,.01,.02,0] 等宽 4 段——
// 段0 { .01,.03 }→均.02/标差.01=2；段1 {0,.02}→1；段2 {−.01,.01}→0；段3 {.02,0}→1。
func TestSegmentSharpeHandTraced(t *testing.T) {
	got := segmentSharpe(eqFromReturns(100, []float64{0.01, 0.03, 0, 0.02, -0.01, 0.01, 0.02, 0}), 100, 4, 0)
	want := []float64{2, 1, 0, 1}
	if len(got) != len(want) {
		t.Fatalf("len=%d want %d", len(got), len(want))
	}
	for j := range want {
		if math.Abs(got[j]-want[j]) > 1e-9 {
			t.Fatalf("段%d=%v want %v（全量 %v）", j, got[j], want[j], got)
		}
	}
	// 确定性：纯算术，同输入逐位相等。
	again := segmentSharpe(eqFromReturns(100, []float64{0.01, 0.03, 0, 0.02, -0.01, 0.01, 0.02, 0}), 100, 4, 0)
	for j := range got {
		if got[j] != again[j] {
			t.Fatalf("不确定：段%d %v vs %v", j, got[j], again[j])
		}
	}
}

// 风险口径与 Compute 对齐：扣 riskFree/252 的日超额收益再算 mean/sd。
// rf=0.0252 → 日扣 1e-4：段0 e=[.0099,.0299]→均.0199/标差.01≈1.99；
// 段1 r=[0,.02]→e=[−.0001,.0199]→均.0099≈.99；段2 e=[−.0101,.0099]→均−.0001≈−.01；
// 段3 e=[.0199,−.0001]→.99。
func TestSegmentSharpeRiskFreeAligned(t *testing.T) {
	got := segmentSharpe(eqFromReturns(100, []float64{0.01, 0.03, 0, 0.02, -0.01, 0.01, 0.02, 0}), 100, 4, 0.0252)
	want := []float64{1.99, 0.99, -0.01, 0.99}
	for j := range want {
		if math.Abs(got[j]-want[j]) > 1e-3 {
			t.Fatalf("段%d=%v want≈%v（全量 %v）", j, got[j], want[j], got)
		}
	}
}

// 段1 两日收益全 0 → 零波动记 NaN（零波动段读不出离散度，不硬解 0 冒充满秩）。
func TestSegmentSharpeFlatBucketNaN(t *testing.T) {
	got := segmentSharpe(eqFromReturns(100, []float64{0.01, 0.02, 0, 0, 0.01, -0.01, 0.005, 0.015}), 100, 4, 0)
	if len(got) != 4 {
		t.Fatalf("len=%d want 4", len(got))
	}
	if !math.IsNaN(got[1]) {
		t.Errorf("平段应 NaN，得 %v", got[1])
	}
	for _, j := range []int{0, 2, 3} {
		if math.IsNaN(got[j]) || math.IsInf(got[j], 0) {
			t.Errorf("段%d 应有限，得 %v", j, got[j])
		}
	}
}

// 近退化段同纪律：tick 级尘埃（sd≈5e-9）若放行会被 mean/sd 无界放大成离群
// Sharpe 劫持 CSCV 秩比较——噪声地板 1e-6 以下一律 NaN（审查批销号）。
func TestSegmentSharpeDustBucketNaN(t *testing.T) {
	got := segmentSharpe(eqFromReturns(100, []float64{1e-8, 2e-8, 0.01, -0.005, 0.02, 0.03, -0.01, 0.005}), 100, 4, 0)
	if !math.IsNaN(got[0]) {
		t.Errorf("尘埃段应 NaN（sd≈5e-9 < 1e-6），得 %v", got[0])
	}
	for _, j := range []int{1, 2, 3} {
		if math.IsNaN(got[j]) || math.IsInf(got[j], 0) {
			t.Errorf("段%d 应有限，得 %v", j, got[j])
		}
	}
}

func TestSegmentSharpeGuards(t *testing.T) {
	eq := eqFromReturns(100, []float64{0.01, 0.02, 0.01, 0.01, 0.01, 0.01, 0.01, 0.01})
	for _, c := range []struct {
		name    string
		initial float64
		seg     int
	}{
		{"奇数段", 100, 3},
		{"段数不足4", 100, 2},
		{"零段", 100, 0},
		{"本金非正", 0, 4},
		{"本金为负", -1, 4},
	} {
		if got := segmentSharpe(eq, c.initial, c.seg, 0); got != nil {
			t.Errorf("%s：应拒绝，得 %v", c.name, got)
		}
	}
	// 曲线装不下每段两日（6 段需 12 日）。
	if got := segmentSharpe(eq, 100, 6, 0); got != nil {
		t.Errorf("len 8 < 6*2 应拒绝，得 %v", got)
	}
	// 恰达边界 len == seg*2 应收。
	if got := segmentSharpe(eq, 100, 4, 0); got == nil {
		t.Error("len == seg*2 应出数")
	}
}

// 非正净值纪律：爆仓位（净值归零）所在段整段 NaN 剔除，不拿半截段参与秩比较。
// 收益特意取参差值（全等收益零波动必 NaN，见上一用例，那是另一族纪律）。
func TestSegmentSharpeNonPositiveEquity(t *testing.T) {
	rs := []float64{0.01, 0.03, 0.02, -0.01, 0.015, 0.005, 0.02, -0.005}
	eq := eqFromReturns(100, rs)
	eq[3].Equity = 0 // 第 4 日清零 → i=4 的 prev 为 0 → 段2（i∈[4,6)）整段不可算
	got := segmentSharpe(eq, 100, 4, 0)
	if !math.IsNaN(got[2]) {
		t.Errorf("清零后段应 NaN，得 %v", got[2])
	}
	for _, j := range []int{0, 1, 3} {
		if math.IsNaN(got[j]) {
			t.Errorf("段%d 不应受牵连，得 NaN", j)
		}
	}
	// 净值为负同纪律（prev≤0 整段 NaN）。
	eq2 := eqFromReturns(100, rs)
	eq2[5].Equity = -3
	got2 := segmentSharpe(eq2, 100, 4, 0)
	if !math.IsNaN(got2[3]) {
		t.Errorf("负净值后段应 NaN，得 %v", got2[3])
	}
}

// 落库消毒（高危销号）：空仓平线候选的段链全 NaN → finiteOrNil 整条置 nil；
// 钉死 NaN 必顶穿 encoding/json 的毒化面，并验证消毒后报告可正常 marshal。
func TestSegmentsMarshalSanitize(t *testing.T) {
	flat := segmentSharpe(eqFromReturns(100, make([]float64, 16)), 100, 4, 0)
	if flat == nil {
		t.Fatal("形状合法的平线应给全 NaN 段链而非 nil")
	}
	rep := &Report{Optimize: &OptimizeResult{Points: []Point{{Valid: true, Segments: flat}}}}
	if _, err := json.Marshal(rep); err == nil {
		t.Fatal("NaN 段链必须顶穿 encoding/json——这正是消毒存在的理由")
	}
	rep.Optimize.Points[0].Segments = finiteOrNil(flat)
	if rep.Optimize.Points[0].Segments != nil {
		t.Error("消毒应为整条置 nil（不落键）")
	}
	if _, err := json.Marshal(rep); err != nil {
		t.Fatalf("消毒后必须可落库: %v", err)
	}
	// 全有限原样放行；±Inf 同样整条置 nil。
	ok := []float64{2, 1, 0, 1}
	if got := finiteOrNil(ok); len(got) != 4 {
		t.Errorf("有限段链不得动，得 %v", got)
	}
	if finiteOrNil([]float64{1, math.Inf(1)}) != nil {
		t.Error("+Inf 应整条置 nil")
	}
	if finiteOrNil(nil) != nil {
		t.Error("nil 应原样 nil")
	}
}

// JSON 兼容：Point.Segments 为 nil 时键必须缺席（omitempty）——存量旧报告
// unmarshal 出 nil，判据位据此保未验证；非空时按数组落库。
func TestPointSegmentsJSONContract(t *testing.T) {
	raw, err := json.Marshal(Point{Params: map[string]float64{"a": 1}, Valid: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "segments") {
		t.Errorf("nil 段不得落键: %s", raw)
	}
	raw, err = json.Marshal(Point{Valid: true, Segments: []float64{2, 1, 0, 1}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"segments":[2,1,0,1]`) {
		t.Errorf("段数组应按序落库: %s", raw)
	}
	// 旧报告（无 segments 键）读回 = nil，判据链走「缺段未验证」。
	var p Point
	if err := json.Unmarshal([]byte(`{"params":{"a":1},"valid":true}`), &p); err != nil {
		t.Fatal(err)
	}
	if p.Segments != nil {
		t.Errorf("旧报告段应 nil，得 %v", p.Segments)
	}
}
