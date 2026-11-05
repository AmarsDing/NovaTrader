package ntmcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	trpcmcp "trpc.group/trpc-go/trpc-mcp-go"

	"server/ent"
	"server/ent/tradesignal"
	"server/pkg/market"
)

// ============================================================================
// nt-mcp 工具面单测（P3 测试组）：
//   - 四工具 happy path 投影（出参 DTO 即协议防火墙，字段完备性钉死在断言里）;
//   - 「错误≠空列表」——仓储/Redis 错误落得 IsError，绝不塌成空数组装活；
//   - 入参枚举校验（book/side/day/task_id）与 limit 钳制、推理 240 rune 截断；
//   - RegisterTools 白名单装配计数。
// ============================================================================

// ---------------- fake 仓储 ----------------

type fakeSnapshots struct {
	allFn func(ctx context.Context) ([]market.Snapshot, error)
}

func (f *fakeSnapshots) All(ctx context.Context) ([]market.Snapshot, error) {
	return f.allFn(ctx)
}

type fakePositions struct {
	lastBook string
	listFn   func(ctx context.Context, book string) ([]*ent.Position, error)
	cashFn   func(ctx context.Context, book string) (*ent.TradeCash, bool, error)
}

func (f *fakePositions) ListPositions(ctx context.Context, book string) ([]*ent.Position, error) {
	f.lastBook = book
	return f.listFn(ctx, book)
}
func (f *fakePositions) GetCash(ctx context.Context, book string) (*ent.TradeCash, bool, error) {
	return f.cashFn(ctx, book)
}

type fakeSignals struct {
	lastFilter SignalFilter
	listFn     func(ctx context.Context, f SignalFilter) ([]*ent.TradeSignal, error)
}

func (f *fakeSignals) ListSignals(ctx context.Context, fl SignalFilter) ([]*ent.TradeSignal, error) {
	f.lastFilter = fl
	return f.listFn(ctx, fl)
}

type fakeReports struct {
	taskFn   func(ctx context.Context, id int) (*ent.BacktestTask, error)
	reportFn func(ctx context.Context, taskID int) (*ent.BacktestReport, error)
}

func (f *fakeReports) GetTask(ctx context.Context, id int) (*ent.BacktestTask, error) {
	return f.taskFn(ctx, id)
}
func (f *fakeReports) GetReport(ctx context.Context, taskID int) (*ent.BacktestReport, error) {
	return f.reportFn(ctx, taskID)
}

// ---------------- 断言辅助 ----------------

func newReq(t *testing.T, name string, args map[string]interface{}) *trpcmcp.CallToolRequest {
	t.Helper()
	return &trpcmcp.CallToolRequest{Params: trpcmcp.CallToolParams{Name: name, Arguments: args}}
}

func resultText(t *testing.T, res *trpcmcp.CallToolResult) string {
	t.Helper()
	if len(res.Content) == 0 {
		t.Fatalf("result content empty")
	}
	tc, ok := res.Content[0].(trpcmcp.TextContent)
	if !ok {
		t.Fatalf("content[0] not TextContent: %T", res.Content[0])
	}
	return tc.Text
}

func f64(v float64) *float64 { return &v }

// ---------------- query_market_snapshot ----------------

