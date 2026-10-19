package brokerif

import (
	"testing"
	"time"

	"server/pkg/ashare"
	"server/pkg/risk"
	"server/pkg/tradecal"
)

// 2026-10-09 周五 10:00，连续竞价——与 pkg/risk 测试同一时刻口径。
var prevNow = time.Date(2026, 10, 9, 10, 0, 0, 0, tradecal.Shanghai())

// previewBase 是一份 R01–R15 全过的买入上下文（对齐 pkg/risk 的 base 夹具）。
func previewBase() risk.Input {
	q := risk.Quote{
		Symbol: "000001.SZ", Last: 10.00, PrevClose: 10.00, Bid1: 9.99, Ask1: 10.00,
		Amount: 2e8, PrevAmount: 1e8, ListedDays: 1000, Time: prevNow.Add(-time.Second),
	}
	return risk.Input{
		Now:     prevNow,
		Session: tradecal.Session{Phase: tradecal.AMTrading, Trading: true},
		Mode:    risk.L1,
		Account: risk.Account{
			Type: risk.SIM, Equity: 1_000_000, Available: 500_000, DayStartEquity: 1_000_000,
			Holdings: map[string]risk.Holding{}, AsOf: prevNow.Add(-5 * time.Second),
		},
		Quote:  &q,
		Market: risk.Market{Phase: "HOT"},
	}
}

func buyReq(price float64, vol int) PreviewRequest {
	return PreviewRequest{
		Order:   Order{ClientID: "p-1", Symbol: "000001.SZ", Side: Buy, Price: price, Volume: vol},
		Account: risk.SIM, Source: risk.Auto,
	}
}

func TestPreviewApprovedBuy(t *testing.T) {
	p := PreviewOrder(previewBase(), risk.Defaults(), buyReq(10.00, 1000), ashare.DefaultFee())
	if !p.Approved || len(p.Results) != 15 {
		t.Fatalf("want approved with 15 rule rows, got %+v", p)
	}
	if p.Volume != 1000 || p.Adjusted {
		t.Fatalf("no shrink expected: %+v", p)
	}
	// 金额 10000；费用 = max(2.5,5) 佣金 + 0.1 过户 = 5.10；买入现金影响为负。
	if p.Amount != 10000 || p.Fee != 5.10 || p.CashDelta != -10005.10 {
		t.Fatalf("money math: amount=%v fee=%v delta=%v", p.Amount, p.Fee, p.CashDelta)
	}
	if p.BlockRule != "" || p.Reason != "" {
		t.Fatalf("approved preview must not carry block info: %+v", p)
	}
}

func TestPreviewBlockedCarriesRule(t *testing.T) {
	in := previewBase()
	in.KillSwitch = true
	p := PreviewOrder(in, risk.Defaults(), buyReq(10.00, 1000), ashare.DefaultFee())
	if p.Approved || p.BlockRule != "R01" || p.Reason == "" {
		t.Fatalf("want R01 block, got %+v", p)
	}
	if len(p.Results) != 1 || p.Volume != 0 || p.Amount != 0 || p.Fee != 0 || p.CashDelta != 0 {
		t.Fatalf("blocked preview must stop at the first rule and carry no money math: %+v", p)
	}
}

func TestPreviewInvalidIsR00(t *testing.T) {
	req := buyReq(10.00, 1000)
	req.Order.Symbol = "abc"
	p := PreviewOrder(previewBase(), risk.Defaults(), req, ashare.DefaultFee())
	if p.Approved || p.BlockRule != risk.RuleInvalid {
		t.Fatalf("invalid input must surface as R00, got %+v", p)
	}
}

func TestPreviewShrinkedByCash(t *testing.T) {
	in := previewBase()
	in.Account.Available = 5500
	p := PreviewOrder(in, risk.Defaults(), buyReq(10.00, 1000), ashare.DefaultFee())
	if !p.Approved || !p.Adjusted || p.Volume != 500 {
		t.Fatalf("cash shrink should land on 500, got %+v", p)
	}
	// 缩量后按 500 股算账；费用 = max(1.25,5) + 0.05 = 5.05。
	if p.Amount != 5000 || p.Fee != 5.05 || p.CashDelta != -5005.05 {
		t.Fatalf("money math after shrink: %+v", p)
	}
}

