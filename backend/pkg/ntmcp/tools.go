package ntmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	trpcmcp "trpc.group/trpc-go/trpc-mcp-go"

	"server/ent"
	"server/pkg/market"
)

// ============================================================================
// nt-mcp 工具面（三仓联动 P3，2026-11-02）：四个只读工具，映射三仓关联方案
// §L3 的 nt-mcp-server 工具集（query_market_snapshot / get_positions /
// list_trade_signals / get_backtest_report）。
//
// 纪律（与 HCF hcf-mcp / M14 同口径）：
//   - 只读——下单/改仓/审批类写操作一律走 M09 风控链与人工审批，不进 MCP；
//   - 「错误≠空列表」——仓储/Redis 错误落得工具 IsError 如实报，绝不塌成
//     空数组装活；无数据（零持仓/零信号）如实返回空 items + total=0；
//   - 投影即防火墙——ent 行类型不外泄，工具应答只含显式声明字段
//     （signal_reasoning 截断 240 rune；backtest result 大 JSON 不下发，只给
//     摘要列 + result_bytes 体积，防止 LLM 上下文被灌爆）；
//   - 入参一律不信任——页大小服务端钳制（MaxPageSize），book/side 枚举校验。
// ============================================================================

// ToolDeps 工具面依赖（窄端口：只读仓储 + 页大小上限）。
type ToolDeps struct {
	Snapshots   SnapshotsReader
	Positions   PositionsReader
	Signals     SignalsReader
	Reports     ReportsReader
	MaxPageSize int32
	Log         log.Logger
}

// SnapshotsReader 最新行情快照面（datahub 写 Redis snap:latest，market 同源口径）。
type SnapshotsReader interface {
	All(ctx context.Context) ([]market.Snapshot, error)
}

// PositionsReader 持仓与现金面（M09 positions / trade_cash，按 book 分账）。
type PositionsReader interface {
	ListPositions(ctx context.Context, book string) ([]*ent.Position, error)
	GetCash(ctx context.Context, book string) (*ent.TradeCash, bool, error)
}

// SignalFilter 信号列表查询条件（与 app/strategy biz.SignalFilter 同语义）。
type SignalFilter struct {
	Book   string
	Status string
	Side   string // buy / sell / hold；空 = 不限
	Day    *time.Time
	Limit  int
}

// SignalsReader 交易信号面（M06 trade_signals 只读投影）。
type SignalsReader interface {
	ListSignals(ctx context.Context, f SignalFilter) ([]*ent.TradeSignal, error)
}

// ReportsReader 回测报告面（M07 backtest_task + backtest_report）。
type ReportsReader interface {
	GetTask(ctx context.Context, id int) (*ent.BacktestTask, error)
	GetReport(ctx context.Context, taskID int) (*ent.BacktestReport, error)
}

// RegisterTools 按白名单装配工具（返回注册数——调用方据此断言装配完整）。
func RegisterTools(s *trpcmcp.Server, d ToolDeps, gates *ToolGates) int {
	n := 0
	if gates.MarketSnapshot {
		s.RegisterTool(trpcmcp.NewTool("query_market_snapshot",
			trpcmcp.WithDescription("NT 最新行情快照（datahub snap:latest 同源；symbols 过滤 + limit 截断；含昨收/最新价/量额/买卖五档/数据时效标记 stale）"),
		), d.handleQueryMarketSnapshot)
		n++
	}
	if gates.Positions {
		s.RegisterTool(trpcmcp.NewTool("get_positions",
			trpcmcp.WithDescription("NT 账户持仓与现金（book=paper 模拟盘|live 实盘；含持仓数量/可卖/成本/现价/浮盈与现金/日初权益；只读，不代表可交易授权）"),
		), d.handleGetPositions)
		n++
	}
	if gates.TradeSignals {
		s.RegisterTool(trpcmcp.NewTool("list_trade_signals",
			trpcmcp.WithDescription("NT 交易信号列表（M06 同源；book/status/side/day 过滤 + limit；含信号类型/价格带/评分/状态/平仓结果；推理摘要截断）"),
		), d.handleListTradeSignals)
		n++
	}
	if gates.BacktestReport {
		s.RegisterTool(trpcmcp.NewTool("get_backtest_report",
			trpcmcp.WithDescription("M07 回测报告摘要（task_id 查询；含任务态/收益/回撤/夏普/胜率/前瞻校验；result 大 JSON 只给体积不下发）"),
		), d.handleGetBacktestReport)
		n++
	}
	return n
}

