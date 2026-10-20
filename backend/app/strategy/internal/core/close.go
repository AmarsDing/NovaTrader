package core

// 本文件是 M06 闭仓归属环（strategy Runner 第 5 条常驻环 closeLoop）：
// 逐镜像 executionLoop（10s tick、启动即跑、每页 5s ctx、keyset IDGT 内存
// 游标、页 200、errors.Join 续跑、短页回卷 0），页内按 pair 去重，pair 粒度
// 编排 facts → biz.PlanClosure → 逐 plan uc.Transition(ActorTrade)。
// 库存恒等门只含合同 §1.3 枚举的数据矛盾（账本行脏/卖超/窗口内 Σ卖≠Σ买/
// 读取超限截断）：整 pair loud Errorf + 进程内冷却 10 分钟（§4.2 SOP 触发点）；
// facts 暂态读错误（PG 瞬断、页内 5s ctx 超时）不进门，errors.Join 续跑不冷却。
// Gate-B 超配 Warnf 计数，同一信号超配持续 >2 分钟升 Errorf（§4.1 探测）。

import (
	"context"
	"errors"
	"fmt"
	"time"

	"server/app/strategy/internal/biz"
	"server/pkg/observe"
)

const (
	closureCooldown     = 10 * time.Minute // 库存恒等门冷却：过期自动重试，不永久拉黑
	closureOverEscalate = 2 * time.Minute  // 超配持续升级阈值（§4.1 探测 → 人工介入信号）
)

// closurePairKey 是评估粒度：(book, symbol)。
type closurePairKey struct {
	book   string
	symbol string
}

// closurePairState 是进程内 pair 状态：冷却到期时刻 + 每信号超配计时。
type closurePairState struct {
	coolUntil time.Time
	over      map[int]time.Time // 首次发现某信号超配（Σ名下买入 > ExecutedVolume）的时刻
	escalated map[int]bool      // 超配持续 >2min 已升 Errorf，同一段超配只升一次
}

// closeLoop 逐镜像 executionLoop：启动即跑，之后 10s 一跳；重启全扫自愈。
func (r *Runner) closeLoop(ctx context.Context) {
	defer r.wg.Done()
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	sweep := func() {
		bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		finish := observe.ForService("strategy").Op("closure_projection").Begin()
		next, closed, err := r.reconcileClosuresPage(bounded, r.closureCursor, 200)
		observe.FinishError(finish, err)
		r.closureCursor = next
		if err != nil && ctx.Err() == nil {
			r.log.Warnf("closure projection: %v", err)
		}
		if closed > 0 {
			r.log.Infof("closure projections closed=%d", closed)
		}
	}
	sweep()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			sweep()
		}
	}
}

// reconcileClosuresPage 翻页取候选信号，页内按 pair 去重后逐 pair 评估；
// 单 pair 失败不拖垮整页（有界推进 + errors.Join，与 ReconcileExecutionsPage 同款）。
func (r *Runner) reconcileClosuresPage(ctx context.Context, after, limit int) (next, closed int, err error) {
	repo, ok := r.uc.ClosureProjection()
	if !ok {
		return after, 0, fmt.Errorf("strategy: closure projection repository unavailable")
	}
	if limit <= 0 || limit > 200 {
		limit = 200
	}
	ids, err := repo.CloseCandidates(ctx, r.uc.Settings().Book, after, limit)
	if err != nil {
		return after, 0, err
	}
	seen := map[closurePairKey]bool{}
	for _, id := range ids {
		if ctx.Err() != nil {
			return next, closed, errors.Join(err, ctx.Err())
		}
		next = id
		book, symbol, e := r.uc.ClosurePairOf(ctx, id)
		if e != nil {
			err = errors.Join(err, fmt.Errorf("closure candidate %d: %w", id, e))
			continue
		}
		key := closurePairKey{book: book, symbol: symbol}
		if seen[key] {
			continue // 页内按 pair 去重：同 pair 多候选只重放一次
		}
		seen[key] = true
		n, e := r.evaluateClosure(ctx, repo, key)
		closed += n
		if e != nil {
			err = errors.Join(err, fmt.Errorf("closure pair %s/%s: %w", key.book, key.symbol, e))
		}
	}
	if len(ids) < limit {
		next = 0 // 短页回卷：下轮从 0 重扫（重启全扫自愈同款）
	}
	return next, closed, err
}