func TestHandleQueryMarketSnapshot_HappyPath(t *testing.T) {
	ts := time.Date(2026, 11, 2, 10, 30, 0, 0, time.UTC)
	deps := ToolDeps{
		Snapshots: &fakeSnapshots{allFn: func(ctx context.Context) ([]market.Snapshot, error) {
			return []market.Snapshot{
				{Symbol: "600519.SH", Time: ts, AsOf: ts, Source: "tushare",
					PreClose: 1500, Open: 1510, High: 1525, Low: 1505, Last: 1520,
					Volume: 1234567, Amount: 1876543.21, Bid: [][2]float64{{1520, 100}}, Ask: [][2]float64{{1521, 200}}},
				{Symbol: "000001.SZ", Time: ts, AsOf: ts, Source: "tushare", Stale: true, PreClose: 10, Last: 10.5},
			}, nil
		}},
		MaxPageSize: 50,
	}

	res, err := deps.handleQueryMarketSnapshot(context.Background(),
		newReq(t, "query_market_snapshot", map[string]interface{}{"limit": 10}))
	if err != nil || res.IsError {
		t.Fatalf("unexpected error: %v / %s", err, resultText(t, res))
	}
	var out marketSnapshotOutPage
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Total != 2 || out.Returned != 2 || out.Truncated {
		t.Errorf("page meta = %+v, want total=2 returned=2", out)
	}
	if out.ServerTime == "" {
		t.Errorf("server_time must be set")
	}
	// 排序确定性（symbol 升序）。
	if out.Items[0].Symbol != "000001.SZ" || out.Items[1].Symbol != "600519.SH" {
		t.Errorf("sort order = %v/%v", out.Items[0].Symbol, out.Items[1].Symbol)
	}
	m := out.Items[1]
	if m.Last != 1520 || m.Volume != 1234567 || m.Bid[0][0] != 1520 || m.Time != "2026-11-02T10:30:00Z" {
		t.Errorf("projection mismatch: %+v", m)
	}
	if !out.Items[0].Stale {
		t.Errorf("stale flag must pass through (时效标记不下毒)")
	}
}

func TestHandleQueryMarketSnapshot_SymbolFilterAndClamp(t *testing.T) {
	deps := ToolDeps{
		Snapshots: &fakeSnapshots{allFn: func(ctx context.Context) ([]market.Snapshot, error) {
			return []market.Snapshot{
				{Symbol: "600519.SH", Time: time.Now()}, {Symbol: "000001.SZ", Time: time.Now()},
			}, nil
		}},
		MaxPageSize: 50,
	}
	res, err := deps.handleQueryMarketSnapshot(context.Background(),
		newReq(t, "query_market_snapshot", map[string]interface{}{"symbols": []string{" 600519.SH ", ""}, "limit": 99999}))
	if err != nil || res.IsError {
		t.Fatalf("unexpected error: %v / %s", err, resultText(t, res))
	}
	var out marketSnapshotOutPage
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Returned != 1 || out.Items[0].Symbol != "600519.SH" {
		t.Errorf("symbol filter = %+v", out)
	}
}

func TestHandleQueryMarketSnapshot_ErrorNotSilenced(t *testing.T) {
	deps := ToolDeps{
		Snapshots: &fakeSnapshots{allFn: func(ctx context.Context) ([]market.Snapshot, error) {
			return nil, errors.New("redis connection refused")
		}},
		MaxPageSize: 50,
	}
	res, err := deps.handleQueryMarketSnapshot(context.Background(), newReq(t, "query_market_snapshot", nil))
	if err != nil {
		t.Fatalf("handler must not return go error, got %v", err)
	}
	if !res.IsError || !strings.Contains(resultText(t, res), "redis connection refused") {
		t.Errorf("read error must surface as IsError (错误≠空列表), got %q", resultText(t, res))
	}
}

// ---------------- get_positions ----------------

func TestHandleGetPositions_HappyPath(t *testing.T) {
	updated := time.Date(2026, 11, 2, 9, 35, 0, 0, time.UTC)
	pos := &fakePositions{
		listFn: func(ctx context.Context, book string) ([]*ent.Position, error) {
			return []*ent.Position{
				{Symbol: "600519.SH", Quantity: 100, Available: 100, AvgCost: f64(1500), CurrentPrice: f64(1520), Pnl: f64(2000), UpdatedAt: updated},
				{Symbol: "000001.SZ", Quantity: 0, Available: 0},
			}, nil
		},
		cashFn: func(ctx context.Context, book string) (*ent.TradeCash, bool, error) {
			return &ent.TradeCash{Cash: 500000.5, RealizedPnl: 12000.25, DayStartDate: "2026-11-02", DayStartEquity: 660000.75, PaperDays: 30, OpenHalted: false}, true, nil
		},
	}
	deps := ToolDeps{Positions: pos, MaxPageSize: 50}

	res, err := deps.handleGetPositions(context.Background(),
		newReq(t, "get_positions", map[string]interface{}{"book": "paper"}))
	if err != nil || res.IsError {
		t.Fatalf("unexpected error: %v / %s", err, resultText(t, res))
	}
	var out positionsOut
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Book != "paper" || out.Total != 2 || len(out.Positions) != 2 {
		t.Fatalf("page meta = %+v", out)
	}
	p := out.Positions[0]
	if p.Symbol != "600519.SH" || p.Quantity != 100 || p.AvgCost != 1500 || p.Pnl != 2000 ||
		p.UpdatedAt != "2026-11-02T09:35:00Z" {
		t.Errorf("projection mismatch: %+v", p)
	}
	if out.Cash == nil || out.Cash.Cash != 500000.5 || out.Cash.DayStartEquity != 660000.75 || out.Cash.PaperDays != 30 {
		t.Errorf("cash projection = %+v", out.Cash)
	}
	if pos.lastBook != "paper" {
		t.Errorf("book passthrough = %q", pos.lastBook)
	}
}

