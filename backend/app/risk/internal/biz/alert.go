package biz

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"server/pkg/alert"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/uuid"
)

// ErrAlertNotFound 规则不存在或刚被删掉（Edit/Rearm/Delete/评估落库都可能遇到）。
var ErrAlertNotFound = errors.New("告警规则不存在")

// noteMaxLen 与 alert_rule 表列宽一致。
const noteMaxLen = 200

// evalThrottle 评估节拍的最小间隔：快照消息和 3 秒兜底节拍都会调 Tick，规则评估不必更密。
const evalThrottle = 2 * time.Second

// AlertFire 是一次规则触发：同一步的规则落库与通知 outbox 必须原子（见 AlertStore.ApplyAlertStep）。
type AlertFire struct {
	Rule  alert.Rule
	Price float64
	At    time.Time
}

// AlertStore 是 data 层提供的规则持久化端口。
type AlertStore interface {
	ListAlertRules(ctx context.Context) ([]alert.Rule, error)
	// ActiveAlertRules 只回 active 行——评估节拍不碰已触发的。
	ActiveAlertRules(ctx context.Context) ([]alert.Rule, error)
	GetAlertRule(ctx context.Context, id string) (alert.Rule, error)
	CreateAlertRule(ctx context.Context, r alert.Rule, operator string) error
	// ReplaceAlertRule 整行白名单替换（Edit/Rearm 的产物），找不到返回 ErrAlertNotFound。
	ReplaceAlertRule(ctx context.Context, r alert.Rule, operator string) error
	DeleteAlertRule(ctx context.Context, id, operator string) error
	// ApplyAlertStep 落一步评估结果：以 (rule_id, armed_at, status=active, 无 triggered_at)
	// 为乐观前置 CAS 应用；过期节拍（重挂/编辑/并发先触发）applied=false 且不落库不投 outbox；
	// 规则不存在返回 ErrAlertNotFound。fire 非 nil 时同一事务写通知 outbox。
	ApplyAlertStep(ctx context.Context, next alert.Rule, fire *AlertFire) (bool, error)
}

// AlertCenter 盯盘告警规则的用例与节拍评估（G3）。CRUD 走 pkg/alert 的创建/重建语义，
// Tick 由 risk core 在行情快照节拍（含 3 秒兜底）调用；触发通知经 store 落进 outbox，
// 由既有投递链送 notify.request——本层不直接发 NATS。
type AlertCenter struct {
	store  AlertStore
	quotes QuoteSource
	now    func() time.Time
	log    *log.Helper

	mu       sync.Mutex
	lastEval time.Time
}

func NewAlertCenter(store AlertStore, quotes QuoteSource, logger log.Logger) *AlertCenter {
	return &AlertCenter{
		store: store, quotes: quotes, now: time.Now,
		log: log.NewHelper(log.With(logger, "module", "risk/alerts")),
	}
}

func validNote(note string) (string, error) {
	note = strings.TrimSpace(note)
	if len(note) > noteMaxLen {
		return "", fmt.Errorf("%w: 备注超过 200 字", ErrInvalid)
	}
	return note, nil
}

func (a *AlertCenter) List(ctx context.Context) ([]alert.Rule, error) {
	return a.store.ListAlertRules(ctx)
}

