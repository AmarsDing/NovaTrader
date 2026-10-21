package biz

// =============================================================================
// M13-T02 契约固化（自动消费 | 终审闸门）
// 自动消费者把 strategy 的 pending 信号转成 SIM 条件计划（sim_auto 授权）。
// 契约：信号状态机 pending→approved 的最终闸门由 trade place() 内联的同步
// /risk/v1/check（Engine.place → risk.Check）承担；strategy 侧
// risk_checking/approved 分支只服务人工半自动流，自动消费链不依赖、不回写。
// 幂等：以 signal_id 生成确定性 business_key 先占 execution_intent，重复事件
// 命中唯一约束即跳过并计数；任一后续步骤失败保留意图给人核对（RecoverIntents
// 如实标 unknown），同一信号绝不自动重发第二次开仓尝试。
// =============================================================================

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"server/pkg/accountstate"
	"server/pkg/ashare"
	"server/pkg/decision"
	"server/pkg/market"
	"server/pkg/observe"
	"server/pkg/rules"
	"server/pkg/tradecal"
)

type PlanRecord struct {
	Snapshot decision.Snapshot `json:"snapshot"`
	Plan     decision.Plan     `json:"plan"`
	State    string            `json:"state"`
	Mode     string            `json:"mode"`
	Operator string            `json:"operator"`
	Reason   string            `json:"reason"`
	Intent   *Intent           `json:"intent,omitempty"`
}
type PlanFacts struct {
	SignalID                                                                 int
	Book, Symbol, Side, Strategy, Status, Reason, SignalHash                 string
	StrategyRevision, ConfigRevision                                         int
	Entry, EntryLow, EntryHigh, StopLoss, TakeProfit, PositionPct, RuleScore float64
	ModelScore                                                               *float64
	FormedAt, ValidUntil                                                     time.Time
	Evidence                                                                 []string
	Account                                                                  accountstate.Snapshot
	Quote                                                                    market.Snapshot
}
type AutomationStore interface {
	PlanFacts(context.Context, int, string) (PlanFacts, error)
	SavePlan(context.Context, PlanRecord, int) error
	GetPlan(context.Context, string, int) (PlanRecord, error)
	ListPlans(context.Context, string, string) ([]PlanRecord, error)
	SetPlanState(context.Context, string, int, string, string, string, string, string) error
	ListIntents(context.Context, string) ([]Intent, error)
	ListUnresolvedIntents(context.Context, string) ([]Intent, error)
}

