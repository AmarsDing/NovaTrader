package data

// 闭仓归并投影的显式 PG 隔离集成（NT_TEST_PG_PORT 先例，无实例 skip）：
// 造 trade_fill / trade_signal 行走穿「候选谓语 → 账本重放 → PlanClosure →
// Transition CAS → signal_events.reason 证据 JSON」全链。

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/uuid"
	"server/app/strategy/internal/biz"
	"server/ent"
	"server/ent/executionintent"
	"server/ent/signalevent"
	"server/ent/tradefill"
	"server/ent/tradesignal"
	"server/pkg/tradecal"
)

// closureFixture 建一条 done 候选形态买入信号与其账本，返回可用句柄与清理。
func closureFixture(t *testing.T, d *Data, book, symbol string, volume int, price float64) (*biz.Usecase, biz.ClosureProjectionRepo, biz.SignalRepo, *ent.TradeSignal, string) {
	t.Helper()
	day := time.Date(2026, 10, 20, 0, 0, 0, 0, tradecal.Shanghai())
	row, err := d.client.TradeSignal.Create().
		SetBook(book).SetSignalTime(day.Add(10 * time.Hour)).SetStockCode(symbol).SetStockName("闭仓合成").
		SetSignalType(tradesignal.SignalTypeBuy).SetEntryPrice(10).SetStopLoss(9).SetTakeProfit(11).
		SetSignalReasoning("synthetic closure contract").SetConfidenceScore(80).
		SetStatus(string(biz.StatusDone)).SetExecutedVolume(volume).SetExecutedPrice(price).
		SetTradeDate(day).
		Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	prefix := "closure-" + uuid.NewString()
	t.Cleanup(func() {
		_, _ = d.client.TradeFill.Delete().Where(tradefill.ClientOrderIDHasPrefix(prefix)).Exec(ctx)
	})
	repo := NewSignalRepo(d)
	cpr, ok := repo.(biz.ClosureProjectionRepo)
	if !ok {
		t.Fatal("signalRepo must implement biz.ClosureProjectionRepo")
	}
	uc := biz.NewUsecase(biz.NewSettings(nil), nil, nil, nil, repo, nil, nil, nil, nil, log.DefaultLogger)
	return uc, cpr, repo, row, prefix
}

func closureFillRow(t *testing.T, d *Data, orderID, signalID, book, symbol, side string, qty int, price, fee float64) *ent.TradeFill {
	t.Helper()
	row, err := d.client.TradeFill.Create().
		SetClientOrderID(orderID).SetBook(book).SetSymbol(symbol).SetSide(side).
		SetQty(qty).SetPrice(price).SetAmount(price * float64(qty)).SetFee(fee).SetSignalID(signalID).
		Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return row
}

// closureFillRowAt 显式钉 created_at：closed_at=trade 进程入库墙钟的确定性
// （重放恒定，合同 §1.4）必须以历史时刻钉死，不能挂运行日墙钟。
func closureFillRowAt(t *testing.T, d *Data, orderID, signalID, book, symbol, side string, qty int, price, fee float64, at time.Time) *ent.TradeFill {
	t.Helper()
	row, err := d.client.TradeFill.Create().
		SetClientOrderID(orderID).SetBook(book).SetSymbol(symbol).SetSide(side).
		SetQty(qty).SetPrice(price).SetAmount(price * float64(qty)).SetFee(fee).SetSignalID(signalID).
		SetCreatedAt(at).
		Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return row
}