func (a *AlertCenter) Create(ctx context.Context, symbol, condition string, target float64, note, operator string) (alert.Rule, error) {
	cond, err := alert.ParseCondition(condition)
	if err != nil {
		return alert.Rule{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	note, err = validNote(note)
	if err != nil {
		return alert.Rule{}, err
	}
	r, err := alert.Create("alr-"+uuid.NewString(), symbol, cond, target, a.now())
	if err != nil {
		return alert.Rule{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	r.Note = note
	if err := a.store.CreateAlertRule(ctx, r, strings.TrimSpace(operator)); err != nil {
		return alert.Rule{}, err
	}
	return r, nil
}

// Edit 白名单重建：触发态字段（status/last_price/triggered_at）清零，基线重新建立。
func (a *AlertCenter) Edit(ctx context.Context, id, symbol, condition string, target float64, note, operator string) (alert.Rule, error) {
	cond, err := alert.ParseCondition(condition)
	if err != nil {
		return alert.Rule{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	note, err = validNote(note)
	if err != nil {
		return alert.Rule{}, err
	}
	cur, err := a.store.GetAlertRule(ctx, strings.TrimSpace(id))
	if err != nil {
		return alert.Rule{}, err
	}
	next, err := alert.Edit(cur, symbol, cond, target, a.now())
	if err != nil {
		return alert.Rule{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	next.Note = note
	if err := a.store.ReplaceAlertRule(ctx, next, strings.TrimSpace(operator)); err != nil {
		return alert.Rule{}, err
	}
	return next, nil
}

// Rearm 按原参数重建（清触发态、换新基线），触发后的规则由此重新布防。
func (a *AlertCenter) Rearm(ctx context.Context, id, operator string) (alert.Rule, error) {
	cur, err := a.store.GetAlertRule(ctx, strings.TrimSpace(id))
	if err != nil {
		return alert.Rule{}, err
	}
	next, err := alert.Rearm(cur, a.now())
	if err != nil {
		return alert.Rule{}, err
	}
	if err := a.store.ReplaceAlertRule(ctx, next, strings.TrimSpace(operator)); err != nil {
		return alert.Rule{}, err
	}
	return next, nil
}

func (a *AlertCenter) Delete(ctx context.Context, id, operator string) error {
	return a.store.DeleteAlertRule(ctx, strings.TrimSpace(id), strings.TrimSpace(operator))
}

// Tick 在行情快照节拍评估所有 active 规则。节流不重复评估；单条规则出错不阻塞其他规则。
// 触发的规则经 ApplyAlertStep 同事务落库 + outbox；节拍期间被重挂/并发先触发的过期评估
// 由 CAS 作废（applied=false），重启或重复节拍不会重复发通知（dedup_key 兜底）。
func (a *AlertCenter) Tick(ctx context.Context) {
	now := a.now()
	a.mu.Lock()
	if now.Sub(a.lastEval) < evalThrottle {
		a.mu.Unlock()
		return
	}
	a.lastEval = now
	a.mu.Unlock()

	rules, err := a.store.ActiveAlertRules(ctx)
	if err != nil {
		a.log.Warnf("alert rules read: %v", err)
		return
	}
	if len(rules) == 0 {
		return
	}
	seen := map[string]bool{}
	symbols := make([]string, 0, len(rules))
	for _, r := range rules {
		if !seen[r.Symbol] {
			seen[r.Symbol] = true
			symbols = append(symbols, r.Symbol)
		}
	}
	snaps, err := a.quotes.Quotes(ctx, symbols)
	if err != nil {
		a.log.Warnf("alert quotes read: %v", err)
		return
	}
	for _, r := range rules {
		snap, ok := snaps[r.Symbol]
		if !ok || snap.Last <= 0 {
			continue
		}
		next, fired := alert.Step(r, snap.Last, now)
		if !fired && samePrice(next.LastPrice, r.LastPrice) {
			continue // 价未变，不落库
		}
		var fire *AlertFire
		if fired {
			fire = &AlertFire{Rule: next, Price: snap.Last, At: now}
		}
		applied, err := a.store.ApplyAlertStep(ctx, next, fire)
		if err != nil {
			a.log.Warnf("alert step persist %s: %v", r.ID, err)
			continue
		}
		if !applied && fire != nil {
			// 算出的触发被并发重挂/抢先触发作废：提示一拍，下拍按最新读面再评。
			a.log.Warnf("alert step %s: stale fire discarded (rearmed or concurrently fired)", r.ID)
		}
	}
}

func samePrice(a, b *float64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