// evaluateClosure 编排一次 pair 重放：facts → PlanClosure → 逐 plan Transition。
// ErrConflict / ErrExecutionProtected 是竞态正常结局，跳过不失败（ExpireDue 先例）。
// 错误分流（合同 §1.3 恒等门只含数据矛盾，不含读错误）：
//   - biz.PlanClosure 返回 error = 账本行脏 / 卖超 / 窗口内 Σ卖≠Σ买 → 恒等门冷却；
//   - facts 读取超限截断（biz.ErrClosureIdentityGate）→ 恒等门冷却；
//   - 其余 facts 读错误（PG 瞬断、页内 5s ctx 超时）= 暂态：上浮 errors.Join 续跑，不冷却不误报 SOP。
func (r *Runner) evaluateClosure(ctx context.Context, repo biz.ClosureProjectionRepo, key closurePairKey) (int, error) {
	now := r.now()
	if st := r.pairState[key]; st != nil && now.Before(st.coolUntil) {
		return 0, nil // 冷却期内跳过评估，周期计数照常（页级 observe 已计）
	}
	facts, err := repo.ClosurePairFacts(ctx, key.book, key.symbol)
	if err != nil {
		if errors.Is(err, biz.ErrClosureIdentityGate) {
			r.closureGate(key, now, err)
		}
		return 0, err
	}
	res, err := biz.PlanClosure(facts)
	if err != nil {
		// PlanClosure 只对恒等门枚举矛盾返回 error（biz/close.go 注释与实现）。
		r.closureGate(key, now, err)
		return 0, err
	}
	r.noteClosureOver(key, res.Over, now)
	closed := 0
	var perr error
	for _, plan := range res.Plans {
		_, terr := r.uc.Transition(ctx, plan.SignalID, biz.StatusClosed, plan.Reason, biz.ActorTrade,
			&biz.Close{ExitPrice: plan.ExitPrice, PnlPct: plan.PnlPct, At: plan.At})
		switch {
		case terr == nil:
			closed++
		case errors.Is(terr, biz.ErrConflict) || errors.Is(terr, biz.ErrExecutionProtected) ||
			errors.Is(terr, biz.ErrBadTransition):
			// 竞态正常结局：facts 无锁读 + CAS 构成 TOCTOU 安全网，下轮重来。
			// ErrBadTransition = 同信号一次重放跨两窗口产出多条 plan，首条落库后
			// 次条面对终态（CanTransition 拒 closed→closed）；信号中途漂入
			// expired/cancelled 同型。候选谓语下轮自动出清，不视为失败。
			continue
		default:
			perr = errors.Join(perr, fmt.Errorf("closure signal %d: %w", plan.SignalID, terr))
		}
	}
	r.closureStateCleanup(key, now)
	return closed, perr
}

// closureGate 生效库存恒等门（合同 §1.3 枚举矛盾，§4.2 人工 SOP 触发点）：
// 整 pair loud Errorf + 进程内冷却 10 分钟；过期自动重试，不永久拉黑。
func (r *Runner) closureGate(key closurePairKey, now time.Time, err error) {
	r.log.Errorf("[closure] pair %s/%s inventory identity gate violated, cooling %s: %v", key.book, key.symbol, closureCooldown, err)
	r.closureState(key).coolUntil = now.Add(closureCooldown)
}

// noteClosureOver 维护 Gate-B 超配分级：首见 Warnf 计数，持续 >2 分钟升 Errorf；
// 超配自愈的信号从计时册清除（投影环纠正 EV 后下轮正常归属）。
func (r *Runner) noteClosureOver(key closurePairKey, over []int, now time.Time) {
	var st *closurePairState
	if len(over) > 0 {
		st = r.closureState(key)
	} else if st = r.pairState[key]; st == nil {
		return
	}
	current := map[int]bool{}
	for _, id := range over {
		current[id] = true
		since, seen := st.over[id]
		switch {
		case !seen:
			st.over[id] = now
			r.log.Warnf("[closure] signal %d pair %s/%s over-allocated (named buy qty > executed volume); skipping until projection catches up",
				id, key.book, key.symbol)
		case !st.escalated[id] && now.Sub(since) > closureOverEscalate:
			st.escalated[id] = true
			r.log.Errorf("[closure] signal %d pair %s/%s over-allocation persists since %s (>%s); manual review per SOP",
				id, key.book, key.symbol, since.Format(time.RFC3339), closureOverEscalate)
		}
	}
	for id := range st.over {
		if !current[id] {
			delete(st.over, id)
			delete(st.escalated, id)
		}
	}
}

func (r *Runner) closureState(key closurePairKey) *closurePairState {
	st := r.pairState[key]
	if st == nil {
		st = &closurePairState{over: map[int]time.Time{}, escalated: map[int]bool{}}
		r.pairState[key] = st
	}
	return st
}

// closureStateCleanup 回收干净的 pair 状态：无冷却、无在册超配即删，防常驻膨胀。
func (r *Runner) closureStateCleanup(key closurePairKey, now time.Time) {
	st := r.pairState[key]
	if st == nil {
		return
	}
	if len(st.over) == 0 && (st.coolUntil.IsZero() || !now.Before(st.coolUntil)) {
		delete(r.pairState, key)
	}
}