func (e *Engine) automationStore() (AutomationStore, error) {
	s, ok := e.store.(AutomationStore)
	if !ok {
		return nil, fmt.Errorf("%w: 条件计划仓储未接入", ErrInvalid)
	}
	return s, nil
}
func (e *Engine) CreatePlan(ctx context.Context, signalID int, account, planID string, base int) (out PlanRecord, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	book, err := bookOf(account)
	if err != nil {
		return out, err
	}
	if book != BookPaper {
		return out, fmt.Errorf("%w: 当前条件计划仅开放 SIM", ErrInvalid)
	}
	store, err := e.automationStore()
	if err != nil {
		return out, err
	}
	err = e.withLedger(ctx, "global", func(c context.Context) error {
		if _, err := e.snapshot(c, book); err != nil {
			return err
		}
		f, err := store.PlanFacts(c, signalID, book)
		if err != nil {
			return err
		}
		now := e.clock()
		if f.Status != "pending" && f.Status != "approved" {
			return fmt.Errorf("%w: 信号状态不可形成计划", ErrInvalid)
		}
		if f.Quote.Expired(now, 10*time.Second) || f.Account.Reconciliation.Status != "matched" || !f.Account.DayStartKnown {
			return fmt.Errorf("%w: 行情或账户未就绪", ErrInvalid)
		}
		if !f.ValidUntil.After(now) {
			return fmt.Errorf("%w: 信号已过期", ErrInvalid)
		}
		revision := 1
		if planID == "" {
			planID = uuid.NewString()
		} else {
			old, err := store.GetPlan(c, planID, base)
			if err != nil {
				return err
			}
			if old.Plan.SignalID != signalID || old.Plan.AccountType != accountOf(book) || old.State == "executed" {
				return ErrConflict
			}
			revision = base + 1
		}
		snapshot := decision.Snapshot{Version: 1, SnapshotID: uuid.NewString(), SignalID: signalID, SignalHash: f.SignalHash, Symbol: f.Symbol, AccountType: accountOf(book), AccountID: "default", AccountSnapshotID: f.Account.SnapshotID, AccountGeneration: f.Account.Generation, AccountStateHash: decision.AccountHash(f.Account), Strategy: f.Strategy, StrategyRevision: f.StrategyRevision, ConfigRevision: f.ConfigRevision, FormedAt: now, SourceTime: f.Quote.Time, Quote: f.Quote, Evidence: append([]string{}, f.Evidence...), CounterEvidence: []string{"价格触及止损则规则假设失效", "行情、账户或配置改变后须重新核验"}, Unknowns: []string{"尚无样本外校准的上涨概率", "未来成交与流动性不保证"}, RuleScore: f.RuleScore, ModelScore: f.ModelScore, Reason: f.Reason}
		until := f.ValidUntil
		if until.After(now.Add(30 * time.Minute)) {
			until = now.Add(30 * time.Minute)
		}
		plan := decision.Plan{Version: 1, PlanID: planID, Revision: revision, SnapshotID: snapshot.SnapshotID, SignalID: signalID, AccountType: accountOf(book), AccountID: "default", Symbol: f.Symbol, Side: f.Side, PriceOrigin: "rules", EntryLow: f.EntryLow, EntryHigh: f.EntryHigh, LimitPrice: f.Entry, StopLoss: f.StopLoss, TakeProfit: f.TakeProfit, PositionPct: f.PositionPct, MaxNotional: f.Account.Equity * f.PositionPct, ValidFrom: now, ValidUntil: until, ExecutionDay: now.In(tradecal.Shanghai()).Format("2006-01-02"), Summary: fmt.Sprintf("等待价格进入 %.2f–%.2f；触发后仍需最新风控", f.EntryLow, f.EntryHigh), Invalidation: []string{"超过有效期", "信号、策略或配置版本改变", "账户账本改变或对账异常", "行情陈旧、停牌或交易时段不允许"}}
		if f.Side == "sell" {
			plan.EntryLow = f.Entry
			plan.EntryHigh = f.Entry * 1.2
			plan.Summary = "达到卖出条件后，仅使用实际可卖数量；T+1部分继续等待"
		}
		if err := plan.Validate(snapshot); err != nil {
			return err
		}
		out = PlanRecord{Snapshot: snapshot, Plan: plan, State: "draft", Mode: "review"}
		return store.SavePlan(c, out, base)
	})
	return
}
func (e *Engine) Plans(ctx context.Context, account string) ([]PlanRecord, error) {
	book, err := bookOf(account)
	if err != nil {
		return nil, err
	}
	store, err := e.automationStore()
	if err != nil {
		return nil, err
	}
	return store.ListPlans(ctx, book, "")
}
func (e *Engine) AuthorizePlan(ctx context.Context, id string, revision int, mode, operator string) (out PlanRecord, err error) {
	if operator == "" || (mode != "sim_confirm" && mode != "sim_auto" && mode != "revoke") {
		return out, ErrInvalid
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	store, err := e.automationStore()
	if err != nil {
		return out, err
	}
	err = e.withLedger(ctx, "global", func(c context.Context) error {
		var err error
		out, err = store.GetPlan(c, id, revision)
		if err != nil {
			return err
		}
		if out.Plan.AccountType != "SIM" || out.State == "executed" || out.State == "superseded" {
			return ErrConflict
		}
		state := "revoked"
		reason := "操作人撤销授权"
		if mode != "revoke" {
			if out.State != "authorized" {
				active, err := store.ListPlans(c, BookPaper, "authorized")
				if err != nil {
					return err
				}
				if len(active) >= 64 {
					return fmt.Errorf("%w: 已达64个活动计划上限，请先撤销或处理已有计划", ErrInvalid)
				}
			}
			f, err := store.PlanFacts(c, out.Plan.SignalID, BookPaper)
			if err != nil {
				return err
			}
			if err := e.validatePlan(c, out, f, false); err != nil {
				return err
			}
			state = "authorized"
			reason = "SIM 条件授权，提交前重新核验"
		}
		if err = store.SetPlanState(c, id, revision, out.State, state, mode, operator, reason); err != nil {
			return err
		}
		out.State = state
		out.Mode = mode
		out.Operator = operator
		out.Reason = reason
		return nil
	})
	return
}
func (e *Engine) validatePlan(ctx context.Context, r PlanRecord, f PlanFacts, trigger bool) error {
	if f.Status != "pending" && f.Status != "approved" {
		return fmt.Errorf("%w: %w: 信号已失效", ErrInvalid, decision.ErrVersionChanged)
	}
	_, _, actual, err := e.accountSnapshot(ctx, BookPaper)
	if err != nil {
		return err
	}
	actual.Generation = f.Account.Generation
	actual.SnapshotID = f.Account.SnapshotID
	quote := f.Quote
	if !trigger {
		quote.Last = r.Plan.LimitPrice
	}
	return decision.Eligible(r.Plan, r.Snapshot, actual, quote, f.SignalHash, f.ConfigRevision, e.clock())
}
func (e *Engine) ExecutePlan(ctx context.Context, id string, revision int, operator string) (out Order, err error) {
	finish := observe.ForService("trade").Op("plan_execute").Begin()
	defer func() { finish(executionOutcome(out, err)) }()
	claimedID := ""
	e.mu.Lock()
	defer e.mu.Unlock()
	store, err := e.automationStore()
	if err != nil {
		return out, err
	}
	err = e.withLedger(ctx, "global", func(c context.Context) error {
		r, err := store.GetPlan(c, id, revision)
		if err != nil {
			return err
		}
		if r.State != "authorized" || r.Plan.AccountType != "SIM" {
			return fmt.Errorf("%w: 计划未授权或已失效", ErrInvalid)
		}
		if r.Mode == "sim_confirm" && operator == "" {
			return fmt.Errorf("%w: 需要当前人工确认", ErrInvalid)
		}
		f, err := store.PlanFacts(c, r.Plan.SignalID, BookPaper)
		if err != nil {
			return err
		}
		if err = e.validatePlan(c, r, f, true); err != nil {
			return err
		}
		volume := 0
		if r.Plan.Side == "buy" {
			volume, err = ashare.RoundBuy(r.Plan.Symbol, int(r.Plan.MaxNotional/r.Plan.LimitPrice))
		} else {
			for _, h := range f.Account.Holdings {
				if h.Symbol == r.Plan.Symbol {
					volume = h.Available - h.FrozenSell
				}
			}
		}
		if r.Plan.Side == "buy" {
			volume = afford(e.set.Fee, r.Plan.MaxNotional, r.Plan.Symbol, volume, r.Plan.LimitPrice)
		}
		if err != nil || volume <= 0 {
			return fmt.Errorf("%w: 预算不足一手或T+1无可卖数量", ErrInvalid)
		}
		clientID := "plan-" + decision.Hash(struct {
			Book   string
			Signal int
		}{BookPaper, r.Plan.SignalID})[:40]
		claimedID = clientID
		out, err = e.placeDurable(c, PlaceRequest{ExpectedConfigRevision: &r.Snapshot.ConfigRevision, ClientOrderID: clientID, BusinessKey: "signal:SIM:default:" + strconv.Itoa(r.Plan.SignalID), PlanID: id, PlanRevision: revision, Account: "SIM", Symbol: r.Plan.Symbol, Side: r.Plan.Side, Price: r.Plan.LimitPrice, Volume: volume, Source: "auto", Operator: r.Operator, SignalID: strconv.Itoa(r.Plan.SignalID), StrategyVersion: strconv.Itoa(r.Snapshot.StrategyRevision), FinalCheck: func(cc context.Context) error {
			latest, err := store.PlanFacts(cc, r.Plan.SignalID, BookPaper)
			if err != nil {
				return err
			}
			return e.validatePlan(cc, r, latest, true)
		}})
		if err != nil {
			return err
		}
		return store.SetPlanState(c, id, revision, "authorized", "executed", r.Mode, r.Operator, "原订单 "+out.ClientOrderID+"："+out.Status)
	})
	if err != nil && claimedID != "" && (errors.Is(err, ErrExecutionInterrupted) || out.ClientOrderID != "") {
		_ = e.store.(LedgerStore).FinishIntentDetached(ctx, claimedID, "unknown", "执行事务未确认；保留原订单身份待核对")
	}
	return
}
func (e *Engine) TickPlans(ctx context.Context) error {
	store, err := e.automationStore()
	if err != nil {
		return err
	}
	rows, err := store.ListPlans(ctx, BookPaper, "authorized")
	if err != nil {
		return err
	}
	for _, r := range rows {
		if r.Mode != "sim_auto" {
			continue
		}
		if !e.clock().Before(r.Plan.ValidUntil) {
			_ = e.withLedger(ctx, "global", func(c context.Context) error {
				return store.SetPlanState(c, r.Plan.PlanID, r.Plan.Revision, "authorized", "expired", r.Mode, r.Operator, "有效期结束")
			})
			continue
		}
		// Non-triggered conditions are expected; current status remains visible.
		if _, err = e.ExecutePlan(ctx, r.Plan.PlanID, r.Plan.Revision, ""); err != nil {
			if errors.Is(err, decision.ErrVersionChanged) || errors.Is(err, decision.ErrLedgerChanged) || errors.Is(err, decision.ErrExpired) || errors.Is(err, ErrUncertain) {
				if updateErr := e.withLedger(ctx, "global", func(c context.Context) error {
					return store.SetPlanState(c, r.Plan.PlanID, r.Plan.Revision, "authorized", "blocked", r.Mode, r.Operator, err.Error())
				}); updateErr != nil && !errors.Is(updateErr, ErrConflict) {
					return updateErr
				}
			}
			e.log.Debugf("plan %s waiting: %v", r.Plan.PlanID, err)
		}
	}
	return nil
}
func (e *Engine) Intents(ctx context.Context, account string) ([]Intent, error) {
	book, err := bookOf(account)
	if err != nil {
		return nil, err
	}
	store, err := e.automationStore()
	if err != nil {
		return nil, err
	}
	return store.ListIntents(ctx, book)
}

// AutoGates 是自动消费的过滤闸门（M13-T02）：买方向+pending 之外的条目按
// 配置裁决。阈值含边界；模板为闭集白名单；AllowDegraded=false 时 AI 降级
// 信号一律不自动开新仓（治理：AI 全挂时不靠裸规则单开仓）。
type AutoGates struct {
	ScoreMin      float64
	Templates     map[string]bool
	AllowDegraded bool
}

// DefaultAutoGates 是出厂闸门（默认两生产模板、阈值 80、AI 降级不自动开新仓）。
// 运行期由 appcfg 键 app.trade.auto.score_min / .templates / .allow_degraded 盖写，
// 键校验在 pkg/params 白名单；模板别名集与 pkg/rules 注册常量同源。
func DefaultAutoGates() AutoGates {
	return AutoGates{ScoreMin: 80, Templates: map[string]bool{rules.NameFirstBoard: true, rules.NamePullback: true}}
}

// AutoSignal 是 strategy.signal.updated 载荷里自动消费者关心的裁剪
// （载荷契约见 M06 设计文档第 12 节）；biz 不 import strategy 包，字段名对齐载荷 json 键。
type AutoSignal struct {
	SignalID   int
	Book       string
	Status     string
	Symbol     string
	Strategy   string
	SignalType string
	FinalScore float64
	AIDegraded bool
}

// AutoOutcome 是这一次自动消费的落点。Skipped 非空表示被过滤或去重（成功处理
// 的一种，不是失败）；非空 return 且 err 非 nil 时意图已占位、留给人工核对。
type AutoOutcome struct {
	Plan    PlanRecord
	Skipped string
}

// AutoConsume 把一条信号事件按过滤链裁决，通过则 CreatePlan(sim_auto)+AuthorizePlan。
// 只下单 SIM 账本；appcfg/环境读取由 core 组装进 gates，biz 只认 Gates 本体。
func (e *Engine) AutoConsume(ctx context.Context, s AutoSignal, gates AutoGates) (AutoOutcome, error) {
	if s.SignalID <= 0 {
		return AutoOutcome{}, fmt.Errorf("%w: 信号编号非法", ErrInvalid)
	}
	if gates.ScoreMin <= 0 {
		gates.ScoreMin = DefaultAutoGates().ScoreMin
	}
	if len(gates.Templates) == 0 {
		gates.Templates = DefaultAutoGates().Templates
	}
	switch {
	case s.Book != "" && s.Book != BookPaper:
		// 全自动只进 SIM；live 账本由 T03/T05 双闸另行放行，这不是可由配置绕过的口。
		return AutoOutcome{Skipped: "book"}, nil
	case s.SignalType != "buy":
		return AutoOutcome{Skipped: "non_buy"}, nil
	case s.Status != "pending":
		return AutoOutcome{Skipped: "status"}, nil
	case s.FinalScore < gates.ScoreMin:
		return AutoOutcome{Skipped: "score"}, nil
	case !gates.Templates[s.Strategy]:
		return AutoOutcome{Skipped: "template"}, nil
	case s.AIDegraded && !gates.AllowDegraded:
		return AutoOutcome{Skipped: "ai_degraded"}, nil
	}
	durable, ok := e.store.(LedgerStore)
	if !ok {
		return AutoOutcome{}, fmt.Errorf("%w: durable ledger unavailable", ErrInvalid)
	}
	if _, err := e.automationStore(); err != nil {
		return AutoOutcome{}, err
	}
	id := strconv.Itoa(s.SignalID)
	claim := Intent{
		BusinessKey:   "autosignal:SIM:default:" + id,
		ClientOrderID: "autosignal-" + id,
		RequestHash: decision.Hash(struct {
			ID                           int
			Book, Symbol, Side, Strategy string
		}{s.SignalID, BookPaper, s.Symbol, s.SignalType, s.Strategy}),
		Book:  BookPaper,
		State: "claimed",
	}
	if claim.RequestHash == "" {
		return AutoOutcome{}, fmt.Errorf("%w: 意图摘要不可用", ErrInvalid)
	}
	in, created, err := durable.ClaimIntent(ctx, claim)
	if err != nil {
		return AutoOutcome{}, err
	}
	if !created {
		if in.RequestHash != claim.RequestHash || in.ClientOrderID != claim.ClientOrderID {
			return AutoOutcome{}, fmt.Errorf("%w: 同一信号自动意图身份不一致，需人工核对", ErrConflict)
		}
		return AutoOutcome{Skipped: "duplicate"}, nil
	}
	plan, err := e.CreatePlan(ctx, s.SignalID, "SIM", "", 0)
	if err != nil {
		return AutoOutcome{}, err
	}
	plan, err = e.AuthorizePlan(ctx, plan.Plan.PlanID, plan.Plan.Revision, "sim_auto", "auto")
	if err != nil {
		return AutoOutcome{}, err
	}
	if err = durable.FinishIntent(ctx, claim.ClientOrderID, plan.State, plan.Plan.PlanID); err != nil {
		return AutoOutcome{}, err
	}
	return AutoOutcome{Plan: plan}, nil
}