func TestHandleGetPositions_DefaultPaperAndNoCashRow(t *testing.T) {
	pos := &fakePositions{
		listFn: func(ctx context.Context, book string) ([]*ent.Position, error) { return nil, nil },
		cashFn: func(ctx context.Context, book string) (*ent.TradeCash, bool, error) {
			return nil, false, nil
		},
	}
	deps := ToolDeps{Positions: pos, MaxPageSize: 50}
	res, err := deps.handleGetPositions(context.Background(), newReq(t, "get_positions", nil))
	if err != nil || res.IsError {
		t.Fatalf("unexpected error: %v / %s", err, resultText(t, res))
	}
	var out positionsOut
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Book != "paper" || out.Total != 0 || out.Cash != nil {
		t.Errorf("default book + missing cash row = %+v (缺行≠零现金，cash 必须缺省)", out)
	}
}

func TestHandleGetPositions_BookValidationAndErrorNotSilenced(t *testing.T) {
	deps := ToolDeps{Positions: &fakePositions{
		listFn: func(ctx context.Context, book string) ([]*ent.Position, error) {
			return nil, errors.New("pg connection lost")
		},
		cashFn: func(ctx context.Context, book string) (*ent.TradeCash, bool, error) {
			return nil, false, nil
		},
	}, MaxPageSize: 50}

	res, _ := deps.handleGetPositions(context.Background(),
		newReq(t, "get_positions", map[string]interface{}{"book": "paper2"}))
	if !res.IsError || !strings.Contains(resultText(t, res), "paper or live") {
		t.Errorf("invalid book must be param error, got %q", resultText(t, res))
	}

	res, err := deps.handleGetPositions(context.Background(), newReq(t, "get_positions", nil))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !res.IsError || !strings.Contains(resultText(t, res), "pg connection lost") {
		t.Errorf("repo error must surface, got %q", resultText(t, res))
	}
}

// ---------------- list_trade_signals ----------------

