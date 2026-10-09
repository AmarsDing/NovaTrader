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