// ============================== 入参 DTO ==============================

type marketSnapshotIn struct {
	Symbols []string `json:"symbols"`
	Limit   int32    `json:"limit"`
}

type getPositionsIn struct {
	Book string `json:"book"` // paper | live
}

type tradeSignalsIn struct {
	Book   string `json:"book"`
	Status string `json:"status"`
	Side   string `json:"side"`
	Day    string `json:"day"` // YYYY-MM-DD
	Limit  int32  `json:"limit"`
}

type backtestReportIn struct {
	TaskID int `json:"task_id"`
}

// ============================== 工具实现 ==============================

// handleQueryMarketSnapshot 最新行情快照（Redis snap:latest；stale 原样透传——
// 时效标记不下毒，消费方自己判断）。
func (d ToolDeps) handleQueryMarketSnapshot(ctx context.Context, req *trpcmcp.CallToolRequest) (*trpcmcp.CallToolResult, error) {
	var in marketSnapshotIn
	if err := unmarshalArgs(req, &in); err != nil {
		return trpcmcp.NewErrorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
	}
	all, err := d.Snapshots.All(ctx)
	if err != nil {
		return trpcmcp.NewErrorResult(fmt.Sprintf("market snapshot read failed: %v", err)), nil
	}
	want := map[string]bool{}
	for _, s := range in.Symbols {
		if t := strings.TrimSpace(s); t != "" {
			want[t] = true
		}
	}
	limit := in.Limit
	if limit <= 0 || limit > d.limit() {
		limit = d.limit()
	}
	items := make([]marketSnapshotOut, 0, len(all))
	for _, snap := range all {
		if len(want) > 0 && !want[snap.Symbol] {
			continue
		}
		items = append(items, snapshotOf(snap))
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Symbol < items[j].Symbol })
	truncated := false
	if len(items) > int(limit) {
		items = items[:limit]
		truncated = true
	}
	return jsonResult(marketSnapshotOutPage{
		Items: items, Total: len(all), Returned: len(items), Truncated: truncated,
		ServerTime: time.Now().UTC().Format(time.RFC3339),
	})
}

// handleGetPositions 持仓 + 现金（book 分账：paper 模拟盘 / live 实盘；M09 口径）。
func (d ToolDeps) handleGetPositions(ctx context.Context, req *trpcmcp.CallToolRequest) (*trpcmcp.CallToolResult, error) {
	var in getPositionsIn
	if err := unmarshalArgs(req, &in); err != nil {
		return trpcmcp.NewErrorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
	}
	book := strings.TrimSpace(in.Book)
	if book == "" {
		book = "paper"
	}
	if book != "paper" && book != "live" {
		return trpcmcp.NewErrorResult("book must be paper or live"), nil
	}
	rows, err := d.Positions.ListPositions(ctx, book)
	if err != nil {
		return trpcmcp.NewErrorResult(fmt.Sprintf("positions query failed: %v", err)), nil
	}
	cash, hasCash, err := d.Positions.GetCash(ctx, book)
	if err != nil {
		return trpcmcp.NewErrorResult(fmt.Sprintf("cash query failed: %v", err)), nil
	}
	items := make([]positionOut, 0, len(rows))
	for _, p := range rows {
		items = append(items, positionOut{
			Symbol: p.Symbol, Quantity: p.Quantity, Available: p.Available,
			AvgCost: f64Ptr(p.AvgCost), CurrentPrice: f64Ptr(p.CurrentPrice),
			Pnl: f64Ptr(p.Pnl), UpdatedAt: p.UpdatedAt.UTC().Format(time.RFC3339),
		})
	}
	out := positionsOut{Book: book, Positions: items, Total: len(items)}
	if hasCash {
		out.Cash = &cashOut{
			Cash: cash.Cash, RealizedPnl: cash.RealizedPnl,
			DayStartDate: cash.DayStartDate, DayStartEquity: cash.DayStartEquity,
			PaperDays: cash.PaperDays, OpenHalted: cash.OpenHalted,
			HaltReason: cash.HaltReason,
		}
	}
	return jsonResult(out)
}