func TestHandleListTradeSignals_HappyPathAndFilterPassthrough(t *testing.T) {
	sigTime := time.Date(2026, 11, 2, 9, 45, 0, 0, time.UTC)
	tradeDate := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	validUntil := sigTime.Add(48 * time.Hour)
	sig := &fakeSignals{listFn: func(ctx context.Context, f SignalFilter) ([]*ent.TradeSignal, error) {
		return []*ent.TradeSignal{{
			ID: 101, Book: "paper", SignalTime: sigTime, StockCode: "600519.SH", StockName: "贵州茅台",
			SignalType: tradesignal.SignalType("buy"), EntryPrice: 1520, StopLoss: 1480, TakeProfit: 1580,
			SignalReasoning: strings.Repeat("理", 300), ConfidenceScore: 78.5, Status: "pending",
			TradeDate: &tradeDate, Strategy: "first_board_weak2strong", PositionPct: 0.1,
			ValidUntil: &validUntil, Evidence: []string{"intel_abc"}, TraceID: "tr_101",
		}}, nil
	}}
	deps := ToolDeps{Signals: sig, MaxPageSize: 50}

	res, err := deps.handleListTradeSignals(context.Background(), newReq(t, "list_trade_signals",
		map[string]interface{}{"book": "paper", "status": "pending", "side": "buy", "day": "2026-11-02", "limit": 20}))
	if err != nil || res.IsError {
		t.Fatalf("unexpected error: %v / %s", err, resultText(t, res))
	}
	var out pageOut[tradeSignalOut]
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Total != 1 || len(out.Items) != 1 {
		t.Fatalf("page = %+v", out)
	}
	it := out.Items[0]
	if it.ID != 101 || it.StockCode != "600519.SH" || it.SignalType != "buy" || it.Status != "pending" ||
		it.Strategy != "first_board_weak2strong" || it.Confidence != 78.5 || it.TradeDate != "2026-11-02" {
		t.Errorf("projection mismatch: %+v", it)
	}
	if n := len([]rune(it.Reasoning)); n != 241 {
		t.Errorf("reasoning truncation = %d runes, want 241", n)
	}
	if it.ValidUntil != "2026-11-04T09:45:00Z" || it.TraceID != "tr_101" {
		t.Errorf("time/trace projection = %q / %q", it.ValidUntil, it.TraceID)
	}
	// 过滤条件透传验证。
	f := sig.lastFilter
	if f.Book != "paper" || f.Status != "pending" || f.Side != "buy" || f.Limit != 20 || f.Day == nil {
		t.Errorf("filter passthrough = %+v", f)
	}
}

func TestHandleListTradeSignals_ClampAndValidation(t *testing.T) {
	sig := &fakeSignals{listFn: func(ctx context.Context, f SignalFilter) ([]*ent.TradeSignal, error) {
		return nil, nil
	}}
	deps := ToolDeps{Signals: sig, MaxPageSize: 50}

	// limit 超上限钳到 50。
	if _, err := deps.handleListTradeSignals(context.Background(),
		newReq(t, "list_trade_signals", map[string]interface{}{"limit": 100000})); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if sig.lastFilter.Limit != 50 {
		t.Errorf("limit clamp = %d, want 50", sig.lastFilter.Limit)
	}
	// 非法 side / day / book 一律参数错误。
	for _, args := range []map[string]interface{}{
		{"side": "short"}, {"day": "2026/11/02"}, {"book": "shadow"},
	} {
		res, _ := deps.handleListTradeSignals(context.Background(), newReq(t, "list_trade_signals", args))
		if !res.IsError {
			t.Errorf("args %v must be param error", args)
		}
	}
}

func TestHandleListTradeSignals_ErrorNotSilenced(t *testing.T) {
	deps := ToolDeps{Signals: &fakeSignals{listFn: func(ctx context.Context, f SignalFilter) ([]*ent.TradeSignal, error) {
		return nil, errors.New("trade_signals table scan timeout")
	}}, MaxPageSize: 50}
	res, err := deps.handleListTradeSignals(context.Background(), newReq(t, "list_trade_signals", nil))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !res.IsError || !strings.Contains(resultText(t, res), "scan timeout") {
		t.Errorf("repo error must surface, got %q", resultText(t, res))
	}
}

// ---------------- get_backtest_report ----------------

