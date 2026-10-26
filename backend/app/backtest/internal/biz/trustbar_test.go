package biz

import (
	"math"
	"testing"
	"time"

	"server/pkg/backtest"
)

// W8c 判据口径钉死（R-UI-20/23 配套）：blind 四条同立；pbo 三形态分流——
// optimize/evolve 走 CSCV 候选×段矩阵（Valid-only 池/段长对齐/严格 < 0.5 阈值/
// objective≠sharpe 不冒名认证保未验证），walkforward 按多窗失效率；无成交窗不作
// 分位样本（沿用 48db0d2 分位成功判据语义）、零值=未验证。

func trustTask() *Task {
	return &Task{
		ID:     1,
		Kind:   KindSingle,
		Status: StatusSucceeded,
		Start:  time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC),
		End:    time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		Spec:   Spec{ManifestID: "m-1", ManifestHash: "h-1"},
	}
}

func trustReport() *backtest.Report {
	pass := true
	return &backtest.Report{
		Kind:              KindSingle,
		DatasetManifestID: "m-1",
		DatasetManifestHash: "h-1",
		Metrics:           backtest.Metrics{Rounds: 42, Sharpe: 1.2},
		Lookahead:         &backtest.LookaheadResult{Pass: pass},
		Assumptions:       []string{"佣金 0.025%", "滑点 1 tick"},
		Config: backtest.Config{Fee: backtest.FeeConfig{
			CommissionRate: 0.00025, // 2.5 bps
		}},
	}
}

func trustSettings() Settings { return Settings{MinTrades: 30} }

func TestComputeTrustBarBlindPassAndDescriptions(t *testing.T) {
	tb := computeTrustBar(trustTask(), trustReport(), trustSettings())
	if !tb.BlindVerified {
		t.Fatal("四条同立应通过盲测判据")
	}
	if tb.PboVerified {
		t.Fatal("单点任务无多窗样本，pbo 必须保未验证")
	}
	if tb.TradeCount != 42 {
		t.Errorf("trade_count=%d want 42", tb.TradeCount)
	}
	if tb.WindowStart != "2026-01-05" || tb.WindowEnd != "2026-06-30" {
		t.Errorf("窗口声明漂移: %s ~ %s", tb.WindowStart, tb.WindowEnd)
	}
	if tb.FeeBps != 2.5 {
		t.Errorf("fee_bps=%v want 2.5", tb.FeeBps)
	}
	if tb.CostModel == "" || tb.DatasetManifestID != "m-1" || tb.DatasetHash != "h-1" {
		t.Errorf("描述字段缺失: %+v", tb)
	}
	// 报告缺 manifest 时回落任务 Spec 声明。
	r := trustReport()
	r.DatasetManifestID, r.DatasetManifestHash = "", ""
	tb = computeTrustBar(trustTask(), r, trustSettings())
	if tb.DatasetManifestID != "m-1" || tb.DatasetHash != "" {
		t.Errorf("manifest 回落漂移: %+v", tb)
	}
}

func TestComputeTrustBarBlindClauses(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*backtest.Report, *Task)
	}{
		{"前瞻自检未运行", func(r *backtest.Report, _ *Task) { r.Lookahead = nil }},
		{"前瞻自检未通过", func(r *backtest.Report, _ *Task) { r.Lookahead = &backtest.LookaheadResult{Pass: false} }},
		{"样本外衰减", func(r *backtest.Report, _ *Task) { r.Optimize = &backtest.OptimizeResult{Flags: []string{flagOOSDecay}} }},
		{"成交笔数不足", func(r *backtest.Report, _ *Task) { r.Metrics.Rounds = 29 }},
		{"未冻结数据集", func(_ *backtest.Report, tk *Task) { tk.Spec.ManifestID = " " }},
	}
	for _, c := range cases {
		tk, r := trustTask(), trustReport()
		c.mut(r, tk)
		if tb := computeTrustBar(tk, r, trustSettings()); tb.BlindVerified {
			t.Errorf("%s：判据仍通过", c.name)
		}
	}
	// 成交笔数恰达下限应通过。
	r := trustReport()
	r.Metrics.Rounds = 30
	if tb := computeTrustBar(trustTask(), r, trustSettings()); !tb.BlindVerified {
		t.Error("笔数恰达下限应通过")
	}
}

