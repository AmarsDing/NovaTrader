package biz

import (
	"context"
	"time"
)

// Bar 是一根不复权日线。AdjFactor 为后复权因子。
type Bar struct {
	Time      time.Time
	Open      float64
	High      float64
	Low       float64
	Close     float64
	Volume    int64
	Amount    float64
	AdjFactor float64
}

type StockInfo struct {
	Symbol   string
	Name     string
	Industry string
	Concept  string
	CircMV   *float64
	PE       *float64
}

type Holding struct {
	Book      string
	Symbol    string
	Quantity  int
	Available int
	AvgCost   *float64
	Price     *float64
	PnL       *float64
}

// MarketRepo 读事实包所需的行情、情报和持仓。查不到时返回空值，不返回错误。
type MarketRepo interface {
	Stock(ctx context.Context, symbol string) (*StockInfo, error)
	// DailyBars 返回 asOf 及以前最近 n 根日线，按时间升序。
	DailyBars(ctx context.Context, symbol string, asOf time.Time, n int) ([]Bar, error)
	StockNews(ctx context.Context, symbol string, from, to time.Time, limit int) ([]Intel, error)
	MarketNews(ctx context.Context, from, to time.Time, minImportance, limit int) ([]Intel, error)
	Holdings(ctx context.Context) ([]Holding, error)
	// Factors 返回 asOf 及以前各截面最新一行合并后的因子。没有时返回 nil, nil。
	Factors(ctx context.Context, symbol string, asOf time.Time) (map[string]float64, error)
	// Phase 返回 asOf 及以前最新的情绪阶段。没有时返回空字符串。
	Phase(ctx context.Context, asOf time.Time) (string, error)
}

type DecisionRecord struct {
	Kind          string
	Symbol        string
	TraceID       string
	Content       map[string]any
	Confidence    *float64
	PromptVersion string
	Model         string
	Degraded      bool
	Discarded     bool
	LatencyMS     int
}

// DecisionRepo 写 agent_decisions。先 Begin 拿到编号，模型调用记录才能挂到这次研判上。
type DecisionRepo interface {
	Begin(ctx context.Context, kind, symbol, traceID string) (int, error)
	Finish(ctx context.Context, id int, rec DecisionRecord) error
	Get(ctx context.Context, id int) (*DecisionRecord, error)
}

// BriefingRepo 写 morning_briefings。Save 在同一事务里写 outbox 事件。
type BriefingRepo interface {
	Save(ctx context.Context, b *Briefing) error
	// Get 查不到时返回 nil, nil。
	Get(ctx context.Context, day time.Time) (*Briefing, error)
}

type Progress interface {
	Publish(ctx context.Context, ev ProgressEvent)
}

