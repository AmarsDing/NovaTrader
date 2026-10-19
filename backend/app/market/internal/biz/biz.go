// Package biz 是 market（M02）的业务编排：预热、每轮快照、封口落库、检查点、收盘、竞价、外围、资金。
// 计算本身在 pkg/market，这里只负责调度和读写。设计见 doc/开发文档/M02-行情与因子/设计文档.md。
package biz

import (
	"context"
	"time"

	"server/conf"
	"server/pkg/market"

	"github.com/google/wire"
)

var ProviderSet = wire.NewSet(NewConfig, NewUsecase)

// 因子截面类别，与 stock_factor.kind 一致。
const (
	KindClose    = "close"
	KindIntraday = "intraday"
	KindAuction  = "auction"
	KindCapital  = "capital"
)

// P2 落库来源标记（limit_premium.source / market_breadth.source）。
const (
	// SourceMarketAgg 是 market 服务自己算出来的（与 market_data.source 同名）。
	SourceMarketAgg = "market_agg"
	// SourceLiveBar 是读接口在快照缺失时按 market_data 实时兜底算的，不落库。
	SourceLiveBar = "live_bar"
)

// BarRow 是库里的一根 K 线和它的复权因子。
type BarRow struct {
	market.Bar
	Adj      float64
	PreClose float64
}

// FactorRecord 是 stock_factor 的一行。
type FactorRecord struct {
	market.FactorRow
	Kind string
}

// Repo 是数据层端口。所有写入都是 upsert，可重复执行。
type Repo interface {
	LoadStocks(ctx context.Context) ([]market.StockInfo, error)
	// StockBasic 读单券静态（报价侧）。库中无此证券时返回 Found=false 而不是错误。
	StockBasic(ctx context.Context, symbol string) (StockBasicRow, error)
	// DailyHistory 返回每只股票 before 之前最近 n 根日线，按时间升序。
	DailyHistory(ctx context.Context, before time.Time, n int) (map[string][]market.DayBar, error)
	// SealedOn 返回某交易日收盘涨停股的连板数。
	SealedOn(ctx context.Context, day time.Time) (map[string]int, error)
	// ClosePhase 返回某交易日最后一条情绪记录的阶段，没有时返回空。
	ClosePhase(ctx context.Context, day time.Time) (market.Phase, error)
	Config(ctx context.Context, key string) (string, error)

	SaveMinuteBars(ctx context.Context, bars []market.Bar, adj map[string]float64) error
	SaveDailyIfAbsent(ctx context.Context, bars []market.DayBar) error
	SaveFactors(ctx context.Context, day time.Time, kind string, rows []market.FactorRow) error
	SaveBoards(ctx context.Context, boards []market.LimitBoard) error
	SaveSentiment(ctx context.Context, s market.Sentiment) error
	SaveSectorHeat(ctx context.Context, day, asOf time.Time, rows []market.SectorHeat) error

	SectorMembers(ctx context.Context, day time.Time) (map[string][]string, map[string]string, error)
	MoneyFlows(ctx context.Context, day time.Time, n int) (map[string][]market.Flow, error)
	DayAmounts(ctx context.Context, day time.Time) (map[string]float64, error)
	LhbSeats(ctx context.Context, day time.Time) (map[string][]market.Seat, error)
	SeatTags(ctx context.Context) (map[string]string, error)
	// EnsureDefaults 写入席位标签和外围映射的默认行。已有行不覆盖。
	EnsureDefaults(ctx context.Context) error
	OverseasQuotes(ctx context.Context, since time.Time) ([]market.Quote, error)
	OverseasMappings(ctx context.Context) ([]market.Mapping, error)

	Bars(ctx context.Context, symbol, freq string, start, end time.Time, limit int) ([]BarRow, error)
	FactorsAt(ctx context.Context, day time.Time, kind string, asOf time.Time, symbols []string) ([]FactorRecord, error)
	SentimentAt(ctx context.Context, day, asOf time.Time) (market.Sentiment, bool, error)
	Boards(ctx context.Context, day time.Time, direction, status string) ([]market.LimitBoard, error)
	SectorHeatAt(ctx context.Context, day, asOf time.Time, top int) ([]market.SectorHeat, error)

	// P2（监控驾驶舱业务监控方案 §7）
	// DailyBarDays 返回 [from,to) 内所有 1d 收盘行的 symbol+日（存在性检查，不取价量）。
	DailyBarDays(ctx context.Context, from, to time.Time) ([]DailyBarDay, error)
	// DailyBarsOn 返回某交易日全部 1d 收盘行，按 symbol 索引。
	DailyBarsOn(ctx context.Context, day time.Time) (map[string]market.DayBar, error)
	// SaveIssues 把质量问题 upsert 进 data_quality_issue。
	SaveIssues(ctx context.Context, issues []QualityIssue) error
	// PremiumRows 读某日全部 limit_premium 行。
	PremiumRows(ctx context.Context, day time.Time) ([]PremiumRow, error)
	// PremiumRange 读 [from,to] 区间内的 limit_premium 行（按 trade_date 倒序）。
	PremiumRange(ctx context.Context, from, to time.Time) ([]PremiumRow, error)
	// SavePremiums upsert limit_premium（trade_date+symbol 唯一）。
	SavePremiums(ctx context.Context, rows []PremiumRow) error
	// BreadthRange 读 [from,to] 区间内的 market_breadth 行（按 trade_date 倒序）。
	BreadthRange(ctx context.Context, from, to time.Time) ([]BreadthRecord, error)
	// SaveBreadth upsert market_breadth（trade_date 唯一）。
	SaveBreadth(ctx context.Context, rec BreadthDay) error
}

// SnapshotSource 读 Redis snap:latest。
type SnapshotSource interface {
	All(ctx context.Context) ([]market.Snapshot, error)
}

// Publisher 发普通 NATS 事件。
type Publisher interface {
	Publish(ctx context.Context, subject string, payload any) error
}

// Config 是运行参数，缺省值见 conf.Market 注释。
type Config struct {
	RefreshInterval time.Duration
	StaleAfter      time.Duration
	BarGrace        time.Duration
	WarmupDays      int
	Checkpoints     []string
	WatchTTL        time.Duration
}

func NewConfig(c *conf.Market) Config {
	cfg := Config{
		RefreshInterval: 3 * time.Second,
		StaleAfter:      10 * time.Second,
		BarGrace:        5 * time.Second,
		WarmupDays:      250,
		Checkpoints:     []string{"10:00", "11:31", "14:30"},
		WatchTTL:        10 * time.Minute,
	}
	if c == nil {
		return cfg
	}
	if d := c.GetRefreshInterval(); d != nil && d.AsDuration() > 0 {
		cfg.RefreshInterval = d.AsDuration()
	}
	if d := c.GetStaleAfter(); d != nil && d.AsDuration() > 0 {
		cfg.StaleAfter = d.AsDuration()
	}
	if d := c.GetBarGrace(); d != nil && d.AsDuration() > 0 {
		cfg.BarGrace = d.AsDuration()
	}
	if n := c.GetWarmupDays(); n > 0 {
		cfg.WarmupDays = int(n)
	}
	if len(c.GetCheckpoints()) > 0 {
		cfg.Checkpoints = c.GetCheckpoints()
	}
	if d := c.GetWatchTtl(); d != nil && d.AsDuration() > 0 {
		cfg.WatchTTL = d.AsDuration()
	}
	return cfg
}