func wfWindow(sharpe float64, rounds int) backtest.Window {
	return backtest.Window{Test: backtest.Metrics{Sharpe: sharpe, Rounds: rounds}}
}

func TestComputeTrustBarPboWalkForward(t *testing.T) {
	// 5 窗：1 窗无成交（不作样本）→ 有效 4；1/4 失效 < 50% → 通过。
	r := trustReport()
	r.WalkForward = &backtest.WalkForwardResult{Windows: []backtest.Window{
		wfWindow(0.5, 3), wfWindow(-0.2, 3), wfWindow(0.8, 0), wfWindow(0.1, 2), wfWindow(0.4, 2),
	}}
	tb := computeTrustBar(trustTask(), r, trustSettings())
	if !tb.PboVerified {
		t.Error("失效率 1/4 应通过 pbo 判据")
	}
	// 2/4 失效 → 不通过。
	r.WalkForward.Windows[0] = wfWindow(-0.5, 3)
	if tb = computeTrustBar(trustTask(), r, trustSettings()); tb.PboVerified {
		t.Error("失效率 2/4 不应通过")
	}
	// 有效窗不足 PboMinWindows → 未验证（不拿少量样本冒充分位读数）。
	r.WalkForward = &backtest.WalkForwardResult{Windows: []backtest.Window{
		wfWindow(0.5, 3), wfWindow(0.2, 3), wfWindow(0.8, 0),
	}}
	if tb = computeTrustBar(trustTask(), r, trustSettings()); tb.PboVerified {
		t.Error("有效窗 2 窗必须保未验证")
	}
}

// --- W8c 接线：pbo 判据分流（optimize/evolve → CSCV 候选×段矩阵） ---
//
// CSCV 读数钉在 pkg/backtest（pbo_test.go 手追全组合）；本层只钉判据口径：
// 有效候选池、段长对齐、不足/不可算一律未验证、存量旧报告不回填。

func optPoints(rows ...[]float64) []backtest.Point {
	pts := make([]backtest.Point, len(rows))
	for i, row := range rows {
		pts[i] = backtest.Point{Valid: true, Objective: 1, Segments: row}
	}
	return pts
}

func TestComputeTrustBarPboOptimizeCSCV(t *testing.T) {
	// 全段一致（IS 最优恒为 OOS 最优）→ PBO=0 → 判据通过。
	r := trustReport()
	r.Optimize = &backtest.OptimizeResult{Points: optPoints(
		[]float64{3, 3, 3, 3}, []float64{2, 2, 2, 2}, []float64{1, 1, 1, 1},
	)}
	if tb := computeTrustBar(trustTask(), r, trustSettings()); !tb.PboVerified {
		t.Error("一致矩阵 PBO=0 应通过 pbo 判据")
	}
	// IS/OOS 镜像对抗（最优纯靠运气）→ PBO=1 → 不通过。
	r.Optimize.Points = optPoints(
		[]float64{1, 1, -1, -1}, []float64{-1, -1, 1, 1},
	)
	if tb := computeTrustBar(trustTask(), r, trustSettings()); tb.PboVerified {
		t.Error("镜像对抗矩阵 PBO=1 不得通过")
	}
}

