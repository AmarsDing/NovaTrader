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
