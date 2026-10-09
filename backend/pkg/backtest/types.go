// Package backtest 是事件驱动回测引擎。只做计算，不连数据库；数据由 Source 提供。
// 规则见 doc/开发文档/M07-回测与自进化/设计文档.md。
package backtest

import (
	"context"
	"fmt"
	"strings"
	"time"

	"server/pkg/ashare"
	"server/pkg/brokerif/sim"
	"server/pkg/rules"
	"server/pkg/tradecal"
)

// EngineVersion 在撮合、复权、指标口径变化时递增，写进每个任务，用来判断能否比对复现。
const EngineVersion = "m07-1"

const (
	Freq1m = "1m"
	Freq1d = "1d"
)

// DayBar 是一根不复权日线。Day 为交易日 00:00（上海）。Adj 为后复权因子；PreClose 为交易所前收，缺失为 0。
type DayBar struct {
	Day      time.Time
	Open     float64
	High     float64
	Low      float64
	Close    float64
	Volume   int64
	Amount   float64
	Adj      float64
	PreClose float64
}

// MinBar 是一根不复权 1 分钟线。Time 为 K 线开始时间，走完时刻为 Time + 1 分钟。
type MinBar struct {
	Time   time.Time
	Open   float64
	High   float64
	Low    float64
	Close  float64
	Volume int64
	Amount float64
}

// NameSpan 是一段时间内的证券名称。End 为零表示至今。
type NameSpan struct {
	Name  string
	Start time.Time
	End   time.Time
}

// PhasePoint 是某一时刻的情绪阶段（M02 market_sentiment）。
type PhasePoint struct {
	AsOf  time.Time
	Phase string
}

// Source 提供回测数据。实现可以超前读取，引擎负责只把可见部分交给策略。
type Source interface {
	// TradingDays 返回 [from, to] 内的交易日。日历有缺日时必须返回错误。
	TradingDays(ctx context.Context, from, to time.Time) ([]time.Time, error)
	// DailyBars 返回 [from, to] 内全部代码的日线，按日期升序。
	DailyBars(ctx context.Context, from, to time.Time) (map[string][]DayBar, error)
	// MinuteBars 返回某个交易日这些代码的 1 分钟线，按时间升序。
	MinuteBars(ctx context.Context, day time.Time, symbols []string) (map[string][]MinBar, error)
	// Names 返回名称史。没有数据时返回空 map。
	Names(ctx context.Context) (map[string][]NameSpan, error)
	// ListDates 返回上市日期。没有数据时返回空 map。
	ListDates(ctx context.Context) (map[string]time.Time, error)
	// Phases 返回 [from, to] 内的情绪阶段，按 AsOf 升序。没有数据时返回空。
	Phases(ctx context.Context, from, to time.Time) ([]PhasePoint, error)
}

// FeeConfig 是费率。JSON 字段名固定，任务配置会归档。
type FeeConfig struct {
	CommissionRate float64 `json:"commission_rate"`
	MinCommission  float64 `json:"min_commission"`
	StampTaxRate   float64 `json:"stamp_tax_rate"`
	TransferRate   float64 `json:"transfer_rate"`
}

func (f FeeConfig) Fee() ashare.Fee {
	return ashare.Fee{CommissionRate: f.CommissionRate, MinCommission: f.MinCommission, StampTaxRate: f.StampTaxRate, TransferRate: f.TransferRate}
}

// Config 是一次回测的全部输入。同一份 Config 和同一份数据必须得到同样的成交。
type Config struct {
	Strategy       string             `json:"strategy"`
	Params         map[string]float64 `json:"params"`
	Freq           string             `json:"freq"`
	Start          string             `json:"start"`
	End            string             `json:"end"`
	InitialCash    float64            `json:"initial_cash"`
	Benchmark      string             `json:"benchmark"`
	SlippageTicks  int                `json:"slippage_ticks"`
	VolumeCap      float64            `json:"volume_cap"`
	LimitFill      string             `json:"limit_fill"`
	Fee            FeeConfig          `json:"fee"`
	PositionPct    float64            `json:"position_pct"`
	MaxPositions   int                `json:"max_positions"`
	MaxCandidates  int                `json:"max_candidates"`
	HistoryBars    int                `json:"history_bars"`
	RiskFree       float64            `json:"risk_free"`
	Seed           int64              `json:"seed"`
	LookaheadCheck bool               `json:"lookahead_check"`
}

// DefaultConfig 是设计文档第 3 节的默认撮合参数。
func DefaultConfig() Config {
	f := ashare.DefaultFee()
	r := sim.DefaultMatchRule()
	return Config{
		Freq:          Freq1m,
		InitialCash:   1_000_000,
		Benchmark:     "000300.SH",
		SlippageTicks: r.SlippageTicks,
		VolumeCap:     r.VolumeCap,
		LimitFill:     r.LimitFill,
		Fee:           FeeConfig{CommissionRate: f.CommissionRate, MinCommission: f.MinCommission, StampTaxRate: f.StampTaxRate, TransferRate: f.TransferRate},
		PositionPct:   0.2,
		MaxPositions:  5,
		MaxCandidates: 100,
		HistoryBars:   120,
		Seed:          1,
	}
}

