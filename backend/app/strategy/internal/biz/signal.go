package biz

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"server/pkg/events"
	"server/pkg/rules"
)

// Status 是信号状态，见 M06 设计文档第 7 节。
type Status string

const (
	StatusPending      Status = "pending"
	StatusRiskChecking Status = "risk_checking"
	StatusApproved     Status = "approved"
	StatusRejected     Status = "rejected"
	StatusExecuting    Status = "executing"
	StatusDone         Status = "done"
	StatusClosed       Status = "closed"
	StatusExpired      Status = "expired"
	StatusCancelled    Status = "cancelled"
)

var transitions = map[Status][]Status{
	StatusPending:      {StatusRiskChecking, StatusExpired, StatusCancelled},
	StatusRiskChecking: {StatusApproved, StatusRejected},
	StatusApproved:     {StatusExecuting, StatusExpired, StatusCancelled},
	StatusExecuting:    {StatusDone, StatusCancelled},
	StatusDone:         {StatusClosed},
}

// OpenStatuses 是非终态。
var OpenStatuses = []Status{StatusPending, StatusRiskChecking, StatusApproved, StatusExecuting}

// ExpirableStatuses 是到期后会被改成 expired 的状态。
var ExpirableStatuses = []Status{StatusPending, StatusApproved}

