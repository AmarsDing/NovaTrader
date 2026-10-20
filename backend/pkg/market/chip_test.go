package market

import (
	"math"
	"testing"
	"time"
)

// bar i 天后的生产日期。h/l/c 为前复权价,turn 为换手率百分数。
// 期望值逐用例对齐 research-worker/vibe-astock/calc/tests/test_indicators.py
// test_chip_distribution_hand_cases 的手算结果（approx abs 同值）。
func chipBar(i int, h, l, c, turn float64) ChipBar {
	return ChipBar{
		Day:  time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC).AddDate(0, 0, i),
		High: h, Low: l, Close: c, Turn: turn,
	}
}

func chipOK(t *testing.T, r ChipResult) {
	t.Helper()
	if r.Status != ChipOK {
		t.Fatalf("status = %q (%s), want ok", r.Status, r.Reason)
	}
}

func chipNear(v *float64, want, abs float64) bool {
	return v != nil && math.Abs(*v-want) <= abs
}

func TestChipDistributionHandCases(t *testing.T) {
	// 两天一字板：第 1 天 10 播种全部筹码；第 2 天 12 换手 50% → 一半搬到 12：均成本 11,现价 12 全部获利。
	r1 := ChipDistribution([]ChipBar{chipBar(0, 10, 10, 10, 0), chipBar(1, 12, 12, 12, 50)}, ChipOptions{MinPoints: 2})
	chipOK(t, r1)
	if !chipNear(r1.Profit, 1.0, 1e-9) || !chipNear(r1.AvgCost, 11.0, 0.02) {
		t.Fatalf("case1 profit=%v avg=%v", r1.Profit, r1.AvgCost)
	}
	if r1.Days != 2 || r1.CumTurnoverPct != 50 {
		t.Fatalf("case1 days=%d cum=%v", r1.Days, r1.CumTurnoverPct)
	}

	// 第 2 天跌到 8、换手 50%：一半在 10 一半在 8；现价 8 → 获利 0.5（半步容差）,均成本 9。
	r2 := ChipDistribution([]ChipBar{chipBar(0, 10, 10, 10, 0), chipBar(1, 8, 8, 8, 50)}, ChipOptions{MinPoints: 2})
	chipOK(t, r2)
	if !chipNear(r2.Profit, 0.5, 1e-9) || !chipNear(r2.AvgCost, 9.0, 0.02) {
		t.Fatalf("case2 profit=%v avg=%v", r2.Profit, r2.AvgCost)
	}

	// 三天连续衰减 10→12(50%)→14(50%)：0.25@10 / 0.25@12 / 0.5@14；均成本 12.5,峰在 14,现价 14 全获利。
	r3 := ChipDistribution([]ChipBar{chipBar(0, 10, 10, 10, 0), chipBar(1, 12, 12, 12, 50), chipBar(2, 14, 14, 14, 50)}, ChipOptions{MinPoints: 2})
	chipOK(t, r3)
	if !chipNear(r3.AvgCost, 12.5, 0.03) || !chipNear(r3.Profit, 1.0, 1e-9) || !chipNear(r3.PeakPrice, 14.0, 0.03) {
		t.Fatalf("case3 avg=%v profit=%v peak=%v", r3.AvgCost, r3.Profit, r3.PeakPrice)
	}

	// turn=100 → 全部搬走：均成本 8。
	r4 := ChipDistribution([]ChipBar{chipBar(0, 10, 10, 10, 0), chipBar(1, 8, 8, 8, 100)}, ChipOptions{MinPoints: 2})
	chipOK(t, r4)
	if !chipNear(r4.AvgCost, 8.0, 0.02) {
		t.Fatalf("case4 avg=%v", r4.AvgCost)
	}

	// decay=2 让 60% 换手截断为 100%：均成本 8,全部获利。
	r5 := ChipDistribution([]ChipBar{chipBar(0, 10, 10, 10, 0), chipBar(1, 8, 8, 8, 60)}, ChipOptions{MinPoints: 2, Decay: 2})
	chipOK(t, r5)
	if !chipNear(r5.AvgCost, 8.0, 0.02) || !chipNear(r5.Profit, 1.0, 1e-9) {
		t.Fatalf("case5 avg=%v profit=%v", r5.AvgCost, r5.Profit)
	}

	// 三角分布：单日低 8 高 12 收 10 → 峰在均价 10、均成本≈10、现价 10 获利 ≈0.5（半步容差略 >0.5）。
	r6 := ChipDistribution([]ChipBar{chipBar(0, 12, 8, 10, 0), chipBar(1, 12, 8, 10, 0)}, ChipOptions{MinPoints: 2})
	chipOK(t, r6)
	if !chipNear(r6.AvgCost, 10.0, 0.03) || !chipNear(r6.PeakPrice, 10.0, 0.03) {
		t.Fatalf("case6 avg=%v peak=%v", r6.AvgCost, r6.PeakPrice)
	}
	if r6.Profit == nil || *r6.Profit < 0.5 || *r6.Profit > 0.52 {
		t.Fatalf("case6 profit=%v, want 0.5..0.52", r6.Profit)
	}

	// 窄振幅（窄于网格步长）回退最近格点不丢换手衰减：均成本 ≈ 10.0015。
	r7 := ChipDistribution([]ChipBar{chipBar(0, 20, 5, 10, 0), chipBar(1, 10.002, 10.001, 10.0015, 100)}, ChipOptions{MinPoints: 2})
	chipOK(t, r7)
	if !chipNear(r7.AvgCost, 10.0015, 0.06) {
		t.Fatalf("case7 avg=%v", r7.AvgCost)
	}
}

