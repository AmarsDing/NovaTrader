package biz

// 声明式保护规则（R16）状态机，借法 freqtrade protections（StoplossGuard / CooldownPeriod）。
//
// 数据源：strategy.signal.updated 终态闭合事件（status=closed，携带 pnl_pct/closed_at/
// stock_code/book）。信号闭合 = 策略视角的一笔完结交易，与 freqtrade 用自身交易记录
// 驱动保护规则同构。
//
// 纪律：
//   - pnl_pct 缺失（nil）= 盈亏未知：不计入连亏序列（「没取到≠0」，未知不冒充亏损），
//     也不打断既有连亏计数——跳过该笔看更早的；冷却期不看盈亏，不受此限。
//   - 锁是分钟级易失态，内存持有、按绝对时间自然过期：事件断流不放大（无新锁），
//     重启丢锁属已知边界（硬防线在 R01–R15 与熔断，保护锁是软增强）。
//   - book 维度隔离：paper/live 各自的闭合序列各自锁，互不污染。
//   - 锁的基准时刻取事件 closed_at（非收到时刻）：回放旧事件产生的锁已过期即弃，
//     天然防补发放大。
//   - 序列按 closed_at 有序（乱序到达时重排），并按 signal_id 在环内去重：同一信号重复
//     投递不重复计入连亏。
import (
	"fmt"
	"sort"
	"time"

	"server/pkg/risk"
)

// closedRingCap 是单 symbol 闭合序列的容量上限。连亏窗最大 20（params 校验上限），
// 留一倍余量给未知盈亏的跳过扫描。
const closedRingCap = 40

// closedTrade 是一笔已闭合信号交易。known=false 表示 pnl 缺失（未知，不计入连亏）。
// id 是 signal_id，仅用于环内去重（同一信号重复闭合事件不重复计数）。
type closedTrade struct {
	id    int
	pnl   float64
	known bool
	at    time.Time
}

// ProtectionLock 是一条活跃锁（引擎 State/告警共用）。
type ProtectionLock struct {
	Reason string    `json:"reason"`
	Until  time.Time `json:"until"`
}

type symbolProtection struct {
	closed []closedTrade
	lock   *ProtectionLock
}

// protections 持有全部 (book, symbol) 的闭合序列与活跃锁。零值不可用，须 newProtections。
type protections struct {
	bySymbol map[string]map[string]*symbolProtection // book → symbol → 状态
}

func newProtections() *protections {
	return &protections{bySymbol: map[string]map[string]*symbolProtection{}}
}

func (p *protections) slot(book, symbol string) *symbolProtection {
	bk, ok := p.bySymbol[book]
	if !ok {
		bk = map[string]*symbolProtection{}
		p.bySymbol[book] = bk
	}
	sp, ok := bk[symbol]
	if !ok {
		sp = &symbolProtection{}
		bk[symbol] = sp
	}
	return sp
}

// bookOfAccount 把风控账户类型映射回信号 book（paper/live）。
func bookOfAccount(account risk.AccountType) string {
	if account == risk.LIVE {
		return "live"
	}
	return "paper"
}

// onClosed 记录一笔闭合并评估是否触发新锁。触发时返回锁与 true；已锁定的 symbol
// 在旧锁到期前不因新闭合刷新锁（锁内再亏不续命，到期自然释放后再评）。
// closedAt 缺失时按 now 兜底（冷却基准必须有锚点，宁宽勿缺）。
// signalID 为 0（缺省）时跳过去重；非 0 时同一 signal_id 在环内只计一次。
func (p *protections) onClosed(book, symbol string, signalID int, pnl *float64, closedAt *time.Time, params risk.Params, now time.Time) (ProtectionLock, bool) {
	at := now
	if closedAt != nil && !closedAt.IsZero() {
		at = *closedAt
	}
	sp := p.slot(book, symbol)
	if signalID != 0 {
		for i := range sp.closed {
			if sp.closed[i].id == signalID {
				return ProtectionLock{}, false // 同一信号的重复闭合事件：不重复计入
			}
		}
	}
	ct := closedTrade{id: signalID, known: pnl != nil, at: at}
	if pnl != nil {
		ct.pnl = *pnl
	}
	sp.closed = append(sp.closed, ct)
	// 乱序到达（补发/重投）时按 closed_at 重排，保证「最近 N 笔」口径正确。
	if n := len(sp.closed); n > 1 && sp.closed[n-2].at.After(at) {
		sort.SliceStable(sp.closed, func(i, j int) bool { return sp.closed[i].at.Before(sp.closed[j].at) })
	}
	if len(sp.closed) > closedRingCap {
		sp.closed = sp.closed[len(sp.closed)-closedRingCap:]
	}

	// 旧锁未到期不刷新（含「锁内再闭合」场景），到期即弃。
	if sp.lock != nil {
		if sp.lock.Until.After(now) {
			return ProtectionLock{}, false
		}
		sp.lock = nil
	}

	streak := params.ProtectionLossStreak
	switch {
	case streak > 0 && p.lossStreakHit(sp, streak):
		lock := ProtectionLock{
			Reason: fmt.Sprintf("连亏 %d 笔保护锁定", streak),
			Until:  at.Add(params.ProtectionLock),
		}
		sp.lock = &lock
		return lock, true
	case params.ProtectionCooldown > 0:
		lock := ProtectionLock{
			Reason: "平仓冷却期",
			Until:  at.Add(params.ProtectionCooldown),
		}
		sp.lock = &lock
		return lock, true
	}
	return ProtectionLock{}, false
}

// lossStreakHit 判断最近 n 笔已知盈亏的闭合是否全部亏损。未知盈亏的笔跳过
// （不重置计数也不计入）；已知盈亏不足 n 笔时不触发。
func (p *protections) lossStreakHit(sp *symbolProtection, n int) bool {
	seen := 0
	for i := len(sp.closed) - 1; i >= 0 && seen < n; i-- {
		ct := sp.closed[i]
		if !ct.known {
			continue
		}
		if ct.pnl >= 0 {
			return false
		}
		seen++
	}
	return seen == n
}

// view 返回 (book, symbol) 在 now 时刻的锁定可见状态。纯读：过期锁在此只报未锁定、
// 不清理——调用方 input() 仅持读锁（RLock），清理会与其他读者/写者竞争该字段。
// 过期锁由 onClosed 在该 symbol 下次闭合时替换，或随进程重启消失，两者都是分钟级
// 易失态的自然归宿（内存有界：只随「曾锁定过的 symbol 数」增长）。
func (p *protections) view(book, symbol string, now time.Time) risk.ProtectionView {
	bk, ok := p.bySymbol[book]
	if !ok {
		return risk.ProtectionView{}
	}
	sp, ok := bk[symbol]
	if !ok || sp.lock == nil || !sp.lock.Until.After(now) {
		return risk.ProtectionView{}
	}
	return risk.ProtectionView{Locked: true, Until: sp.lock.Until, Reason: sp.lock.Reason}
}