// Normalize 用默认值补齐空字段，校验并返回起止日。参数按策略注册表补齐默认值。
func (c *Config) Normalize() (start, end time.Time, err error) {
	d := DefaultConfig()
	if c.Freq == "" {
		c.Freq = d.Freq
	}
	if c.Freq != Freq1m && c.Freq != Freq1d {
		return start, end, fmt.Errorf("backtest: freq must be 1m or 1d, got %q", c.Freq)
	}
	if c.InitialCash <= 0 {
		c.InitialCash = d.InitialCash
	}
	if c.Benchmark == "" {
		c.Benchmark = d.Benchmark
	}
	if c.VolumeCap <= 0 {
		c.VolumeCap = d.VolumeCap
	}
	if c.VolumeCap > 1 {
		return start, end, fmt.Errorf("backtest: volume_cap must be in (0, 1]")
	}
	if c.SlippageTicks < 0 {
		return start, end, fmt.Errorf("backtest: slippage_ticks must be >= 0")
	}
	if c.LimitFill == "" {
		c.LimitFill = d.LimitFill
	}
	if c.LimitFill != sim.LimitFillOpened && c.LimitFill != sim.LimitFillNever {
		return start, end, fmt.Errorf("backtest: limit_fill must be opened or never")
	}
	if c.Fee == (FeeConfig{}) {
		c.Fee = d.Fee
	}
	if c.PositionPct <= 0 || c.PositionPct > 1 {
		c.PositionPct = d.PositionPct
	}
	if c.MaxPositions <= 0 {
		c.MaxPositions = d.MaxPositions
	}
	if c.MaxCandidates <= 0 {
		c.MaxCandidates = d.MaxCandidates
	}
	if c.HistoryBars < 30 {
		c.HistoryBars = d.HistoryBars
	}
	if c.Seed == 0 {
		c.Seed = d.Seed
	}
	spec, ok := rules.Find(c.Strategy)
	if !ok {
		return start, end, fmt.Errorf("backtest: unknown strategy %q", c.Strategy)
	}
	params, err := spec.Resolve(c.Params)
	if err != nil {
		return start, end, err
	}
	c.Params = params
	if start, err = ParseDay(c.Start); err != nil {
		return start, end, fmt.Errorf("backtest: start: %w", err)
	}
	if end, err = ParseDay(c.End); err != nil {
		return start, end, fmt.Errorf("backtest: end: %w", err)
	}
	if end.Before(start) {
		return start, end, fmt.Errorf("backtest: end %s is before start %s", c.End, c.Start)
	}
	return start, end, nil
}

// ParseDay 解析 2006-01-02，返回上海时区当日 00:00。
func ParseDay(s string) (time.Time, error) {
	return time.ParseInLocation("2006-01-02", strings.TrimSpace(s), tradecal.Shanghai())
}

// Day 把任意时刻截到上海时区当日 00:00。
func Day(t time.Time) time.Time {
	d := t.In(tradecal.Shanghai())
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, tradecal.Shanghai())
}

func dayKey(t time.Time) string { return t.In(tradecal.Shanghai()).Format("2006-01-02") }

// Trade 是一笔成交或除权入账。Side 为 buy / sell / dividend。
type Trade struct {
	Seq     int       `json:"seq"`
	Symbol  string    `json:"symbol"`
	Side    string    `json:"side"`
	Time    time.Time `json:"time"`
	Price   float64   `json:"price"`
	Qty     int       `json:"qty"`
	Amount  float64   `json:"amount"`
	Fee     float64   `json:"fee"`
	Reason  string    `json:"reason"`
	PnL     float64   `json:"pnl"`
	RoundID int       `json:"round_id"`
}

// Round 是一个完整的持仓回合。
type Round struct {
	ID         int       `json:"id"`
	Symbol     string    `json:"symbol"`
	OpenTime   time.Time `json:"open_time"`
	CloseTime  time.Time `json:"close_time"`
	BuyAmount  float64   `json:"buy_amount"`
	SellAmount float64   `json:"sell_amount"`
	Fees       float64   `json:"fees"`
	Income     float64   `json:"income"`
	PnL        float64   `json:"pnl"`
	Return     float64   `json:"return"`
	HoldDays   int       `json:"hold_days"`
	ExitReason string    `json:"exit_reason"`
	openIdx    int
}

// EquityPoint 是一个交易日收盘后的账户。
type EquityPoint struct {
	Day       time.Time `json:"day"`
	Equity    float64   `json:"equity"`
	Cash      float64   `json:"cash"`
	Benchmark float64   `json:"benchmark"`
	Drawdown  float64   `json:"drawdown"`
}

// OpenPosition 是区间结束时仍未平仓的持仓，按最后收盘价估值，不计入回合统计。
type OpenPosition struct {
	Symbol   string  `json:"symbol"`
	Qty      int     `json:"qty"`
	AvgCost  float64 `json:"avg_cost"`
	Last     float64 `json:"last"`
	Value    float64 `json:"value"`
	HoldDays int     `json:"hold_days"`
}

// OrderLog 是一次下单决策。Time 是决策时刻（K 线走完的时刻），不是成交时刻。
type OrderLog struct {
	Time   time.Time `json:"time"`
	Symbol string    `json:"symbol"`
	Side   string    `json:"side"`
	Qty    int       `json:"qty"`
	Limit  float64   `json:"limit"`
	Reason string    `json:"reason"`
}

// Result 是一次运行的原始结果。
type Result struct {
	Config     Config         `json:"config"`
	Orders     []OrderLog     `json:"-"`
	Trades     []Trade        `json:"trades"`
	Rounds     []Round        `json:"rounds"`
	Rejects    int            `json:"rejects"`
	RejectWhy  map[string]int `json:"reject_why"`
	Equity     []EquityPoint  `json:"equity"`
	Open       []OpenPosition `json:"open"`
	Warnings   []string       `json:"warnings"`
	DataHash   string         `json:"data_hash"`
	FillsHash  string         `json:"fills_hash"`
	DataCutoff time.Time      `json:"data_cutoff"`
}

// Progress 在每个交易日结束后回调。
type Progress func(done, total int)
