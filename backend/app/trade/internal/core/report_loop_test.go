package core

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"server/app/trade/internal/biz"
	"server/pkg/brokerif"
	"server/pkg/events"

	"github.com/go-kratos/kratos/v2/log"
)

// M13-T04 回报回路端到端：假 ReportSource 按 broker-gw simulator 的协议载荷
// 剧本回放（接口注入，不经网络），验证休眠门、交易所历节拍、账本 parity、
// 重放幂等与人核口留痕。载荷形状与 broker-gw reader.normalize 输出同构。

type fakeReportSource struct {
	mu     sync.Mutex
	frames []json.RawMessage
	last   json.RawMessage
	calls  int
}

func (f *fakeReportSource) push(frame map[string]any) {
	raw, err := json.Marshal(frame)
	if err != nil {
		panic(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.frames = append(f.frames, raw)
}

// Snapshot 逐帧消费剧本；剧本播完返回最后一帧（网关持续提供最新快照是常态，
// 重放幂等由归并器保证）。
func (f *fakeReportSource) Snapshot(context.Context) (json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if len(f.frames) == 0 {
		return f.last, nil
	}
	f.last = f.frames[0]
	f.frames = f.frames[1:]
	return f.last, nil
}

func reportFrame(reports map[string]any) map[string]any {
	return map[string]any{
		"protocol":      "eastmoney-file-csv-isolated-v1",
		"mode":          "ISOLATED",
		"account_id":    "55001234",
		"reports_ready": true,
		"can_open_live": false,
		"observed_at":   1761093600.0,
		"reports":       reports,
	}
}

func readyFullReport(name string, records []any) map[string]any {
	return map[string]any{"name": name, "available": true, "fresh": true, "records": records}
}

func readyAppendReport(name string, records []any) map[string]any {
	return map[string]any{"name": name, "available": true, "records": records}
}

func orderRow(sid, state string, volume, filled int) map[string]any {
	return map[string]any{"sid": sid, "symbol": "SHSE.600000", "state": state, "order_business": "1",
		"price": "10.00", "volume": volume, "filled_volume": filled}
}

func fillRow(sid, execID string, qty int) map[string]any {
	return map[string]any{"sid": sid, "exec_id": execID, "symbol": "SHSE.600000", "kind": "FILL",
		"order_business": "1", "price": "10.00", "amount": "4000.00", "volume": qty}
}

func reportRunner(t *testing.T, src ReportSource) (*Runner, *biz.MemStore, *brokerif.Tracker) {
	t.Helper()
	mem := biz.NewMemStore()
	e := biz.NewEngine(mem, allowCheck{}, biz.Settings{InitialCash: 1_000_000}, log.DefaultLogger)
	tracker := brokerif.NewTracker(time.Now())
	r := &Runner{
		e: e, log: log.NewHelper(log.DefaultLogger),
		live:       &LiveChannel{Tracker: tracker},
		reportSrc:  src,
		reportAcct: "55001234",
	}
	return r, mem, tracker
}

func near(a, b float64) bool { const eps = 1e-6; return a-b < eps && b-a < eps }

func TestReportOnceDormantUntilConnected(t *testing.T) {
	src := &fakeReportSource{}
	r, _, tracker := reportRunner(t, src)
	ctx := context.Background()
	// idle：健康链没连上 → 明示休眠，绝不对未连接的通道发起调用。
	tick := r.reportOnce(ctx)
	if !tick.dormant || tick.err != nil || src.calls != 0 {
		t.Fatalf("idle must sleep without calling source: %+v calls=%d", tick, src.calls)
	}
	// down 同口径休眠。
	if err := tracker.Transition(brokerif.Down, time.Now()); err != nil {
		t.Fatal(err)
	}
	tick = r.reportOnce(ctx)
	if !tick.dormant || tick.err != nil || src.calls != 0 {
		t.Fatalf("down must sleep: %+v calls=%d", tick, src.calls)
	}
	if status, _ := tracker.Current(); status != brokerif.Down {
		t.Fatalf("tracker: %v", status)
	}
	// connected（含 down→connected 恢复）才轮询。
	if err := tracker.Transition(brokerif.Connected, time.Now()); err != nil {
		t.Fatal(err)
	}
	src.frames = []json.RawMessage{}
	src.push(reportFrame(map[string]any{
		"cash.csv":             readyFullReport("cash.csv", []any{map[string]any{"balance": "100000.00", "available": "100000.00", "frozen": "0.00"}}),
		"position.csv":         readyFullReport("position.csv", []any{}),
		"order_status.csv":     readyFullReport("order_status.csv", []any{}),
		"execution_report.csv": readyAppendReport("execution_report.csv", []any{}),
	}))
	tick = r.reportOnce(ctx)
	if tick.err != nil || !tick.merged || tick.dormant || src.calls != 1 {
		t.Fatalf("connected tick: %+v calls=%d", tick, src.calls)
	}
}

func TestReportIntervalFollowsTradingCalendar(t *testing.T) {
	r, _, _ := reportRunner(t, &fakeReportSource{})
	shanghai := time.FixedZone("CST", 8*3600)
	r.now = func() time.Time { return time.Date(2026, 10, 21, 10, 0, 0, 0, shanghai) } // 周三盘中
	if got := r.reportInterval(context.Background()); got != reportTickTrading {
		t.Fatalf("trading tick = %v", got)
	}
	r.now = func() time.Time { return time.Date(2026, 10, 21, 12, 0, 0, 0, shanghai) } // 午休
	if got := r.reportInterval(context.Background()); got != reportTickNonTrading {
		t.Fatalf("noon tick = %v", got)
	}
	r.now = func() time.Time { return time.Date(2026, 10, 24, 10, 0, 0, 0, shanghai) } // 周六
	if got := r.reportInterval(context.Background()); got != reportTickNonTrading {
		t.Fatalf("weekend tick = %v", got)
	}
}

func TestReportLoopEndToEndReplayAndOrphan(t *testing.T) {
	src := &fakeReportSource{}
	r, mem, tracker := reportRunner(t, src)
	if err := tracker.Transition(brokerif.Connected, time.Now()); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// 本地账本：实盘现金基线 + 一张限价买单（已提交柜台）。
	if err := mem.Commit(ctx, biz.Batch{
		Cash: &biz.Cash{Book: biz.BookLive, Cash: 100000, DayStartDate: "2026-10-21", DayStartEquity: 100000},
		Orders: []biz.Order{{ClientOrderID: "SID1", Book: biz.BookLive, Symbol: "600000.SH", Side: "buy",
			Status: biz.StatusSubmitted, Price: 10, Volume: 1000}},
	}); err != nil {
		t.Fatal(err)
	}
	// 帧1：柜台 ACK + 成交 400@10（追加流 fresh=null 是常态）。
	src.push(reportFrame(map[string]any{
		"cash.csv":             readyFullReport("cash.csv", []any{map[string]any{"balance": "89994.96", "available": "89994.96", "frozen": "0.00"}}),
		"position.csv":         readyFullReport("position.csv", []any{map[string]any{"symbol": "SHSE.600000", "volume": 400, "volume_today": 400, "available_now": 0}}),
		"order_status.csv":     readyFullReport("order_status.csv", []any{orderRow("SID1", "ACKNOWLEDGED", 1000, 0)}),
		"execution_report.csv": readyAppendReport("execution_report.csv", []any{fillRow("SID1", "EX1", 400)}),
	}))
	tick := r.reportOnce(ctx)
	if tick.err != nil || tick.out.FillsApplied != 1 || tick.out.FillsReplayed != 0 {
		t.Fatalf("frame1: %+v err %v", tick, tick.err)
	}
	cash, _, _ := mem.LoadCash(ctx, biz.BookLive)
	if got, want := cash.Cash, 100000-4000-5.04; !near(got, want) {
		t.Fatalf("cash after frame1 = %v, want %v", got, want)
	}
	pos, _, _ := mem.LoadPosition(ctx, biz.BookLive, "600000.SH")
	if pos.Quantity != 400 || pos.Available != 0 {
		t.Fatalf("position after frame1: %+v", pos)
	}

	// 帧1 同帧重放（剧本播完回最后一帧）：exec_id 幂等，账本不动。
	tick = r.reportOnce(ctx)
	if tick.err != nil || tick.out.FillsApplied != 0 || tick.out.FillsReplayed != 1 {
		t.Fatalf("frame1 replay: %+v", tick)
	}
	cash, _, _ = mem.LoadCash(ctx, biz.BookLive)
	if got, want := cash.Cash, 100000-4000-5.04; !near(got, want) {
		t.Fatalf("replay changed cash: %v", got)
	}
	fills, _ := mem.ListFills(ctx, biz.BookLive, 0)
	if len(fills) != 1 {
		t.Fatalf("replay doubled fills: %d", len(fills))
	}

	// 帧2：柜台 FILLED + 剩余 600 成交（EX1 已在上一帧，帧内级重放跳过）。
	src.push(reportFrame(map[string]any{
		"cash.csv":             readyFullReport("cash.csv", []any{map[string]any{"balance": "89989.90", "available": "89989.90", "frozen": "0.00"}}),
		"position.csv":         readyFullReport("position.csv", []any{map[string]any{"symbol": "SHSE.600000", "volume": 1000, "volume_today": 1000, "available_now": 0}}),
		"order_status.csv":     readyFullReport("order_status.csv", []any{orderRow("SID1", "FILLED", 1000, 1000)}),
		"execution_report.csv": readyAppendReport("execution_report.csv", []any{fillRow("SID1", "EX1", 400), fillRow("SID1", "EX2", 600)}),
	}))
	tick = r.reportOnce(ctx)
	if tick.err != nil || tick.out.FillsApplied != 1 || tick.out.FillsReplayed != 1 || len(tick.out.Orphans) != 0 {
		t.Fatalf("frame2: %+v err %v", tick, tick.err)
	}
	order, _, _ := mem.LoadOrder(ctx, "SID1")
	if order.Status != biz.StatusFilled || order.Filled != 1000 {
		t.Fatalf("order after frame2: %+v", order)
	}
	cash, _, _ = mem.LoadCash(ctx, biz.BookLive)
	if got, want := cash.Cash, 100000-10000-10.10; !near(got, want) {
		t.Fatalf("cash after frame2 = %v, want %v", got, want)
	}

	// 帧3：孤儿成交回报 + 空仓报表（与本地持仓对不上）：必须停开仓 + 人核留痕，
	// 归并器不自动裁决任何资产差异。
	src.push(reportFrame(map[string]any{
		"cash.csv":             readyFullReport("cash.csv", []any{map[string]any{"balance": "89989.90", "available": "89989.90", "frozen": "0.00"}}),
		"position.csv":         readyFullReport("position.csv", []any{}),
		"order_status.csv":     readyFullReport("order_status.csv", []any{orderRow("SID1", "FILLED", 1000, 1000)}),
		"execution_report.csv": readyAppendReport("execution_report.csv", []any{fillRow("EX-GHOST", "EX9", 100)}),
	}))
	tick = r.reportOnce(ctx)
	if tick.err != nil || len(tick.out.Orphans) != 1 || tick.out.Reconciled == nil || *tick.out.Reconciled {
		t.Fatalf("frame3 orphan+reconcile: %+v err %v", tick, tick.err)
	}
	cash, _, _ = mem.LoadCash(ctx, biz.BookLive)
	if !cash.OpenHalted {
		t.Fatalf("reconcile mismatch must halt opening: %+v", cash)
	}
	if got := len(mem.Dispositions()); got != 1 {
		t.Fatalf("dispositions = %d", got)
	}
	// 帧3 重放：留痕去重不重复告警。
	tick = r.reportOnce(ctx)
	if tick.err != nil || len(mem.Dispositions()) != 1 {
		t.Fatalf("orphan replay must dedupe disposition: %+v / %v", tick, mem.Dispositions())
	}
	notifies := 0
	for _, ev := range mem.Events() {
		if ev.Subject == events.SubjectNotifyRequest {
			notifies++
		}
	}
	if notifies != 1 {
		t.Fatalf("notify storm guard failed: %d", notifies)
	}
}

func TestReportOnceWithoutSourceIsDormant(t *testing.T) {
	mem := biz.NewMemStore()
	e := biz.NewEngine(mem, allowCheck{}, biz.Settings{}, log.DefaultLogger)
	r := &Runner{e: e, log: log.NewHelper(log.DefaultLogger)}
	tick := r.reportOnce(context.Background())
	if !tick.dormant || tick.err != nil {
		t.Fatalf("unwired channel must be dormant: %+v", tick)
	}
}