// 候选谓语写死：done ∧ buy ∧ executed_volume 非空且 > 0 ∧ book 匹配，其余一律不入选。
func TestPostgresCloseCandidatesPredicateIsStrict(t *testing.T) {
	d := newTestData(t)
	_, cpr, _, keep, _ := closureFixture(t, d, "paper", "600519.SH", 300, 10.01)
	mk := func(book, side, status string, ev *int) {
		day := time.Date(2026, 10, 20, 0, 0, 0, 0, tradecal.Shanghai())
		c := d.client.TradeSignal.Create().
			SetBook(book).SetSignalTime(day).SetStockCode("600519.SH").SetStockName("闭仓合成").
			SetSignalType(tradesignal.SignalType(side)).SetEntryPrice(10).SetStopLoss(9).SetTakeProfit(11).
			SetSignalReasoning("exclusion fixture").SetConfidenceScore(80).SetStatus(status)
		if ev != nil {
			c.SetExecutedVolume(*ev).SetExecutedPrice(10)
		}
		if _, err := c.Save(ctx); err != nil {
			t.Fatal(err)
		}
	}
	ev := 100
	mk("live", "buy", string(biz.StatusDone), &ev)     // 非本服务配置 book
	mk("paper", "sell", string(biz.StatusDone), &ev)   // 非买入
	mk("paper", "buy", string(biz.StatusDone), nil)    // executed_volume 缺失（没取到≠0）
	mk("paper", "buy", string(biz.StatusPending), &ev) // 非 done
	zero := 0
	mk("paper", "buy", string(biz.StatusDone), &zero) // executed_volume 为零

	ids, err := cpr.CloseCandidates(ctx, "paper", 0, 200)
	if err != nil || len(ids) != 1 || ids[0] != keep.ID {
		t.Fatalf("candidates %v err=%v (want only %d)", ids, err, keep.ID)
	}
	// keyset 分页：IDGT 之后为空。
	ids, err = cpr.CloseCandidates(ctx, "paper", keep.ID, 200)
	if err != nil || len(ids) != 0 {
		t.Fatalf("keyset after: %v err=%v", ids, err)
	}
}

