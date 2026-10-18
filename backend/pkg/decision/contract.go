// Package decision freezes server-owned facts and deterministic rule plans.
// It contains no broker, database or model-generated price/size capability.
package decision

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"server/pkg/accountstate"
	"server/pkg/market"
	"server/pkg/symbol"
)

const Version = 1

var ErrInvalid = errors.New("decision invalid")
var ErrExpired = errors.New("plan expired")
var ErrVersionChanged = errors.New("plan version changed")
var ErrLedgerChanged = errors.New("ledger changed")
var ErrNotTriggered = errors.New("condition not triggered")
var ErrUnavailable = errors.New("current facts unavailable")

type Snapshot struct {
	Version           int             `json:"version"`
	SnapshotID        string          `json:"snapshot_id"`
	SignalID          int             `json:"signal_id"`
	SignalHash        string          `json:"signal_hash"`
	Symbol            string          `json:"symbol"`
	AccountType       string          `json:"account_type"`
	AccountID         string          `json:"account_id"`
	AccountSnapshotID string          `json:"account_snapshot_id"`
	AccountGeneration int64           `json:"account_generation"`
	AccountStateHash  string          `json:"account_state_hash"`
	Strategy          string          `json:"strategy"`
	StrategyRevision  int             `json:"strategy_revision"`
	ConfigRevision    int             `json:"config_revision"`
	FormedAt          time.Time       `json:"formed_at"`
	SourceTime        time.Time       `json:"source_time"`
	Quote             market.Snapshot `json:"quote"`
	Evidence          []string        `json:"evidence"`
	CounterEvidence   []string        `json:"counter_evidence"`
	Unknowns          []string        `json:"unknowns"`
	RuleScore         float64         `json:"rule_score"`
	ModelScore        *float64        `json:"model_score,omitempty"`
	Reason            string          `json:"reason"`
}

type Plan struct {
	Version      int       `json:"version"`
	PlanID       string    `json:"plan_id"`
	Revision     int       `json:"revision"`
	SnapshotID   string    `json:"snapshot_id"`
	SignalID     int       `json:"signal_id"`
	AccountType  string    `json:"account_type"`
	AccountID    string    `json:"account_id"`
	Symbol       string    `json:"symbol"`
	Side         string    `json:"side"`
	PriceOrigin  string    `json:"price_origin"`
	EntryLow     float64   `json:"entry_low"`
	EntryHigh    float64   `json:"entry_high"`
	LimitPrice   float64   `json:"limit_price"`
	StopLoss     float64   `json:"stop_loss"`
	TakeProfit   float64   `json:"take_profit"`
	PositionPct  float64   `json:"position_pct"`
	MaxNotional  float64   `json:"max_notional"`
	ValidFrom    time.Time `json:"valid_from"`
	ValidUntil   time.Time `json:"valid_until"`
	ExecutionDay string    `json:"execution_day"`
	Summary      string    `json:"summary"`
	Invalidation []string  `json:"invalidation"`
}

