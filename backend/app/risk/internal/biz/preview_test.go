package biz

import (
	"context"
	"testing"

	"server/pkg/brokerif"
	"server/pkg/risk"

	"github.com/go-kratos/kratos/v2/log"
)

func TestPreviewApprovedBuy(t *testing.T) {
	r := newRig(t, nil)
	r.account(1_000_000, 500_000)
	pv := r.e.Preview(context.Background(), buy("pv", 1000))
	if !pv.Approved || pv.Volume != 1000 || pv.Adjusted || len(pv.Results) != 15 {
		t.Fatalf("preview should approve whole: %+v", pv)
	}
	// 与 pkg 切片同一精度口径：金额 10000，费用 5.10，买入现金影响为负。
	if pv.Amount != 10000 || pv.Fee != 5.10 || pv.CashDelta != -10005.10 {
		t.Fatalf("money math: %+v", pv)
	}
	if pv.Order.ClientID != "pv" || pv.Order.Side != brokerif.Buy || pv.Order.Symbol != "000001.SZ" {
		t.Fatalf("echo: %+v", pv.Order)
	}
}

func TestPreviewLeavesNoTrace(t *testing.T) {
	r := newRig(t, nil)
	r.account(1_000_000, 15_000)
	ctx := context.Background()
	// 先干跑，再真下同额单：真实单拿到全额，证明预览没占预约、没计速度/笔数、没进审计。
	pv := r.e.Preview(ctx, buy("pv", 1000))
	if !pv.Approved || pv.Volume != 1000 {
		t.Fatalf("preview: %+v", pv)
	}
	if r.audit.n != 0 {
		t.Fatal("preview must not be audited")
	}
	if r.e.State().BuysToday != 0 {
		t.Fatal("preview must not count as a buy")
	}
	d, _ := r.e.Check(ctx, buy("b", 1000))
	if !d.Approved || d.Volume != 1000 || d.Adjusted {
		t.Fatalf("a real order after a same-size preview must still get the full lot: %+v", d)
	}
	if r.audit.n != 1 || r.e.State().BuysToday != 1 {
		t.Fatalf("only the real check leaves a trace: audit=%d buys=%d", r.audit.n, r.e.State().BuysToday)
	}
	// 同一账户第二笔真单只剩 400——预约来自真实单，不是预览。
	if d2, _ := r.e.Check(ctx, buy("c", 1000)); d2.Volume != 400 {
		t.Fatalf("reservation must come from real checks only: %+v", d2)
	}
}

func TestPreviewKillSwitchBlocks(t *testing.T) {
	r := newRig(t, nil)
	r.account(1_000_000, 500_000)
	if _, err := r.e.TriggerKill(context.Background(), risk.KillFromTerminal, "测试", ""); err != nil {
		t.Fatal(err)
	}
	pv := r.e.Preview(context.Background(), buy("pv", 100))
	if pv.Approved || pv.BlockRule != "R01" || len(pv.Results) != 1 {
		t.Fatalf("kill switch must surface in preview like in check: %+v", pv)
	}
}

func TestPreviewNotReadyBlocks(t *testing.T) {
	repo := &fakeRepo{}
	e := NewEngine(repo, &fakeQuotes{m: map[string]Snapshot{}}, &fakeAudit{}, &fakePub{}, log.DefaultLogger)
	pv := e.Preview(context.Background(), buy("pv", 100))
	if pv.Approved || pv.BlockRule != risk.RuleInvalid {
		t.Fatalf("unrecovered engine must block preview as R00: %+v", pv)
	}
	if pv.Reason == "" {
		t.Fatal("blocked preview must carry a reason")
	}
}

func TestPreviewConfigRevisionGuard(t *testing.T) {
	r := newRig(t, nil)
	r.account(1_000_000, 500_000)
	o := buy("pv", 100)
	want := 99
	o.ExpectedConfigRevision = &want
	pv := r.e.Preview(context.Background(), o)
	if pv.Approved || pv.BlockRule != risk.RuleInvalid {
		t.Fatalf("plan-bound revision must be enforced in preview too: %+v", pv)
	}
}

func TestPreviewGarbageSideIsR00(t *testing.T) {
	// 方向缺省/脏值必须沿试算链原样走到 validate 判 R00——与实检同一防线；
	// 既往 biz 层「非卖即买」归并会让此类请求被按买单试算放行。
	for _, side := range []risk.Side{"", "hodor"} {
		r := newRig(t, nil)
		r.account(1_000_000, 500_000)
		o := buy("pv", 100)
		o.Side = side
		pv := r.e.Preview(context.Background(), o)
		if pv.Approved || pv.BlockRule != risk.RuleInvalid {
			t.Fatalf("garbage side %q must surface as R00 like Check, got %+v", side, pv)
		}
	}
}

func TestPreviewSellCollectsStampTax(t *testing.T) {
	r := newRig(t, nil)
	r.account(1_000_000, 500_000, risk.Holding{Symbol: "000001.SZ", Quantity: 1000, Available: 1000, AvgCost: 10})
	sell := buy("pv", 500)
	sell.Side = risk.Sell
	pv := r.e.Preview(context.Background(), sell)
	if !pv.Approved || pv.Volume != 500 || pv.Fee != 7.55 || pv.CashDelta != 4992.45 {
		t.Fatalf("sell preview: %+v", pv)
	}
}