// handleListTradeSignals 信号列表（M06 同源投影；推理截断，状态/方向/日期过滤）。
func (d ToolDeps) handleListTradeSignals(ctx context.Context, req *trpcmcp.CallToolRequest) (*trpcmcp.CallToolResult, error) {
	var in tradeSignalsIn
	if err := unmarshalArgs(req, &in); err != nil {
		return trpcmcp.NewErrorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
	}
	f := SignalFilter{
		Book:   strings.TrimSpace(in.Book),
		Status: strings.TrimSpace(in.Status),
		Side:   strings.TrimSpace(in.Side),
		Limit:  int(clampLimit(in.Limit, d.limit())),
	}
	if f.Book == "" {
		f.Book = "paper"
	}
	if f.Book != "paper" && f.Book != "live" {
		return trpcmcp.NewErrorResult("book must be paper or live"), nil
	}
	if f.Side != "" && f.Side != "buy" && f.Side != "sell" && f.Side != "hold" {
		return trpcmcp.NewErrorResult("side must be buy, sell or hold"), nil
	}
	if d := strings.TrimSpace(in.Day); d != "" {
		t, err := time.Parse("2006-01-02", d)
		if err != nil {
			return trpcmcp.NewErrorResult(fmt.Sprintf("invalid day %q (want YYYY-MM-DD)", d)), nil
		}
		f.Day = &t
	}
	rows, err := d.Signals.ListSignals(ctx, f)
	if err != nil {
		return trpcmcp.NewErrorResult(fmt.Sprintf("trade signals query failed: %v", err)), nil
	}
	items := make([]tradeSignalOut, 0, len(rows))
	for _, s := range rows {
		items = append(items, signalOf(s))
	}
	return jsonResult(pageOut[tradeSignalOut]{Items: items, Total: len(items), Page: 1, PageSize: f.Limit})
}

// handleGetBacktestReport 回测报告摘要（task + 报告摘要列 + result 体积；
// 大 JSON 不下发——需要明细走 M07 报告页/人工审批面）。
func (d ToolDeps) handleGetBacktestReport(ctx context.Context, req *trpcmcp.CallToolRequest) (*trpcmcp.CallToolResult, error) {
	var in backtestReportIn
	if err := unmarshalArgs(req, &in); err != nil {
		return trpcmcp.NewErrorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
	}
	if in.TaskID <= 0 {
		return trpcmcp.NewErrorResult("task_id is required (positive integer)"), nil
	}
	task, err := d.Reports.GetTask(ctx, in.TaskID)
	if err != nil {
		return trpcmcp.NewErrorResult(fmt.Sprintf("backtest task lookup failed: %v", err)), nil
	}
	report, err := d.Reports.GetReport(ctx, in.TaskID)
	if err != nil {
		return trpcmcp.NewErrorResult(fmt.Sprintf("backtest report lookup failed: %v", err)), nil
	}
	out := backtestReportOut{
		Task: taskOut{
			TaskID: task.ID, Kind: task.Kind, Name: task.Name, Strategy: task.Strategy,
			Freq: task.Freq, StartDate: task.StartDate.Format("2006-01-02"),
			EndDate: task.EndDate.Format("2006-01-02"), Status: task.Status,
			Progress: task.Progress, FinishedAt: rfc3339Ptr(task.FinishedAt),
		},
		Report: reportSummaryOut{
			TotalReturn: report.TotalReturn, AnnualReturn: report.AnnualReturn,
			MaxDrawdown: report.MaxDrawdown, Sharpe: report.Sharpe,
			WinRate: report.WinRate, ProfitFactor: report.ProfitFactor,
			TradeCount: report.TradeCount, LookaheadPass: report.LookaheadPass,
			OOSDecay: report.OosDecay, FillsHash: report.FillsHash,
			ResultBytes: len(report.Result),
			CreatedAt:   report.CreatedAt.UTC().Format(time.RFC3339),
		},
	}
	return jsonResult(out)
}

// ============================== 出参 DTO（协议防火墙） ==============================

