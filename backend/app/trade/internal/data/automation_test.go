package data

import (
	"context"
	"fmt"
	"github.com/go-kratos/kratos/v2/log"
	"server/app/trade/internal/biz"
	"server/ent/decisionplan"
	"server/ent/executionintent"
	"server/ent/tradeorder"
	"server/ent/tradesignal"
	"server/pkg/market"
	"server/pkg/tradecal"
	"testing"
	"time"
)

type planRisk struct{}

func (planRisk) Check(context.Context, biz.CheckIn) (biz.CheckOut, error) {
	return biz.CheckOut{Approved: true}, nil
}
func TestPostgresPlanFreezeAuthorizeExecuteAndRevoke(t *testing.T) {
	c := accountTestClient(t)
	ctx := context.Background()
	repo := NewRepo(c, log.DefaultLogger)
	repo.quote = func(_ context.Context, s string) (market.Snapshot, error) {
		return market.Snapshot{Symbol: s, Time: time.Now(), AsOf: time.Now(), Last: 10}, nil
	}
	e := biz.NewEngine(repo, planRisk{}, biz.Settings{InitialCash: 1000000}, log.DefaultLogger)
	// A synthetic ledger fixture is anchored to this test's clock. It is not a
	// real trading-day admission sample, and no production ledger is touched.
	if err := repo.Commit(ctx, biz.Batch{Cash: &biz.Cash{Book: "paper", Cash: 1000000, DayStartDate: time.Now().In(tradecal.Shanghai()).Format("2006-01-02"), DayStartEquity: 1000000}}); err != nil {
		t.Fatal(err)
	}
	sig, err := c.TradeSignal.Create().SetBook("paper").SetSignalTime(time.Now()).SetStockCode("600519.SH").SetStockName("合同测试").SetSignalType(tradesignal.SignalTypeBuy).SetEntryPrice(10).SetEntryLow(9.8).SetEntryHigh(10.2).SetStopLoss(9).SetTakeProfit(11).SetPositionPct(.05).SetSignalReasoning("明确标记的合成规则fixture").SetConfidenceScore(80).SetRuleScore(80).SetValidUntil(time.Now().Add(5 * time.Minute)).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.TradeSignal.DeleteOneID(sig.ID).Exec(ctx)
	defer c.DecisionPlan.Delete().Where(decisionplan.SignalID(sig.ID)).Exec(ctx)
	businessKey := fmt.Sprintf("signal:SIM:default:%d", sig.ID)
	defer c.ExecutionIntent.Delete().Where(executionintent.BusinessKey(businessKey)).Exec(ctx)
	defer c.TradeOrder.Delete().Where(tradeorder.SignalID(fmt.Sprint(sig.ID))).Exec(ctx)
	p, err := e.CreatePlan(ctx, sig.ID, "SIM", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if p.Plan.PriceOrigin != "rules" || p.State != "draft" || p.Snapshot.SignalHash == "" {
		t.Fatal("not frozen", p)
	}
	if _, err = e.AuthorizePlan(ctx, p.Plan.PlanID, 1, "sim_confirm", "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err = e.ExecutePlan(ctx, p.Plan.PlanID, 1, ""); err == nil {
		t.Fatal("confirmation bypassed")
	}
	o, err := e.ExecutePlan(ctx, p.Plan.PlanID, 1, "tester")
	if err != nil {
		t.Fatal(err)
	}
	if o.Status != "submitted" || o.SignalID != fmt.Sprint(sig.ID) {
		t.Fatalf("wrong order %+v", o)
	}
	if _, err = e.ExecutePlan(ctx, p.Plan.PlanID, 1, "tester"); err == nil {
		t.Fatal("executed plan reused")
	}
	plans, err := e.Plans(ctx, "SIM")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range plans {
		if r.Plan.PlanID == p.Plan.PlanID {
			found = true
			if r.State != "executed" || r.Operator != "tester" {
				t.Fatal(r)
			}
		}
	}
	if !found {
		t.Fatal("plan disappeared")
	}
	// A fresh plan for the same signal may be reviewed, but its existing business
	// intent cannot produce a second order, even after a process restart.
	p2, err := e.CreatePlan(ctx, sig.ID, "SIM", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.AuthorizePlan(ctx, p2.Plan.PlanID, 1, "sim_auto", "tester"); err != nil {
		t.Fatal(err)
	}
	restarted := biz.NewEngine(repo, planRisk{}, biz.Settings{}, log.DefaultLogger)
	if err = restarted.RecoverIntents(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = restarted.ExecutePlan(ctx, p2.Plan.PlanID, 1, ""); err == nil {
		t.Fatal("same signal produced a second intent")
	}
	if _, err = e.AuthorizePlan(ctx, p2.Plan.PlanID, 1, "revoke", "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err = e.ExecutePlan(ctx, p2.Plan.PlanID, 1, "tester"); err == nil {
		t.Fatal("revoked plan executed")
	}
}