func TestComputeTrustBarPboOptimizeDegraded(t *testing.T) {
	// 判据池只收有效候选：单有效 → CSCV 不足 → 未验证（不拿孤本冒充读数）。
	r := trustReport()
	pts := optPoints([]float64{3, 3, 3, 3}, []float64{2, 2, 2, 2})
	pts[1].Valid = false
	r.Optimize = &backtest.OptimizeResult{Points: pts}
	if tb := computeTrustBar(trustTask(), r, trustSettings()); tb.PboVerified {
		t.Error("有效候选不足必须保未验证")
	}
	// 段长不齐（6 vs 4）：以首个有效带段行为基准，不齐行剔除后不足 → 未验证，
	// 不截断统计硬对齐。
	r.Optimize.Points = optPoints(
		[]float64{3, 3, 3, 3, 3, 3}, []float64{2, 2, 2, 2}, []float64{1, 1, 1, 1},
	)
	if tb := computeTrustBar(trustTask(), r, trustSettings()); tb.PboVerified {
		t.Error("段长不齐剔除后不足必须保未验证")
	}
	// 生产主路 evaluate 已过 finiteOrNil 消毒（NaN 段链落库前置 nil），此处钉
	// 判据层防御：即使 NaN 绕过消毒到达判据，CSCV 整行剔除后不足 → 未验证
	//（没取到≠0，不补 0 参与秩比较）。
	nanRow := []float64{2, 2, 2, 2}
	nanRow[1] = math.NaN()
	r.Optimize.Points = optPoints([]float64{3, 3, 3, 3}, nanRow)
	if tb := computeTrustBar(trustTask(), r, trustSettings()); tb.PboVerified {
		t.Error("NaN 行剔除后不足必须保未验证")
	}
	// 存量旧报告未带段指标 → 判据位保未验证，不回填不伪造。
	r.Optimize.Points = []backtest.Point{
		{Valid: true, Objective: 1}, {Valid: true, Objective: 0.9},
	}
	if tb := computeTrustBar(trustTask(), r, trustSettings()); tb.PboVerified {
		t.Error("无段旧报告必须保未验证")
	}
}

func TestComputeTrustBarPboEvolveCSCV(t *testing.T) {
	// evolve 与 optimize 同源接线：候选×段矩阵过 CSCV。
	r := trustReport()
	r.Evolve = &backtest.EvolveResult{Points: optPoints(
		[]float64{3, 3, 3, 3}, []float64{2, 2, 2, 2}, []float64{1, 1, 1, 1},
	)}
	if tb := computeTrustBar(trustTask(), r, trustSettings()); !tb.PboVerified {
		t.Error("evolve 一致矩阵 PBO=0 应通过 pbo 判据")
	}
	r.Evolve.Points = optPoints([]float64{1, 1, -1, -1}, []float64{-1, -1, 1, 1})
	if tb := computeTrustBar(trustTask(), r, trustSettings()); tb.PboVerified {
		t.Error("evolve 镜像矩阵 PBO=1 不得通过")
	}
}

func TestComputeTrustBarPboObjectiveGate(t *testing.T) {
	// 矩阵按段 Sharpe 排名：任务用 annual/calmar 做出的选择与矩阵度量错位，
	// 判据不冒名认证 → 未验证；sharpe 与空（旧报告默认口径）放行。
	consistent := optPoints([]float64{3, 3, 3, 3}, []float64{2, 2, 2, 2}, []float64{1, 1, 1, 1})
	r := trustReport()
	r.Optimize = &backtest.OptimizeResult{Objective: "annual", Points: consistent}
	if tb := computeTrustBar(trustTask(), r, trustSettings()); tb.PboVerified {
		t.Error("annual 目标不得用 Sharpe 矩阵冒名认证")
	}
	r.Optimize.Objective = "sharpe"
	if tb := computeTrustBar(trustTask(), r, trustSettings()); !tb.PboVerified {
		t.Error("sharpe 目标与矩阵同度量应通过")
	}
	r.Optimize.Objective = "" // 存量旧报告无键：按 sharpe 默认口径放行
	if tb := computeTrustBar(trustTask(), r, trustSettings()); !tb.PboVerified {
		t.Error("空目标（旧报告默认口径）应按 sharpe 放行")
	}
	r2 := trustReport()
	r2.Evolve = &backtest.EvolveResult{Objective: "calmar", Points: consistent}
	if tb := computeTrustBar(trustTask(), r2, trustSettings()); tb.PboVerified {
		t.Error("evolve calmar 同样不得冒名认证")
	}
}

