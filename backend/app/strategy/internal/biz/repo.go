package biz

import (
	"context"
	"errors"
	"time"

	"server/pkg/events"
	"server/pkg/rules"
)

// ErrConflict 表示状态在读取后被别人改过，迁移没有执行。
var ErrConflict = errors.New("strategy: signal status changed concurrently")

// ErrNotFound 表示信号不存在。
var ErrNotFound = errors.New("strategy: signal not found")

// ErrBadRequest 表示入参不合法。
var ErrBadRequest = errors.New("strategy: bad request")

// MarketSource 给出某一时刻可见的快照。实现方不能把 asOf 之后的数据放进来。
type MarketSource interface {
	// Universe 返回全市场快照。
	Universe(ctx context.Context, asOf time.Time) ([]rules.Snapshot, error)
	// Snapshots 只返回指定代码的快照，查不到的代码直接略过。
	Snapshots(ctx context.Context, symbols []string, asOf time.Time) ([]rules.Snapshot, error)
	// DailyCloses 返回 [from, to] 内每个交易日的收盘价，键为 2006-01-02。
	DailyCloses(ctx context.Context, symbol string, from, to time.Time) (map[string]float64, error)
}

// Fact 是交给 M05 的一条因子。
type Fact struct {
	ID    string
	Label string
	Value float64
	Unit  string
	Text  string
	Dim   string
}

type AnalyzeInput struct {
	Symbol    string
	Name      string
	TraceID   string
	RuleScore float64
	Stage     rules.Stage
	AsOf      time.Time
	Facts     []Fact
}

// AnalyzeOutput 是 M05 的研判结果。Degraded 为真时 Score 不可用；Discarded 为真时这只候选作废。
type AnalyzeOutput struct {
	DecisionID int64
	Score      float64
	Dims       map[string]float64
	Summary    string
	Evidence   []string
	Degraded   bool
	Discarded  bool
	Reason     string
}

// Analyzer 是 M05 的研判端口。返回错误等同于模型不可用。
type Analyzer interface {
	Analyze(ctx context.Context, in AnalyzeInput) (AnalyzeOutput, error)
}

// ParamRepo 读 strategy_config 和当前生效版本。
type ParamRepo interface {
	Values(ctx context.Context) (map[string]string, error)
	// ActiveVersion 没有生效版本时返回 nil, nil。
	ActiveVersion(ctx context.Context) (*int, error)
}

// EnvelopeFunc 在信号写库后生成事件，这样载荷里能带上信号编号。
type EnvelopeFunc func(s *Signal, from Status) (events.Envelope, error)

type SignalRepo interface {
	// Create 在一个事务里写信号、第一条 signal_events 和 outbox。返回信号编号。
	Create(ctx context.Context, s *Signal, env EnvelopeFunc) (int, error)
	// Transition 只在当前状态仍为 from 时改成 to，同一事务写 signal_events 和 outbox。否则返回 ErrConflict。
	Transition(ctx context.Context, id int, from, to Status, reason, actor, traceID string, env EnvelopeFunc, patch *SignalPatch) (*Signal, error)
	Get(ctx context.Context, id int) (*Signal, error)
	// Latest 返回同股同方向最近一条信号，没有时返回 nil, nil。
	Latest(ctx context.Context, book, symbol, side string) (*Signal, error)
	// HasOpen 判断同股同方向是否有非终态信号。
	HasOpen(ctx context.Context, book, symbol, side string) (bool, error)
	// CountBuys 是某交易日已经出过的买入信号数，任何状态都算。
	CountBuys(ctx context.Context, book string, day time.Time) (int, error)
	// Due 返回 valid_until 已过、仍可过期的信号。
	Due(ctx context.Context, now time.Time) ([]*Signal, error)
	// LastDoneBuy 是该股最近一条已成交的买入信号，没有时返回 nil, nil。
	LastDoneBuy(ctx context.Context, book, symbol string) (*Signal, error)
	// List 按编号倒序返回。
	List(ctx context.Context, f SignalFilter) ([]*Signal, error)
}

// SignalFilter 的零值字段不参与过滤。
type SignalFilter struct {
	Book   string
	Day    time.Time
	Status Status
	Side   string
	Limit  int
}

// CandidateFilter 的零值字段不参与过滤。
type CandidateFilter struct {
	Day   time.Time
	Pool  string
	Stage string
	Limit int
}

// Candidate 是 strategy_candidates 的一行。
type Candidate struct {
	ID         int
	TradeDate  time.Time
	Strategy   string
	VersionID  *int
	Symbol     string
	Name       string
	Pool       string
	Stage      string
	MissReason string
	RuleScore  float64
	AIScore    *float64
	FinalScore *float64
	SignalID   *int
	RefPrice   float64
	RetT1      *float64
	RetT3      *float64
	RetT5      *float64
}

type CandidateRepo interface {
	// Save 按（交易日、模板、代码）写入或更新。已是 selected 的行不会被降级，ref_price 只在首次写入或入选时改。
	Save(ctx context.Context, list []Candidate) error
	// PoolSymbols 返回某交易日某个池里的代码。
	PoolSymbols(ctx context.Context, day time.Time, pool string) ([]string, error)
	// PendingOutcomes 返回 since 之后、today 之前、ret_t5 仍为空的候选。
	PendingOutcomes(ctx context.Context, since, today time.Time) ([]Candidate, error)
	SetOutcome(ctx context.Context, id int, t1, t3, t5 *float64) error
	// List 按最终分、规则分倒序返回。
	List(ctx context.Context, f CandidateFilter) ([]Candidate, error)
}

type BlacklistRepo interface {
	// Active 返回 now 时仍有效的黑名单代码。
	Active(ctx context.Context, now time.Time) (map[string]bool, error)
	Add(ctx context.Context, symbol, reason, by string, expires *time.Time) error
	Remove(ctx context.Context, symbol string) error
}

// Holding 是一条持仓。
type Holding struct {
	Symbol    string
	Quantity  int
	Available int
	AvgCost   float64
}

type PositionRepo interface {
	Holdings(ctx context.Context, book string) ([]Holding, error)
}