type pageOut[T any] struct {
	Items    []T   `json:"items"`
	Total    int   `json:"total"`
	Page     int32 `json:"page"`
	PageSize int   `json:"page_size"`
}

type marketSnapshotOutPage struct {
	Items      []marketSnapshotOut `json:"items"`
	Total      int                 `json:"total"`
	Returned   int                 `json:"returned"`
	Truncated  bool                `json:"truncated"`
	ServerTime string              `json:"server_time"`
}

type marketSnapshotOut struct {
	Symbol   string       `json:"symbol"`
	Time     string       `json:"time"`
	AsOf     string       `json:"as_of"`
	Source   string       `json:"source"`
	Stale    bool         `json:"stale"`
	PreClose float64      `json:"pre_close"`
	Open     float64      `json:"open"`
	High     float64      `json:"high"`
	Low      float64      `json:"low"`
	Last     float64      `json:"last"`
	Volume   int64        `json:"volume"`
	Amount   float64      `json:"amount"`
	Bid      [][2]float64 `json:"bid,omitempty"`
	Ask      [][2]float64 `json:"ask,omitempty"`
}

func snapshotOf(s market.Snapshot) marketSnapshotOut {
	return marketSnapshotOut{
		Symbol: s.Symbol, Time: s.Time.UTC().Format(time.RFC3339),
		AsOf: s.AsOf.UTC().Format(time.RFC3339), Source: s.Source, Stale: s.Stale,
		PreClose: s.PreClose, Open: s.Open, High: s.High, Low: s.Low,
		Last: s.Last, Volume: s.Volume, Amount: s.Amount, Bid: s.Bid, Ask: s.Ask,
	}
}

type positionsOut struct {
	Book      string        `json:"book"`
	Total     int           `json:"total"`
	Cash      *cashOut      `json:"cash,omitempty"`
	Positions []positionOut `json:"positions"`
}

type cashOut struct {
	Cash           float64 `json:"cash"`
	RealizedPnl    float64 `json:"realized_pnl"`
	DayStartDate   string  `json:"day_start_date"`
	DayStartEquity float64 `json:"day_start_equity"`
	PaperDays      int     `json:"paper_days"`
	OpenHalted     bool    `json:"open_halted"`
	HaltReason     string  `json:"halt_reason,omitempty"`
}

type positionOut struct {
	Symbol       string  `json:"symbol"`
	Quantity     int     `json:"quantity"`
	Available    int     `json:"available"`
	AvgCost      float64 `json:"avg_cost"`
	CurrentPrice float64 `json:"current_price"`
	Pnl          float64 `json:"pnl"`
	UpdatedAt    string  `json:"updated_at"`
}

type tradeSignalOut struct {
	ID             int      `json:"id"`
	Book           string   `json:"book"`
	StockCode      string   `json:"stock_code"`
	StockName      string   `json:"stock_name"`
	SignalType     string   `json:"signal_type"`
	Status         string   `json:"status"`
	SignalTime     string   `json:"signal_time"`
	TradeDate      string   `json:"trade_date,omitempty"`
	Strategy       string   `json:"strategy"`
	EntryPrice     float64  `json:"entry_price"`
	EntryLow       float64  `json:"entry_low"`
	EntryHigh      float64  `json:"entry_high"`
	StopLoss       float64  `json:"stop_loss"`
	TakeProfit     float64  `json:"take_profit"`
	PositionPct    float64  `json:"position_pct"`
	Confidence     float64  `json:"confidence_score"`
	Reasoning      string   `json:"reasoning"`
	ValidUntil     string   `json:"valid_until,omitempty"`
	ExecutedPrice  float64  `json:"executed_price,omitempty"`
	ExecutedVolume int      `json:"executed_volume,omitempty"`
	ExitPrice      float64  `json:"exit_price,omitempty"`
	PnlPct         float64  `json:"pnl_pct,omitempty"`
	ClosedAt       string   `json:"closed_at,omitempty"`
	Evidence       []string `json:"evidence,omitempty"`
	TraceID        string   `json:"trace_id,omitempty"`
}