// 阈值与计数口径手追（λ≤0 计入 overfit、严格 < 0.5 才通过、argmax 并列取最先——
// 三者都是翻红锚点，pbo 实现口径漂移会在此暴露）。
func TestComputeTrustBarPboBoundary(t *testing.T) {
	// PBO=2/6=1/3：IS={0,1} 趟 A 取 IS 最优但 OOS 垫底（λ<0）；IS={2,3} 趟 B 取
	// IS 最优、OOS 秩 2/3 → ω=0.5 恰 λ=0（Bailey 口径 λ≤0 计入）；其余四趟 B
	// IS/OOS 双优。1/3 < 0.5 → 通过。
	r := trustReport()
	r.Optimize = &backtest.OptimizeResult{Points: optPoints(
		[]float64{3, 3, 0, 0}, []float64{2, 2, 2, 2}, []float64{1, 1, 1, 1},
	)}
	if tb := computeTrustBar(trustTask(), r, trustSettings()); !tb.PboVerified {
		t.Error("PBO=1/3 应通过判据")
	}
	// PBO=3/6=0.5 恰在线：overfit 于 IS={0,1}/{0,3}/{2,3}；IS={0,2}/{1,2} 趟
	// A/B IS 并列取最先（A 胜）是手追锚点。严格 < 阈值 → 不通过。
	r.Optimize.Points = optPoints(
		[]float64{10, 10, 0, 0.5}, []float64{0, 0, 10, 10},
		[]float64{4, 4, 4, 4}, []float64{5.5, 0, 0, 5.5},
	)
	if tb := computeTrustBar(trustTask(), r, trustSettings()); tb.PboVerified {
		t.Error("PBO=0.5 恰在阈值不得通过（严格小于）")
	}
}

func TestComputeTrustBarPboShapeGuards(t *testing.T) {
	r := trustReport()
	// 段数 <4：定档未启用/存量数据——不送 CSCV，直接未验证。
	r.Optimize = &backtest.OptimizeResult{Points: optPoints(
		[]float64{3, 3, 3}, []float64{2, 2, 2},
	)}
	if tb := computeTrustBar(trustTask(), r, trustSettings()); tb.PboVerified {
		t.Error("段数 <4 必须保未验证")
	}
	// 奇数段：CSCV 形态非法（ErrPBOInvalid）→ 不可算即未验证，不假绿。
	r.Optimize.Points = optPoints(
		[]float64{3, 3, 3, 3, 3}, []float64{2, 2, 2, 2, 2},
	)
	if tb := computeTrustBar(trustTask(), r, trustSettings()); tb.PboVerified {
		t.Error("奇数段 CSCV 不可算必须保未验证")
	}
}

func TestComputeTrustBarPboOptimizePrecedence(t *testing.T) {
	// 一份报告只挂一类搜索结果（分支互斥）；防御钉死：双挂时按 optimize 的
	// CSCV 口径判定，不被 walkforward 失效率翻盘。
	r := trustReport()
	r.Optimize = &backtest.OptimizeResult{Points: optPoints(
		[]float64{3, 3, 3, 3}, []float64{2, 2, 2, 2}, []float64{1, 1, 1, 1},
	)}
	r.WalkForward = &backtest.WalkForwardResult{Windows: []backtest.Window{
		wfWindow(-0.5, 3), wfWindow(-0.2, 3), wfWindow(0.8, 3), wfWindow(0.1, 3),
	}}
	if tb := computeTrustBar(trustTask(), r, trustSettings()); !tb.PboVerified {
		t.Error("双挂报告应按 optimize CSCV 口径判定（PBO=0 应通过）")
	}
}

func TestComputeTrustBarZeroReportUnverified(t *testing.T) {
	tb := computeTrustBar(trustTask(), &backtest.Report{}, trustSettings())
	if tb.BlindVerified || tb.PboVerified {
		t.Error("空报告必须双未验证")
	}
	if tb.TradeCount != 0 || tb.FeeBps != 0 || tb.CostModel != "" {
		t.Errorf("空报告不得回填描述字段: %+v", tb)
	}
	if tb.WindowStart == "" || tb.WindowEnd == "" {
		t.Error("窗口日期来自任务，空报告也应声明")
	}
}