// ReviewRepo 收盘定稿与程序版复盘的数据端口。pool/日线来自共享表（M01/M02），
// 冻结与复盘写 emotion_day_frozen、daily_reviews 两张 M05 表。
type ReviewRepo interface {
	// PoolOn 查某日 limit_pool 全量。没有记录返回空切片，不返回错误。
	PoolOn(ctx context.Context, day time.Time) ([]PoolRow, error)
	// BarOn 查 symbols 在 day 的日线收盘与前收。缺失的 symbol 不进 map。
	BarOn(ctx context.Context, symbols []string, day time.Time) (map[string]TierBar, error)
	// UpsertFrozen 按 trade_date 覆盖写定稿行。
	UpsertFrozen(ctx context.Context, f *Frozen) error
	// FrozenOn 没有记录返回 nil, nil。
	FrozenOn(ctx context.Context, day time.Time) (*Frozen, error)
	// SentimentOn 查某日 market_sentiment 截面。没有返回 nil, nil。
	SentimentOn(ctx context.Context, day time.Time) (*SentimentRow, error)
	// SentimentAmounts 返回 day 及以前最近 n 天的 market_sentiment 成交额（按日升序）。
	SentimentAmounts(ctx context.Context, day time.Time, n int) ([]float64, error)
	// MarginTotal 全市场融资融券余额合计。第二个返回值 false 表示没取到。
	MarginTotal(ctx context.Context, day time.Time) (float64, bool, error)
	// SaveProgramReview 写 daily_reviews(kind=program)，同日覆盖。
	SaveProgramReview(ctx context.Context, day time.Time, content string, stats map[string]any) error
	// SaveProgramReviewWithEvent 同 SaveProgramReview，但同一事务里另写一条 outbox 事件
	// （主题 strategy.review，载荷 date/title/body，traceID 填进信封）——M13-T07② 日复盘衔接 M10。
	SaveProgramReviewWithEvent(ctx context.Context, day time.Time, content string, stats map[string]any, traceID, title string) error
	// SignalsOnDays 查交易日 ∈ days 的终态（done/closed）买入信号（M13-T08 信号级对账）。
	// trade_date 为 NULL 的行按 signal_time 的上海日期回退匹配。没有返回空切片，不返回错误。
	SignalsOnDays(ctx context.Context, days []time.Time) ([]SignalRow, error)
	// UpsertPredictionCheck 按 (check_date, source) 覆盖写一日对账。
	UpsertPredictionCheck(ctx context.Context, c *Check) error
	// PredictionChecks 返回最近 n 日对账战绩，按 check_date 倒序。
	PredictionChecks(ctx context.Context, n int) ([]*Check, error)
	// PredictionCheckOn 查某日某来源的对账行。没有返回 nil, nil。
	PredictionCheckOn(ctx context.Context, checkDay time.Time, source string) (*Check, error)
	// SaveReview 写 daily_reviews(指定 kind)，同日同 kind 覆盖。
	SaveReview(ctx context.Context, day time.Time, kind, content string, stats map[string]any, lessons []string, model string) error
	// SaveReviewWithEvent 同 SaveReview，但同一事务里另写一条 outbox 事件（主题 strategy.review，
	// 载荷 date/title/body，traceID 填进信封）——P1-7b 周报衔接 M10 通知与桌面推送。
	SaveReviewWithEvent(ctx context.Context, day time.Time, kind, content string, stats map[string]any, lessons []string, model, traceID, title string) error
	// ReviewOn 查某日某 kind 复盘。found=false 表示没有该行。
	ReviewOn(ctx context.Context, day time.Time, kind string) (content string, stats map[string]any, lessons []string, model string, found bool, err error)
	// HsgtTop10 北向十大成交（P2-8 起作为龙虎榜段的北向补充读数）。
	// 没有当日行返回空切片，不返回错误。
	HsgtTop10(ctx context.Context, day time.Time) ([]NorthTop, error)
	// LhbSeatsOn 当日龙虎榜席位，已并席位画像（Tag：INSTITUTION/HOT_MONEY/QUANT/NORTH，
	// 官方三席位有系统画像，营业部画像空串=未维护）。没有当日行返回空切片。
	LhbSeatsOn(ctx context.Context, day time.Time) ([]LhbSeatRow, error)
	// UnlockWindow 查 [from, to] 窗口内的解禁批次，按 free_date 升序。没有返回空切片。
	UnlockWindow(ctx context.Context, from, to time.Time) ([]UnlockRow, error)
	// UnlockFresh 解禁域最近 days 个自然日内是否采集成功过（source_health.last_success_at）。
	// 用来区分「窗口内真无解禁」与「采集没跑」——没取到不等于 0。
	UnlockFresh(ctx context.Context, day time.Time, days int) (bool, error)
	// HoldingDecreaseRecent 查 [from, to] 内 event_type=holding_decrease 的公告情报，
	// 按重要度、发布时间倒序取前 n 条。没有返回空切片。
	HoldingDecreaseRecent(ctx context.Context, from, to time.Time, n int) ([]Intel, error)
}
