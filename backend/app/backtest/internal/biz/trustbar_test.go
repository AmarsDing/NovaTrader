package biz

import (
	"testing"
	"time"

	"server/pkg/backtest"
)

// W8c 判据口径钉死（R-UI-20/23 配套）：blind 四条同立、pbo 多窗失效率、
// 无成交窗不作分位样本（沿用 48db0d2 分位成功判据语义）、零值=未验证。

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