func TestChipDistributionDomains(t *testing.T) {
	two := []ChipBar{chipBar(0, 10, 10, 10, 0), chipBar(1, 12, 12, 12, 50)}

	// 行数不足默认 MinPoints(20) → not_meaningful,衍生列 nil。
	r := ChipDistribution(two, ChipOptions{})
	if r.Status != ChipNotMeaningful || r.Profit != nil || r.AvgCost != nil {
		t.Fatalf("short=%+v", r)
	}
	if r.Days != 2 || r.CumTurnoverPct != 50 {
		t.Fatalf("short facts days=%d cum=%v", r.Days, r.CumTurnoverPct)
	}

	bad := []struct {
		name string
		bars []ChipBar
		opts ChipOptions
	}{
		{"grid 越域", two, ChipOptions{MinPoints: 2, GridSize: 10}},
		{"decay 越域", two, ChipOptions{MinPoints: 2, Decay: -1}},
		{"turn 超 100", []ChipBar{chipBar(0, 10, 10, 10, 150), chipBar(1, 10, 10, 10, 1)}, ChipOptions{MinPoints: 2}},
		{"负价", []ChipBar{chipBar(0, -1, -1, -1, 1), chipBar(1, 1, 1, 1, 1)}, ChipOptions{MinPoints: 2}},
		{"high<low", []ChipBar{chipBar(0, 9, 10, 9.5, 1), chipBar(1, 10, 10, 10, 1)}, ChipOptions{MinPoints: 2}},
		{"close 出界", []ChipBar{chipBar(0, 10, 9, 11, 1), chipBar(1, 10, 10, 10, 1)}, ChipOptions{MinPoints: 2}},
		{"重复日期", []ChipBar{chipBar(0, 10, 10, 10, 1), chipBar(0, 10, 10, 10, 1)}, ChipOptions{MinPoints: 2}},
		{"NaN 价", []ChipBar{chipBar(0, math.NaN(), 10, 10, 1), chipBar(1, 10, 10, 10, 1)}, ChipOptions{MinPoints: 2}},
	}
	for _, tc := range bad {
		r := ChipDistribution(tc.bars, tc.opts)
		if r.Status != ChipError || r.Profit != nil {
			t.Errorf("%s: status=%q profit=%v, want error+nil", tc.name, r.Status, r.Profit)
		}
	}
}

// 乱序输入内部升序：同一分布两种顺序结果一致。
func TestChipDistributionSortsInput(t *testing.T) {
	a := []ChipBar{chipBar(0, 10, 10, 10, 0), chipBar(1, 12, 12, 12, 50), chipBar(2, 14, 14, 14, 50)}
	b := []ChipBar{a[2], a[0], a[1]}
	ra := ChipDistribution(a, ChipOptions{MinPoints: 2})
	rb := ChipDistribution(b, ChipOptions{MinPoints: 2})
	chipOK(t, ra)
	chipOK(t, rb)
	if !chipNear(ra.AvgCost, 12.5, 0.03) || !chipNear(rb.AvgCost, 12.5, 0.03) {
		t.Fatalf("avg a=%v b=%v", *ra.AvgCost, *rb.AvgCost)
	}
	if rb.WindowEnd != ra.WindowEnd || rb.WindowStart != ra.WindowStart {
		t.Fatalf("window mismatch: a %v..%v b %v..%v", ra.WindowStart, ra.WindowEnd, rb.WindowStart, rb.WindowEnd)
	}
}