func Hash(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// AccountHash identifies economic ledger state, excluding refresh timestamps,
// mark prices and derived equity. A new heartbeat alone is not a new ledger.
func AccountHash(s accountstate.Snapshot) string {
	type holding struct {
		Symbol                      string
		Quantity, Available, Frozen int
		Cost                        float64
	}
	hs := make([]holding, 0, len(s.Holdings))
	for _, h := range s.Holdings {
		hs = append(hs, holding{h.Symbol, h.Quantity, h.Available, h.FrozenSell, h.AvgCost})
	}
	sort.Slice(hs, func(i, j int) bool { return hs[i].Symbol < hs[j].Symbol })
	orders := append([]string(nil), s.ObservedOrders...)
	sort.Strings(orders)
	return Hash(struct {
		Account, ID, Day                  string
		Cash, Available, Frozen, DayStart float64
		Known                             bool
		Holdings                          []holding
		Orders                            []string
	}{string(s.AccountType), s.AccountID, s.TradingDay, s.Cash, s.Available, s.FrozenCash, s.DayStartEquity, s.DayStartKnown, hs, orders})
}

func (s Snapshot) Validate() error {
	if s.Version != Version || s.SnapshotID == "" || s.SignalID <= 0 || s.SignalHash == "" || s.AccountSnapshotID == "" || s.AccountGeneration <= 0 || s.AccountStateHash == "" || s.ConfigRevision < 0 || s.FormedAt.IsZero() || s.SourceTime.IsZero() {
		return fmt.Errorf("%w: incomplete facts", ErrInvalid)
	}
	if s.AccountID != "default" || (s.AccountType != "SIM" && s.AccountType != "LIVE") {
		return fmt.Errorf("%w: unsupported account", ErrInvalid)
	}
	if _, err := symbol.Parse(s.Symbol); err != nil {
		return fmt.Errorf("%w: symbol", ErrInvalid)
	}
	if s.Quote.Symbol != s.Symbol || s.Quote.Expired(s.FormedAt, 10*time.Second) || s.Quote.Last <= 0 {
		return fmt.Errorf("%w: stale quote", ErrInvalid)
	}
	if !finite(s.RuleScore) || s.RuleScore < 0 || s.RuleScore > 100 || s.ModelScore != nil && (!finite(*s.ModelScore) || *s.ModelScore < 0 || *s.ModelScore > 100) {
		return fmt.Errorf("%w: score", ErrInvalid)
	}
	return nil
}
func finite(n float64) bool { return !math.IsNaN(n) && !math.IsInf(n, 0) }
func (p Plan) Validate(s Snapshot) error {
	if err := s.Validate(); err != nil {
		return err
	}
	if p.Version != Version || p.PlanID == "" || p.Revision < 1 || p.SnapshotID != s.SnapshotID || p.SignalID != s.SignalID || p.AccountType != s.AccountType || p.AccountID != s.AccountID || p.Symbol != s.Symbol || p.PriceOrigin != "rules" {
		return fmt.Errorf("%w: identity", ErrInvalid)
	}
	for _, n := range []float64{p.EntryLow, p.EntryHigh, p.LimitPrice, p.StopLoss, p.TakeProfit, p.PositionPct, p.MaxNotional} {
		if !finite(n) || n < 0 {
			return fmt.Errorf("%w: numeric value", ErrInvalid)
		}
	}
	if p.LimitPrice <= 0 || math.Abs(p.LimitPrice*100-math.Round(p.LimitPrice*100)) > 1e-6 || p.EntryLow > p.EntryHigh || p.EntryLow <= 0 {
		return fmt.Errorf("%w: price", ErrInvalid)
	}
	if p.Side != "buy" && p.Side != "sell" {
		return fmt.Errorf("%w: side", ErrInvalid)
	}
	if p.Side == "buy" && (!(p.StopLoss < p.EntryLow && p.EntryLow <= p.LimitPrice && p.LimitPrice <= p.EntryHigh && p.EntryHigh < p.TakeProfit) || p.PositionPct <= 0 || p.PositionPct > 1 || p.MaxNotional <= 0) {
		return fmt.Errorf("%w: rule interval", ErrInvalid)
	}
	if p.ValidFrom.Before(s.FormedAt) || !p.ValidUntil.After(p.ValidFrom) || p.ValidUntil.Sub(p.ValidFrom) > 30*time.Minute || p.ExecutionDay != p.ValidFrom.In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("2006-01-02") {
		return fmt.Errorf("%w: validity", ErrInvalid)
	}
	return nil
}

// Eligible checks current server facts just before an intent is created. Risk
// still applies its own current authorization, liquidity and position limits.
func Eligible(p Plan, s Snapshot, current accountstate.Snapshot, quote market.Snapshot, signalHash string, configRevision int, now time.Time) error {
	if err := p.Validate(s); err != nil {
		return err
	}
	if !now.Before(p.ValidUntil) {
		return fmt.Errorf("%w: %w: 计划已过期", ErrInvalid, ErrExpired)
	}
	if now.Before(p.ValidFrom) {
		return fmt.Errorf("%w: %w: 计划尚未生效", ErrInvalid, ErrUnavailable)
	}
	if err := current.Validate(); err != nil {
		return fmt.Errorf("%w: %w: 账户不可核验", ErrInvalid, ErrUnavailable)
	}
	if string(current.AccountType) != p.AccountType || current.AccountID != p.AccountID || current.Generation < s.AccountGeneration {
		return fmt.Errorf("%w: %w: 账户身份或代际改变", ErrInvalid, ErrVersionChanged)
	}
	if current.Reconciliation.Status != "matched" || !current.DayStartKnown || now.Sub(current.AsOf) > 60*time.Second || current.AsOf.After(now.Add(time.Second)) {
		return fmt.Errorf("%w: %w: 账户未就绪", ErrInvalid, ErrUnavailable)
	}
	if AccountHash(current) != s.AccountStateHash {
		return fmt.Errorf("%w: %w: 账本已变化，请重新形成计划", ErrInvalid, ErrLedgerChanged)
	}
	if signalHash != s.SignalHash || configRevision != s.ConfigRevision {
		return fmt.Errorf("%w: %w: 信号或配置版本已变化", ErrInvalid, ErrVersionChanged)
	}
	if quote.Symbol != p.Symbol || quote.Expired(now, 10*time.Second) || quote.Time.After(now.Add(time.Second)) {
		return fmt.Errorf("%w: %w: 当前行情未知或陈旧", ErrInvalid, ErrUnavailable)
	}
	if quote.Last < p.EntryLow || quote.Last > p.EntryHigh {
		return fmt.Errorf("%w: %w: 尚未进入规则触发区间", ErrInvalid, ErrNotTriggered)
	}
	return nil
}
