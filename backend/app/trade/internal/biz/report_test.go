package biz

import (
	"context"
	"math"
	"strings"
	"testing"

	"server/pkg/events"

	"github.com/go-kratos/kratos/v2/log"
)

// M13-T04 归并器单测：仿真撮合同一套 MemStore 注入，验证 exec_id 幂等、
// 状态映射、T+1/费用 parity、人核口留痕去重、对账沿用、分节「没取到≠没有」。

func reportEngine(t *testing.T) (*Engine, *MemStore) {
	t.Helper()
	mem := NewMemStore()
	e := NewEngine(mem, allow{}, Settings{InitialCash: 1_000_000, LiveEnabled: true, BrokerConnected: true}, log.DefaultLogger)
	return e, mem
}

func seedLiveCash(t *testing.T, mem *MemStore, cash float64, positions []Position, orders []Order) {
	t.Helper()
	batch := Batch{Positions: positions, Orders: orders, Cash: &Cash{Book: BookLive, Cash: cash, DayStartDate: "2026-10-22", DayStartEquity: cash}}
	if err := mem.Commit(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
}

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func keptOrder(t *testing.T, mem *MemStore, sid string) Order {
	t.Helper()
	o, ok, err := mem.LoadOrder(context.Background(), sid)
	if err != nil || !ok {
		t.Fatalf("LoadOrder(%s) = ok %v err %v", sid, ok, err)
	}
	return o
}

func eventCount(mem *MemStore, subject string) int {
	n := 0
	for _, ev := range mem.Events() {
		if ev.Subject == subject {
			n++
		}
	}
	return n
}

func TestApplyBrokerReportBuyFillReplayAndFilled(t *testing.T) {
	e, mem := reportEngine(t)
	ctx := context.Background()
	seedLiveCash(t, mem, 100000, nil, []Order{{
		ClientOrderID: "SID1", Book: BookLive, Symbol: "600000.SH", Side: "buy",
		Status: StatusSubmitted, Price: 10, Volume: 1000,
	}})
	frame := BrokerReport{
		Account: "A001",
		Orders: ReportSection[BrokerOrderReport]{Ready: true, Records: []BrokerOrderReport{
			{SID: "SID1", Symbol: "SHSE.600000", Side: "buy", State: "ACKNOWLEDGED", Price: 10, Volume: 1000},
		}},
		Executions: ReportSection[BrokerExecution]{Ready: true, Records: []BrokerExecution{
			{SID: "SID1", ExecID: "EX1", Symbol: "SHSE.600000", Side: "buy", Kind: "FILL", Qty: 400, Price: 10, Amount: 4000},
		}},
	}
	out, err := e.ApplyBrokerReport(ctx, frame)
	if err != nil || out.FillsApplied != 1 || out.FillsReplayed != 0 || len(out.Orphans) != 0 {
		t.Fatalf("apply: %+v err %v", out, err)
	}
	order := keptOrder(t, mem, "SID1")
	if order.Status != StatusPartial || order.Filled != 400 {
		t.Fatalf("order after partial fill: %+v", order)
	}
	pos, ok, _ := mem.LoadPosition(ctx, BookLive, "600000.SH")
	if !ok || pos.Quantity != 400 || pos.Available != 0 || !approx(pos.AvgCost, 10) {
		t.Fatalf("T+1 position after buy: %+v ok %v", pos, ok)
	}
	cash, _, _ := mem.LoadCash(ctx, BookLive)
	if !approx(cash.Cash, 100000-4000-5.04) {
		t.Fatalf("cash after buy fill = %v, want %v", cash.Cash, 100000-4000-5.04)
	}
	fills, _ := mem.ListFills(ctx, BookLive, 0)
	if len(fills) != 1 || fills[0].ExecID != "EX1" || !approx(fills[0].Fee, 5.04) {
		t.Fatalf("fills: %+v", fills)
	}
	if eventCount(mem, events.SubjectTradeFill) != 1 || eventCount(mem, events.SubjectTradeOrder) != 1 {
		t.Fatalf("events: fills %d orders %d", eventCount(mem, events.SubjectTradeFill), eventCount(mem, events.SubjectTradeOrder))
	}

	// 整帧重放：exec_id 幂等，账本不动、事件不增。
	out, err = e.ApplyBrokerReport(ctx, frame)
	if err != nil || out.FillsApplied != 0 || out.FillsReplayed != 1 {
		t.Fatalf("replay: %+v err %v", out, err)
	}
	cash, _, _ = mem.LoadCash(ctx, BookLive)
	if !approx(cash.Cash, 100000-4000-5.04) {
		t.Fatalf("cash changed after replay: %v", cash.Cash)
	}
	fills, _ = mem.ListFills(ctx, BookLive, 0)
	if len(fills) != 1 {
		t.Fatalf("fills doubled after replay: %d", len(fills))
	}
	if eventCount(mem, events.SubjectTradeFill) != 1 || eventCount(mem, events.SubjectTradeOrder) != 1 {
		t.Fatalf("replay re-emitted events")
	}

	// 第二笔成交凑满：状态映射 FILLED 收口，费用/现金 parity 到分。
	frame.Orders.Records[0] = BrokerOrderReport{SID: "SID1", Symbol: "SHSE.600000", Side: "buy", State: "FILLED", Price: 10, Volume: 1000, Filled: 1000}
	frame.Executions.Records[0] = BrokerExecution{SID: "SID1", ExecID: "EX2", Symbol: "SHSE.600000", Side: "buy", Kind: "FILL", Qty: 600, Price: 10, Amount: 6000}
	out, err = e.ApplyBrokerReport(ctx, frame)
	if err != nil || out.FillsApplied != 1 || out.OrdersAdvanced != 1 {
		t.Fatalf("fill completion: %+v err %v", out, err)
	}
	order = keptOrder(t, mem, "SID1")
	if order.Status != StatusFilled || order.Filled != 1000 {
		t.Fatalf("order after complete fill: %+v", order)
	}
	cash, _, _ = mem.LoadCash(ctx, BookLive)
	if !approx(cash.Cash, 100000-10000-10.10) {
		t.Fatalf("cash after completion = %v, want %v", cash.Cash, 100000-10000-10.10)
	}
	pos, _, _ = mem.LoadPosition(ctx, BookLive, "600000.SH")
	if pos.Quantity != 1000 || pos.Available != 0 {
		t.Fatalf("position after completion: %+v", pos)
	}
}

func TestApplyBrokerReportSellFillRealizedAndT1(t *testing.T) {
	e, mem := reportEngine(t)
	ctx := context.Background()
	seedLiveCash(t, mem, 0,
		[]Position{{Book: BookLive, Symbol: "600519.SH", Quantity: 1000, Available: 1000, AvgCost: 10, LastPrice: 10}},
		[]Order{{ClientOrderID: "SID2", Book: BookLive, Symbol: "600519.SH", Side: "sell", Status: StatusSubmitted, Price: 11, Volume: 1000}})
	out, err := e.ApplyBrokerReport(ctx, BrokerReport{
		Account: "A001",
		Executions: ReportSection[BrokerExecution]{Ready: true, Records: []BrokerExecution{
			{SID: "SID2", ExecID: "EX3", Symbol: "SHSE.600519", Side: "sell", Kind: "FILL", Qty: 1000, Price: 11, Amount: 11000},
		}},
		Positions: ReportSection[RemotePosition]{Ready: true}, // 柜台已无持仓：本地 0 也跳过，对账应平
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Reconciled == nil || !*out.Reconciled || len(out.ReconcileDiffs) != 0 {
		t.Fatalf("reconcile: %+v", out)
	}
	cash, _, _ := mem.LoadCash(ctx, BookLive)
	if !approx(cash.Cash, 11000-10.61) || !approx(cash.RealizedPnL, 1000-10.61) {
		t.Fatalf("cash after sell: %+v", cash)
	}
	pos, ok, _ := mem.LoadPosition(ctx, BookLive, "600519.SH")
	if !ok || pos.Quantity != 0 || pos.Available != 0 {
		t.Fatalf("position after sell: %+v", pos)
	}
	if keptOrder(t, mem, "SID2").Status != StatusFilled {
		t.Fatalf("sell order not closed")
	}
}

func TestApplyBrokerReportReconcileMismatchHaltsOpening(t *testing.T) {
	e, mem := reportEngine(t)
	ctx := context.Background()
	seedLiveCash(t, mem, 100,
		[]Position{{Book: BookLive, Symbol: "600519.SH", Quantity: 1000, Available: 800, AvgCost: 10}},
		nil)
	out, err := e.ApplyBrokerReport(ctx, BrokerReport{
		Account: "A001",
		Positions: ReportSection[RemotePosition]{Ready: true, Records: []RemotePosition{
			{Symbol: "600519.SH", Quantity: 900, Available: 800},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Reconciled == nil || *out.Reconciled || len(out.ReconcileDiffs) != 1 {
		t.Fatalf("reconcile outcome: %+v", out)
	}
	cash, _, _ := mem.LoadCash(ctx, BookLive)
	if !cash.OpenHalted || cash.HaltReason == "" {
		t.Fatalf("open not halted: %+v", cash)
	}
	if eventCount(mem, events.SubjectTradeReconciliation) != 1 {
		t.Fatalf("no reconciliation event emitted")
	}
}

func TestApplyBrokerReportOrphansDispositionDedupe(t *testing.T) {
	e, mem := reportEngine(t)
	ctx := context.Background()
	seedLiveCash(t, mem, 100000, nil, nil)
	frame := BrokerReport{
		Account: "A001",
		Orders: ReportSection[BrokerOrderReport]{Ready: true, Records: []BrokerOrderReport{
			{SID: "GHOST", Symbol: "SHSE.600600", Side: "buy", State: "PARTIAL", Price: 5, Volume: 100, Filled: 40},
		}},
		Executions: ReportSection[BrokerExecution]{Ready: true, Records: []BrokerExecution{
			{SID: "GHOST", ExecID: "EX9", Symbol: "SHSE.600600", Side: "buy", Kind: "FILL", Qty: 40, Price: 5, Amount: 200},
		}},
	}
	out, err := e.ApplyBrokerReport(ctx, frame)
	if err != nil || out.FillsApplied != 0 || len(out.Orphans) != 2 {
		t.Fatalf("orphan frame: %+v err %v", out, err)
	}
	if len(mem.Dispositions()) != 2 {
		t.Fatalf("dispositions: %+v", mem.Dispositions())
	}
	if eventCount(mem, events.SubjectNotifyRequest) != 2 {
		t.Fatalf("notify event count = %d", eventCount(mem, events.SubjectNotifyRequest))
	}
	// 同帧重放：留痕去重，不二次告警（2s 轮询防告警风暴）。
	out, err = e.ApplyBrokerReport(ctx, frame)
	if err != nil || len(out.Orphans) != 2 {
		t.Fatalf("orphan replay: %+v err %v", out, err)
	}
	if len(mem.Dispositions()) != 2 || eventCount(mem, events.SubjectNotifyRequest) != 2 {
		t.Fatalf("replay duplicated dispositions or alerts: %d / %d", len(mem.Dispositions()), eventCount(mem, events.SubjectNotifyRequest))
	}
	// 人核口只留痕，不自动落本地委托/成交。
	if _, ok, _ := mem.LoadOrder(ctx, "GHOST"); ok {
		t.Fatalf("orphan order was auto-booked")
	}
	fills, _ := mem.ListFills(ctx, BookLive, 0)
	if len(fills) != 0 {
		t.Fatalf("orphan fill was auto-booked")
	}
}

func TestApplyBrokerReportConflictsAndUnknown(t *testing.T) {
	e, mem := reportEngine(t)
	ctx := context.Background()
	seedLiveCash(t, mem, 100000, nil, []Order{
		{ClientOrderID: "SID1", Book: BookLive, Symbol: "600000.SH", Side: "buy", Status: StatusSubmitted, Price: 10, Volume: 1000},
		{ClientOrderID: "SID3", Book: BookLive, Symbol: "600000.SH", Side: "buy", Status: StatusSubmitted, Price: 10, Volume: 100},
	})
	out, err := e.ApplyBrokerReport(ctx, BrokerReport{
		Account: "A001",
		Orders: ReportSection[BrokerOrderReport]{Ready: true, Records: []BrokerOrderReport{
			{SID: "SID1", Symbol: "SHSE.600000", Side: "buy", State: "FILLED", Price: 10, Volume: 1000, Filled: 1000},
			{SID: "SID3", Symbol: "SHSE.600000", Side: "buy", State: "UNKNOWN", Price: 10, Volume: 100},
		}},
		Executions: ReportSection[BrokerExecution]{Ready: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	// FILLED vs 本地零成交：冲突单人核，不改写状态。
	if keptOrder(t, mem, "SID1").Status != StatusSubmitted {
		t.Fatalf("conflict order status was advanced")
	}
	foundConflict := false
	for _, o := range out.Orphans {
		if o.Kind == "order_conflict" && o.SID == "SID1" {
			foundConflict = true
		}
		if o.SID == "SID3" {
			t.Fatalf("UNKNOWN order must not be adjudicated: %+v", o)
		}
	}
	if !foundConflict || out.OrdersUnknown != 1 {
		t.Fatalf("conflict/unknown accounting: %+v", out)
	}
}

func TestApplyBrokerReportCancelRejectAndBaseline(t *testing.T) {
	e, mem := reportEngine(t)
	ctx := context.Background()
	// 无 live 资金基线：成交照常入账但必须留 baseline_missing 人核口。
	if err := mem.Commit(ctx, Batch{Orders: []Order{
		{ClientOrderID: "SID4", Book: BookLive, Symbol: "000001.SZ", Side: "buy", Status: StatusSubmitted, Price: 8, Volume: 100},
		{ClientOrderID: "SID5", Book: BookLive, Symbol: "000001.SZ", Side: "buy", Status: StatusSubmitted, Price: 8, Volume: 100},
		{ClientOrderID: "SID6", Book: BookLive, Symbol: "000001.SZ", Side: "buy", Status: StatusSubmitted, Price: 8, Volume: 100},
	}}); err != nil {
		t.Fatal(err)
	}
	out, err := e.ApplyBrokerReport(ctx, BrokerReport{
		Account: "A001",
		Orders: ReportSection[BrokerOrderReport]{Ready: true, Records: []BrokerOrderReport{
			{SID: "SID4", Symbol: "SZSE.000001", Side: "buy", State: "CANCELLED", Price: 8, Volume: 100},
			{SID: "SID5", Symbol: "SZSE.000001", Side: "buy", State: "REJECTED", Price: 8, Volume: 100, RejectDetail: "价格笼子"},
			{SID: "SID6", Symbol: "SZSE.000001", Side: "buy", State: "EXPIRED", Price: 8, Volume: 100},
		}},
		Executions: ReportSection[BrokerExecution]{Ready: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !out.BaselineMissing || len(out.Orphans) != 1 || out.Orphans[0].Kind != "baseline_missing" {
		t.Fatalf("baseline missing: %+v", out)
	}
	if keptOrder(t, mem, "SID4").Status != StatusCancelled || keptOrder(t, mem, "SID6").Status != StatusCancelled {
		t.Fatalf("cancel/expired mapping wrong")
	}
	if o := keptOrder(t, mem, "SID5"); o.Status != StatusRejected || !strings.Contains(o.Reason, "价格笼子") {
		t.Fatalf("rejected mapping: %+v", o)
	}
	if len(mem.Dispositions()) != 1 || mem.Dispositions()[0].EventID != "broker-report:baseline_missing:ledger" {
		t.Fatalf("baseline disposition: %+v", mem.Dispositions())
	}
}

func TestApplyBrokerReportSectionGating(t *testing.T) {
	e, mem := reportEngine(t)
	ctx := context.Background()
	seedLiveCash(t, mem, 100000, nil, []Order{
		{ClientOrderID: "SID1", Book: BookLive, Symbol: "600000.SH", Side: "buy", Status: StatusSubmitted, Price: 10, Volume: 1000},
	})
	out, err := e.ApplyBrokerReport(ctx, BrokerReport{
		Account:    "A001",
		Orders:     ReportSection[BrokerOrderReport]{Ready: true},
		Executions: ReportSection[BrokerExecution]{Ready: false, Reason: "execution_report.csv:STALE", Records: []BrokerExecution{{SID: "SID1", ExecID: "EX7", Symbol: "SHSE.600000", Kind: "FILL", Qty: 100, Price: 10}}},
		Positions:  ReportSection[RemotePosition]{Ready: false, Reason: "position.csv:NOT_READY", Records: []RemotePosition{{Symbol: "600000.SH", Quantity: 9999}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.FillsApplied != 0 || out.Reconciled != nil || len(out.SectionsSkipped) != 2 {
		t.Fatalf("stale sections must not be applied: %+v", out)
	}
	cash, _, _ := mem.LoadCash(ctx, BookLive)
	if !approx(cash.Cash, 100000) {
		t.Fatalf("stale frame touched cash: %+v", cash)
	}
	if keptOrder(t, mem, "SID1").Status != StatusSubmitted || cash.OpenHalted {
		t.Fatalf("stale frame touched ledger state")
	}
}

func TestApplyBrokerReportRequiresAccount(t *testing.T) {
	e, _ := reportEngine(t)
	if _, err := e.ApplyBrokerReport(context.Background(), BrokerReport{}); err == nil {
		t.Fatalf("empty account must fail closed")
	}
}
