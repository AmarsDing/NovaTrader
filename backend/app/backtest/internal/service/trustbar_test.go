package service

import (
	"testing"
	"time"

	"server/app/backtest/internal/biz"
)

// W8a 契约：Task 零值 TrustBar 透传后全部判据位=未验证/未声明（R-UI-20/23）。
// 前端拿零值渲染「已验证」即破契约；该测试钉死缺省语义，判据计算（W8c）不得改写零值含义。
func TestToTaskZeroTrustBarMeansUnverified(t *testing.T) {
	out, err := toTask(&biz.Task{
		ID: 1, Kind: "backtest", Name: "t", Strategy: "s", Freq: "1d",
		Start:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		End:    time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		Status: "succeeded",
	})
	if err != nil {
		t.Fatal(err)
	}
	tb := out.GetTrustBar()
	if tb == nil {
		t.Fatal("trust_bar 必须恒下发（前端按零值显「未验证」，不得整段缺省另设分支）")
	}
	if tb.GetBlindVerified() || tb.GetPboVerified() {
		t.Error("零值不得透成已验证")
	}
	if tb.GetBarsTotal() != 0 || tb.GetTradeCount() != 0 || tb.GetTradingDays() != 0 {
		t.Error("未统计样本量必须保 0（没取到≠0）")
	}
	if tb.GetWindowStart() != "" || tb.GetWindowEnd() != "" || tb.GetCostModel() != "" ||
		tb.GetDatasetManifestId() != "" || tb.GetDatasetHash() != "" {
		t.Error("未声明字段必须保空串")
	}
	if tb.GetFeeBps() != 0 || tb.GetSlippageBps() != 0 {
		t.Error("未声明成本必须保 0，不回填")
	}
}

// 判据位非零时逐字段透传（防接线漂移）。
func TestToTaskTrustBarFieldsPassthrough(t *testing.T) {
	out, err := toTask(&biz.Task{
		ID: 1, Kind: "backtest", Name: "t", Strategy: "s", Freq: "1d",
		Start:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		End:    time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		Status: "succeeded",
		TrustBar: biz.TrustBar{
			BarsTotal: 120, TradingDays: 100, TradeCount: 42,
			WindowStart: "2026-01-01", WindowEnd: "2026-06-01",
			CostModel: "fee+slippage", FeeBps: 2.5, SlippageBps: 10,
			BlindVerified: true, PboVerified: false,
			DatasetManifestID: "m-1", DatasetHash: "h-1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	tb := out.GetTrustBar()
	if !tb.GetBlindVerified() || tb.GetPboVerified() {
		t.Error("blind/pbo 判据位透传漂移")
	}
	if tb.GetBarsTotal() != 120 || tb.GetTradingDays() != 100 || tb.GetTradeCount() != 42 {
		t.Error("样本量透传漂移")
	}
	if tb.GetWindowStart() != "2026-01-01" || tb.GetWindowEnd() != "2026-06-01" ||
		tb.GetCostModel() != "fee+slippage" || tb.GetFeeBps() != 2.5 || tb.GetSlippageBps() != 10 ||
		tb.GetDatasetManifestId() != "m-1" || tb.GetDatasetHash() != "h-1" {
		t.Errorf("字段透传漂移：%+v", tb)
	}
}