// 全链：窗口 → 归属 → Transition CAS → signal_events.reason 证据 JSON → outbox 同事务。
func TestPostgresClosureAttributionWalksWindowToClosedRow(t *testing.T) {
	d := newTestData(t)
	uc, cpr, repo, sig, prefix := closureFixture(t, d, "paper", "600519.SH", 300, 10.01)
	sid := strconv.Itoa(sig.ID)
	// closed_at 钉固定入库墙钟（trade_date=2026-10-20 → 卖出 2026-10-21 14:55 收盘前）：
	// holding_days = 10-20 之后到 10-21（含）的交易日数 = 1，与运行日解耦。
	closedAt := time.Date(2026, 10, 21, 14, 55, 0, 0, tradecal.Shanghai())
	closureFillRow(t, d, prefix+"-buy", sid, "paper", "600519.SH", "buy", 300, 10, 5)
	closureFillRowAt(t, d, prefix+"-sell", "", "paper", "600519.SH", "sell", 300, 11, 8.5, closedAt)
	closureFillRow(t, d, prefix+"-other", sid, "paper", "000001.SZ", "buy", 100, 8, 5) // 别的 pair 不混入

	facts, err := cpr.ClosurePairFacts(ctx, "paper", "600519.SH")
	if err != nil {
		t.Fatal(err)
	}
	if len(facts.Fills) != 2 || facts.Fills[0].Side != "buy" || facts.Fills[1].Side != "sell" {
		t.Fatalf("fills not the pair's own ascending ledger: %+v", facts.Fills)
	}
	if len(facts.Signals) != 1 || facts.Signals[0].ID != sig.ID || facts.PendingIntent[sig.ID] {
		t.Fatalf("signals %+v pending %v", facts.Signals, facts.PendingIntent)
	}
	res, err := biz.PlanClosure(facts)
	if err != nil || len(res.Plans) != 1 || len(res.Over) != 0 {
		t.Fatalf("plans=%v over=%v err=%v", res.Plans, res.Over, err)
	}
	plan := res.Plans[0]
	if plan.SignalID != sig.ID || plan.ExitPrice != 11 || plan.PnlPct != 9.5341 ||
		plan.Window != [2]int{facts.Fills[0].ID, facts.Fills[1].ID} ||
		plan.ClosedFillID != facts.Fills[1].ID || !plan.At.Equal(closedAt) {
		t.Fatalf("plan %+v", plan)
	}

	closed, err := uc.Transition(ctx, sig.ID, biz.StatusClosed, plan.Reason, biz.ActorTrade,
		&biz.Close{ExitPrice: plan.ExitPrice, PnlPct: plan.PnlPct, At: plan.At})
	if err != nil {
		t.Fatal(err)
	}
	if closed.ExitPrice == nil || *closed.ExitPrice != 11 || closed.PnlPct == nil || *closed.PnlPct != 9.5341 ||
		closed.HoldingDays == nil || *closed.HoldingDays != 1 || !closed.ClosedAt.Equal(closedAt) {
		t.Fatalf("closed row %+v", closed)
	}
	// 证据载体：signal_events.reason 中文前缀 + 紧凑 JSON，键一个不少。
	evs, err := d.client.SignalEvent.Query().Where(signalevent.SignalID(sig.ID)).Order(signalevent.ByID()).All(ctx)
	if err != nil || len(evs) != 1 || evs[0].FromStatus != "done" || evs[0].ToStatus != "closed" || evs[0].Actor != biz.ActorTrade {
		t.Fatalf("events %v err=%v", evs, err)
	}
	reason := evs[0].Reason
	if !strings.HasPrefix(reason, "闭仓归并 ") || !strings.Contains(reason, `"signal_id":`+sid) ||
		!strings.Contains(reason, `"pnl_pct":9.5341`) || !strings.Contains(reason, `"closed_fill_id":`) ||
		!strings.Contains(reason, `"replay_head":`) || !strings.Contains(reason, `"buy_fee":5`) ||
		!strings.Contains(reason, `"sell_fee":8.5`) {
		t.Fatalf("evidence reason %q", reason)
	}
	// 候选出清：closed 后不再进候选页，不再产出归属（幂等）。
	if ids, _ := cpr.CloseCandidates(ctx, "paper", 0, 200); len(ids) != 0 {
		t.Fatalf("closed signal still a candidate: %v", ids)
	}
	if _, err = uc.Transition(ctx, sig.ID, biz.StatusClosed, plan.Reason, biz.ActorTrade,
		&biz.Close{ExitPrice: plan.ExitPrice, PnlPct: plan.PnlPct, At: plan.At}); !errors.Is(err, biz.ErrBadTransition) {
		t.Fatalf("re-close must be rejected: %v", err)
	}
	got, _ := repo.Get(ctx, sig.ID)
	if got.Status != biz.StatusClosed {
		t.Fatalf("status regressed: %+v", got)
	}
}

// Gate-D：候选信号存在未决执行意图时跳过本轮，不闭仓；意图消除后正常归属。
func TestPostgresClosurePairFactsCarryPendingIntent(t *testing.T) {
	d := newTestData(t)
	_, cpr, _, sig, prefix := closureFixture(t, d, "paper", "600519.SH", 100, 10)
	sid := strconv.Itoa(sig.ID)
	closureFillRow(t, d, prefix+"-buy", sid, "paper", "600519.SH", "buy", 100, 10, 5)
	closureFillRow(t, d, prefix+"-sell", "", "paper", "600519.SH", "sell", 100, 11, 5)

	key := fmt.Sprintf("signal:SIM:default:%d", sig.ID)
	intent, err := d.client.ExecutionIntent.Create().
		SetBusinessKey(key).SetClientOrderID(prefix + "-intent").SetRequestHash("synthetic").
		SetBook("paper").SetState("claimed").Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = d.client.ExecutionIntent.Delete().Where(executionintent.ID(intent.ID)).Exec(ctx) })
	facts, err := cpr.ClosurePairFacts(ctx, "paper", "600519.SH")
	if err != nil || !facts.PendingIntent[sig.ID] {
		t.Fatalf("pending intent not projected: %+v err=%v", facts.PendingIntent, err)
	}
	res, err := biz.PlanClosure(facts)
	if err != nil || len(res.Plans) != 0 {
		t.Fatalf("Gate-D must skip this round: %+v err=%v", res, err)
	}
}