func signalOf(s *ent.TradeSignal) tradeSignalOut {
	out := tradeSignalOut{
		ID: s.ID, Book: s.Book, StockCode: s.StockCode, StockName: s.StockName,
		SignalType: string(s.SignalType), Status: s.Status,
		SignalTime: s.SignalTime.UTC().Format(time.RFC3339),
		Strategy:   s.Strategy, EntryPrice: s.EntryPrice,
		StopLoss: s.StopLoss, TakeProfit: s.TakeProfit,
		PositionPct: s.PositionPct, Confidence: s.ConfidenceScore,
		Reasoning: truncateRunes(s.SignalReasoning, 240),
		Evidence:  s.Evidence, TraceID: s.TraceID,
	}
	if s.TradeDate != nil {
		out.TradeDate = s.TradeDate.Format("2006-01-02")
	}
	if s.EntryLow != nil {
		out.EntryLow = *s.EntryLow
	}
	if s.EntryHigh != nil {
		out.EntryHigh = *s.EntryHigh
	}
	if s.ValidUntil != nil {
		out.ValidUntil = s.ValidUntil.UTC().Format(time.RFC3339)
	}
	if s.ExecutedPrice != nil {
		out.ExecutedPrice = *s.ExecutedPrice
	}
	if s.ExecutedVolume != nil {
		out.ExecutedVolume = *s.ExecutedVolume
	}
	if s.ExitPrice != nil {
		out.ExitPrice = *s.ExitPrice
	}
	if s.PnlPct != nil {
		out.PnlPct = *s.PnlPct
	}
	if s.ClosedAt != nil {
		out.ClosedAt = s.ClosedAt.UTC().Format(time.RFC3339)
	}
	return out
}

type backtestReportOut struct {
	Task   taskOut          `json:"task"`
	Report reportSummaryOut `json:"report"`
}

type taskOut struct {
	TaskID     int     `json:"task_id"`
	Kind       string  `json:"kind"`
	Name       string  `json:"name"`
	Strategy   string  `json:"strategy"`
	Freq       string  `json:"freq"`
	StartDate  string  `json:"start_date"`
	EndDate    string  `json:"end_date"`
	Status     string  `json:"status"`
	Progress   float64 `json:"progress"`
	FinishedAt string  `json:"finished_at,omitempty"`
}

type reportSummaryOut struct {
	TotalReturn   float64 `json:"total_return"`
	AnnualReturn  float64 `json:"annual_return"`
	MaxDrawdown   float64 `json:"max_drawdown"`
	Sharpe        float64 `json:"sharpe"`
	WinRate       float64 `json:"win_rate"`
	ProfitFactor  float64 `json:"profit_factor"`
	TradeCount    int     `json:"trade_count"`
	LookaheadPass *bool   `json:"lookahead_pass"`
	OOSDecay      bool    `json:"oos_decay"`
	FillsHash     string  `json:"fills_hash,omitempty"`
	ResultBytes   int     `json:"result_bytes"`
	CreatedAt     string  `json:"created_at"`
}

// ============================== 工具函数 ==============================

func (d ToolDeps) limit() int32 {
	if d.MaxPageSize > 0 {
		return d.MaxPageSize
	}
	return 50
}

// clampLimit 条数钳制（limit∈[1,max]——MCP 入参来自 LLM，一律不信任）。
func clampLimit(limit, max int32) int32 {
	if limit <= 0 || limit > max {
		return max
	}
	return limit
}

// unmarshalArgs Arguments(map) → typed struct（json.Unmarshal：未知键忽略）。
func unmarshalArgs(req *trpcmcp.CallToolRequest, out any) error {
	raw, err := json.Marshal(req.Params.Arguments)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

// jsonResult 成功结果（JSON 文本载荷）。
func jsonResult(v any) (*trpcmcp.CallToolResult, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return trpcmcp.NewErrorResult(fmt.Sprintf("encode result: %v", err)), nil
	}
	return trpcmcp.NewTextResult(string(raw)), nil
}

func f64Ptr(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

func rfc3339Ptr(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// truncateRunes 摘要截断（rune 感知，防多字节截断乱码）。
func truncateRunes(s string, max int) string {
	rs := []rune(s)
	if len(rs) <= max {
		return s
	}
	return string(rs[:max]) + "…"
}
