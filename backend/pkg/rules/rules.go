// Package rules 是回测和实盘共用的策略接口。策略不能自己查库。
// 接口见 doc/开发文档/M06-选股与信号/设计文档.md 第 3 节。
package rules

import (
	"context"
	"time"
)

// Bar 是一根 K 线。Time 为 K 线所属时间（日线为交易日）。
type Bar struct {
	Time                           time.Time
	Open, High, Low, Close, Amount float64
	Volume                         int64
}

// Stage 是情绪阶段，取值与 M05 相同。
type Stage string

const (
	StageIce     Stage = "ICE"
	StageRecover Stage = "RECOVER"
	StageWarm    Stage = "WARM"
	StageHot     Stage = "HOT"
	StageFade    Stage = "FADE"
)

// Snapshot 是一只股票在 AsOf 时刻的可见数据。
type Snapshot struct {
	Symbol       string
	Name         string
	ST           bool
	Suspended    bool
	ListDate     time.Time
	AsOf         time.Time
	Bars         []Bar // 已收盘的日线，旧 → 新，不含当日
	Today        *Bar  // 当日到 AsOf 为止；盘前为 nil
	Stage        Stage
	NegativeNews bool
}

// PrevClose 是最后一根已收盘日线的收盘价。
func (s Snapshot) PrevClose() float64 {
	if n := len(s.Bars); n > 0 {
		return s.Bars[n-1].Close
	}
	return 0
}

// Last 是当前价：盘中用当日最新价，盘前用昨收。
func (s Snapshot) Last() float64 {
	if s.Today != nil && s.Today.Close > 0 {
		return s.Today.Close
	}
	return s.PrevClose()
}

// Position 是策略看到的持仓。止损止盈来自买入信号；HoldDays 为买入后经过的交易日数，买入当日为 0。
type Position struct {
	Symbol     string
	Quantity   int
	Available  int
	AvgCost    float64
	StopLoss   float64
	TakeProfit float64
	HoldDays   int
}

const (
	SideBuy  = "buy"
	SideSell = "sell"
)

// 卖出原因，写入 trade_signals.exit_kind。
const (
	ExitStopLoss   = "stop_loss"
	ExitBadNews    = "bad_news"
	ExitTakeProfit = "take_profit"
	ExitSentiment  = "sentiment"
	ExitTime       = "time"
)

// Plan 是一笔打算提交给风控的委托。价格仍要再经过 pkg/ashare。
type Plan struct {
	Side     string
	Price    float64
	Shares   int
	Reason   string
	HoldDays int
	ExitKind string
}

// Strategy 的四个方法在回测和实盘里是同一份实现。
type Strategy interface {
	Name() string
	Filter(ctx context.Context, s Snapshot) bool
	Score(ctx context.Context, s Snapshot) float64
	EntryPlan(ctx context.Context, s Snapshot) (Plan, bool)
	ExitPlan(ctx context.Context, s Snapshot, pos Position) (Plan, bool)
}

// Lookup 读取一个数值参数，没有时 ok 为假。为 nil 时全部用默认值。
type Lookup func(key string) (float64, bool)

func pick(get Lookup, key string, def float64) float64 {
	if get == nil {
		return def
	}
	if v, ok := get(key); ok {
		return v
	}
	return def
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