func TestHandleGetBacktestReport_HappyPath(t *testing.T) {
	fin := time.Date(2026, 10, 31, 11, 0, 0, 0, time.UTC)
	rep := &fakeReports{
		taskFn: func(ctx context.Context, id int) (*ent.BacktestTask, error) {
			return &ent.BacktestTask{
				ID: id, Kind: "single", Name: "首板战法十月回测", Strategy: "first_board",
				Freq: "1d", StartDate: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
				EndDate: time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC),
				Status: "succeeded", Progress: 1, FinishedAt: &fin,
			}, nil
		},
		reportFn: func(ctx context.Context, taskID int) (*ent.BacktestReport, error) {
			return &ent.BacktestReport{
				TaskID: taskID, TotalReturn: 0.182, AnnualReturn: 0.85, MaxDrawdown: 0.062,
				Sharpe: 1.85, WinRate: 0.58, ProfitFactor: 1.62, TradeCount: 24,
				OosDecay: false, FillsHash: "abc123",
				Result:    json.RawMessage(strings.Repeat("x", 4096)), // 大 JSON 只计体积
				CreatedAt: fin,
			}, nil
		},
	}
	deps := ToolDeps{Reports: rep, MaxPageSize: 50}

	res, err := deps.handleGetBacktestReport(context.Background(),
		newReq(t, "get_backtest_report", map[string]interface{}{"task_id": 7}))
	if err != nil || res.IsError {
		t.Fatalf("unexpected error: %v / %s", err, resultText(t, res))
	}
	var out backtestReportOut
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Task.TaskID != 7 || out.Task.Name != "首板战法十月回测" || out.Task.Status != "succeeded" ||
		out.Task.StartDate != "2026-10-01" || out.Task.FinishedAt != "2026-10-31T11:00:00Z" {
		t.Errorf("task projection = %+v", out.Task)
	}
	if out.Report.TotalReturn != 0.182 || out.Report.Sharpe != 1.85 || out.Report.TradeCount != 24 ||
		out.Report.ResultBytes != 4096 || out.Report.FillsHash != "abc123" {
		t.Errorf("report projection = %+v", out.Report)
	}
}

func TestHandleGetBacktestReport_TaskIDRequired_AndLookupError(t *testing.T) {
	rep := &fakeReports{
		taskFn:   func(ctx context.Context, id int) (*ent.BacktestTask, error) { return nil, errors.New("task not found") },
		reportFn: func(ctx context.Context, id int) (*ent.BacktestReport, error) { return nil, nil },
	}
	deps := ToolDeps{Reports: rep, MaxPageSize: 50}

	res, _ := deps.handleGetBacktestReport(context.Background(), newReq(t, "get_backtest_report", nil))
	if !res.IsError || !strings.Contains(resultText(t, res), "task_id is required") {
		t.Errorf("missing task_id must be param error, got %q", resultText(t, res))
	}

	res, err := deps.handleGetBacktestReport(context.Background(),
		newReq(t, "get_backtest_report", map[string]interface{}{"task_id": 999}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !res.IsError || !strings.Contains(resultText(t, res), "task not found") {
		t.Errorf("lookup miss must surface as tool error (错误≠空报表), got %q", resultText(t, res))
	}
}

// ---------------- 装配白名单 ----------------

func TestRegisterTools_GateCounting(t *testing.T) {
	d := ToolDeps{MaxPageSize: 50}
	all := &ToolGates{MarketSnapshot: true, Positions: true, TradeSignals: true, BacktestReport: true}

	s := trpcmcp.NewServer("t", "0.0.1")
	if n := RegisterTools(s, d, all); n != 4 {
		t.Errorf("all gates => 4 tools, got %d", n)
	}
	s2 := trpcmcp.NewServer("t", "0.0.1")
	if n := RegisterTools(s2, d, &ToolGates{MarketSnapshot: true}); n != 1 {
		t.Errorf("single gate => 1 tool, got %d", n)
	}
	s3 := trpcmcp.NewServer("t", "0.0.1")
	if n := RegisterTools(s3, d, &ToolGates{}); n != 0 {
		t.Errorf("no gates => 0 tools, got %d", n)
	}
}

// ---------------- 纯函数 ----------------

func TestClampLimit(t *testing.T) {
	cases := []struct{ limit, max, want int32 }{
		{0, 50, 50}, {-1, 50, 50}, {10, 50, 10}, {500, 50, 50}, {50, 50, 50},
	}
	for _, c := range cases {
		if got := clampLimit(c.limit, c.max); got != c.want {
			t.Errorf("clampLimit(%d,%d)=%d, want %d", c.limit, c.max, got, c.want)
		}
	}
}

func TestTruncateRunes(t *testing.T) {
	got := truncateRunes(strings.Repeat("汉", 300), 240)
	if n := len([]rune(got)); n != 241 || !strings.HasSuffix(got, "…") {
		t.Fatalf("truncate = %d runes", n)
	}
	if strings.ContainsRune(got, '�') {
		t.Fatalf("rune-unsafe truncation")
	}
	if s := truncateRunes("short", 240); s != "short" {
		t.Fatalf("short string passthrough")
	}
}