func TestPreviewSellCollectsStampTax(t *testing.T) {
	in := previewBase()
	in.Account.Holdings["000001.SZ"] = risk.Holding{Symbol: "000001.SZ", Quantity: 1000, Available: 1000, AvgCost: 9}
	req := buyReq(10.00, 500)
	req.Order.Side = Sell
	p := PreviewOrder(in, risk.Defaults(), req, ashare.DefaultFee())
	if !p.Approved || p.Volume != 500 {
		t.Fatalf("sell should pass whole, got %+v", p)
	}
	// 卖出费用 = 佣金 5 + 印花税 2.5 + 过户 0.05 = 7.55；现金影响为正。
	if p.Amount != 5000 || p.Fee != 7.55 || p.CashDelta != 4992.45 {
		t.Fatalf("sell money math: %+v", p)
	}
}

func TestPreviewEchoesAdapterOrder(t *testing.T) {
	p := PreviewOrder(previewBase(), risk.Defaults(), buyReq(10.00, 1000), ashare.DefaultFee())
	if p.Order.ClientID != "p-1" || p.Order.Side != Buy || p.Order.Price != 10.00 || p.Order.Volume != 1000 {
		t.Fatalf("preview must echo the adapter-side order verbatim: %+v", p.Order)
	}
}

func TestPreviewKeepsAssembledSectorAndR09Shrinks(t *testing.T) {
	// 调用方装配带板块（引擎侧由 ref 表回填）；权益 1e6 × MaxSectorPct 0.30 = 板块限额 30 万。
	in := previewBase()
	in.Account.Holdings["600036.SH"] = risk.Holding{Symbol: "600036.SH", Sector: "银行", Quantity: 10000, Available: 10000, AvgCost: 29.5, MarketValue: 295_000}
	in.Order = risk.Order{Sector: "银行"}
	p := PreviewOrder(in, risk.Defaults(), buyReq(10.00, 1000), ashare.DefaultFee())
	// R09：已用 29.5 万、余量 5 千 → 缩量 1000 → 500。若装配的 Sector 被冲掉，
	// 规则走「没有板块信息，未检查」放行全额 1000——本例钉的就是这个回归。
	if !p.Approved || !p.Adjusted || p.Volume != 500 {
		t.Fatalf("R09 shrink via assembled sector: %+v", p)
	}
	var r09 *risk.Result
	for i := range p.Results {
		if p.Results[i].Rule == "R09" {
			r09 = &p.Results[i]
		}
	}
	if r09 == nil || !r09.Pass || r09.Note == "" {
		t.Fatalf("R09 must pass with a shrink note: %+v", r09)
	}
}

func TestPreviewSectorFullRejectsR09(t *testing.T) {
	// 板块额度用满：已用 30 万 = 上限 → 放不下一手，实检口径的 R09 拒绝必须原样出现在试算。
	in := previewBase()
	in.Account.Holdings["600036.SH"] = risk.Holding{Symbol: "600036.SH", Sector: "银行", Quantity: 10000, Available: 10000, AvgCost: 30, MarketValue: 300_000}
	in.Order = risk.Order{Sector: "银行"}
	p := PreviewOrder(in, risk.Defaults(), buyReq(10.00, 1000), ashare.DefaultFee())
	if p.Approved || p.BlockRule != "R09" || p.Volume != 0 || p.Amount != 0 {
		t.Fatalf("saturated sector must surface the same R09 reject as Check: %+v", p)
	}
}

func TestPreviewGarbageSideIsR00(t *testing.T) {
	// 方向缺省/脏值不得归并为买单：如实透传给 validate，与实检同一 R00 防线。
	for _, side := range []Side{"", "hodor"} {
		req := buyReq(10.00, 1000)
		req.Order.Side = side
		p := PreviewOrder(previewBase(), risk.Defaults(), req, ashare.DefaultFee())
		if p.Approved || p.BlockRule != risk.RuleInvalid {
			t.Fatalf("garbage side %q must surface as R00 like Check, got %+v", side, p)
		}
	}
}