// CanTransition 判断 from → to 是否合法。
func CanTransition(from, to Status) bool {
	for _, s := range transitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

// Terminal 判断是否终态。
func (s Status) Terminal() bool {
	_, ok := transitions[s]
	return !ok
}

const (
	ActorStrategy = "strategy"
	ActorRisk     = "risk"
	ActorTrade    = "trade"
	ActorUser     = "user"
	eventSource   = "strategy"
)

// ActorAllowed 判断 actor 能否做 from → to，见设计文档第 7 节。批准只能由风控做。
func ActorAllowed(from, to Status, actor string) bool {
	switch to {
	case StatusRiskChecking, StatusApproved, StatusRejected:
		return actor == ActorRisk
	case StatusExecuting, StatusDone, StatusClosed:
		return actor == ActorTrade
	case StatusExpired:
		return actor == ActorStrategy
	case StatusCancelled:
		if from == StatusExecuting {
			return actor == ActorTrade
		}
		return actor == ActorUser || actor == ActorRisk || actor == ActorTrade || actor == ActorStrategy
	}
	return false
}

// Signal 是一条交易信号。卖出信号的 Entry 是卖出限价，PositionPct 是卖出比例（1 为全部可卖）。
type Signal struct {
	ID          int
	Book        string
	Time        time.Time
	TradeDate   time.Time
	Symbol      string
	Name        string
	Side        string
	Strategy    string
	VersionID   *int
	Entry       float64
	EntryLow    float64
	EntryHigh   float64
	StopLoss    float64
	TakeProfit  float64
	ATR         float64
	PositionPct float64
	ValidUntil  time.Time
	HoldDaysMax int
	RuleScore   float64
	AIScore     *float64
	FinalScore  float64
	Dims        map[string]float64
	Evidence    []string
	AIDegraded  bool
	HighValue   bool
	ExitKind    string
	Reason      string
	ExitPrice   *float64
	PnlPct      *float64 // 百分比，3.2 表示 +3.2%
	ClosedAt    time.Time
	HoldingDays *int
	Status      Status
	TraceID     string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// SignalUpdated 是 strategy.signal.updated 的载荷。前半部分字段名与 M04 设计第 6 节的约定一致，M04 靠它建案例。
type SignalUpdated struct {
	SignalID    int        `json:"signal_id"`
	Book        string     `json:"book"`
	Status      Status     `json:"status"`
	StockCode   string     `json:"stock_code"`
	StockName   string     `json:"stock_name"`
	Strategy    string     `json:"strategy"`
	SignalType  string     `json:"signal_type"`
	SignalTime  time.Time  `json:"signal_time"`
	EntryPrice  float64    `json:"entry_price"`
	Reasoning   string     `json:"reasoning"`
	From        Status     `json:"from"`
	StopLoss    float64    `json:"stop_loss"`
	TakeProfit  float64    `json:"take_profit"`
	PositionPct float64    `json:"position_pct"`
	FinalScore  float64    `json:"final_score"`
	AIDegraded  bool       `json:"ai_degraded"`
	HighValue   bool       `json:"high_value"`
	ExitKind    string     `json:"exit_kind,omitempty"`
	ValidUntil  *time.Time `json:"valid_until,omitempty"`
	ExitPrice   *float64   `json:"exit_price,omitempty"`
	PnlPct      *float64   `json:"pnl_pct,omitempty"`
	HoldingDays *int       `json:"holding_days,omitempty"`
	ClosedAt    *time.Time `json:"closed_at,omitempty"`
}

// Close 是买入信号平仓结果。PnlPct 用百分比，和 M04 案例一致。
type Close struct {
	ExitPrice float64
	PnlPct    float64
	At        time.Time
}

// SignalPatch 是迁移时一并写入的平仓字段。
type SignalPatch struct {
	ExitPrice   *float64
	PnlPct      *float64
	ClosedAt    *time.Time
	HoldingDays *int
}

func signalEnvelope(s *Signal, from Status) (events.Envelope, error) {
	p := SignalUpdated{
		SignalID: s.ID, Book: s.Book, Status: s.Status, StockCode: s.Symbol, StockName: s.Name,
		Strategy: s.Strategy, SignalType: s.Side, SignalTime: s.Time, EntryPrice: s.Entry, Reasoning: s.Reason,
		From: from, StopLoss: s.StopLoss, TakeProfit: s.TakeProfit, PositionPct: s.PositionPct,
		FinalScore: s.FinalScore, AIDegraded: s.AIDegraded, HighValue: s.HighValue, ExitKind: s.ExitKind,
		ExitPrice: s.ExitPrice, PnlPct: s.PnlPct, HoldingDays: s.HoldingDays,
	}
	if !s.ClosedAt.IsZero() {
		v := s.ClosedAt
		p.ClosedAt = &v
	}
	if !s.ValidUntil.IsZero() {
		v := s.ValidUntil
		p.ValidUntil = &v
	}
	return events.New(eventSource, events.SubjectSignal, s.TraceID, p)
}

// Transition 改信号状态。M08、M09 和客户端都走这里，不直接写表。
// close 只在迁到 closed 时必填：出场价和收益率。仓位还没归零时不要调，否则这只票不再按原止损止盈检查。
func (uc *Usecase) Transition(ctx context.Context, id int, to Status, reason, actor string, close *Close) (*Signal, error) {
	cur, err := uc.signals.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !CanTransition(cur.Status, to) {
		return nil, fmt.Errorf("%w: %s → %s", ErrBadTransition, cur.Status, to)
	}
	if !ActorAllowed(cur.Status, to, actor) {
		return nil, fmt.Errorf("%w: actor %q cannot do %s → %s", ErrBadTransition, actor, cur.Status, to)
	}
	patch, err := uc.closePatch(cur, to, close)
	if err != nil {
		return nil, err
	}
	trace := events.TraceID(ctx)
	if trace == "" {
		trace = cur.TraceID
	}
	return uc.signals.Transition(ctx, id, cur.Status, to, reason, actor, trace, signalEnvelope, patch)
}

func (uc *Usecase) closePatch(cur *Signal, to Status, close *Close) (*SignalPatch, error) {
	if to != StatusClosed {
		if close != nil {
			return nil, fmt.Errorf("%w: only closed carries an exit", ErrBadRequest)
		}
		return nil, nil
	}
	if cur.Side != rules.SideBuy {
		return nil, fmt.Errorf("%w: only a buy signal can be closed", ErrBadRequest)
	}
	if close == nil || close.ExitPrice <= 0 || math.IsNaN(close.ExitPrice) || math.IsNaN(close.PnlPct) || math.IsInf(close.PnlPct, 0) {
		return nil, fmt.Errorf("%w: closed requires exit price and pnl percent", ErrBadRequest)
	}
	at := close.At
	if at.IsZero() {
		at = time.Now()
	}
	days := uc.holdDays(cur.TradeDate, tradeDay(at))
	return &SignalPatch{ExitPrice: &close.ExitPrice, PnlPct: &close.PnlPct, ClosedAt: &at, HoldingDays: &days}, nil
}

// ExpireDue 把过了有效期的 pending / approved 信号改成 expired。被别人抢先改掉的跳过。
func (uc *Usecase) ExpireDue(ctx context.Context, now time.Time) (int, error) {
	due, err := uc.signals.Due(ctx, now)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, s := range due {
		_, err := uc.signals.Transition(ctx, s.ID, s.Status, StatusExpired, "超过有效期", ActorStrategy, s.TraceID, signalEnvelope, nil)
		if errors.Is(err, ErrConflict) {
			continue
		}
		if err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
