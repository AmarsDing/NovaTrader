package brokerif

import (
	"fmt"
	"sync"
	"time"
)

// Status 是券商通道连接状态机（G7）。文件单侧的映射口径：idle=未连接或未配置；
// connected=目录可写且终端读取中；degraded=回报超时或目录一度不可写（仍在重试）；down=不可用。
type Status string

const (
	Idle      Status = "idle"
	Connected Status = "connected"
	Degraded  Status = "degraded"
	Down      Status = "down"
)

// edges 是合法迁移。没连上过不能 degraded；恢复只能先回 connected。
var edges = map[Status][]Status{
	Idle:      {Connected, Down},
	Connected: {Degraded, Down},
	Degraded:  {Connected, Down},
	Down:      {Connected},
}

// TransitionOK 报告 from→to 是否为合法迁移；同状态视为幂等刷新，恒真。
func TransitionOK(from, to Status) bool {
	if from == to {
		return true
	}
	for _, s := range edges[from] {
		if s == to {
			return true
		}
	}
	return false
}

// Tracker 跟踪一条通道的当前状态并在变化时通知订阅者。订阅是进程内回调；
// 把变化推上 position/alert 主题是服务侧接线的活，不在这层。
type Tracker struct {
	mu        sync.RWMutex
	status    Status
	since     time.Time
	listeners []func(from, to Status, at time.Time)
}

func NewTracker(now time.Time) *Tracker {
	return &Tracker{status: Idle, since: now}
}

func (t *Tracker) Current() (Status, time.Time) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.status, t.since
}

// Subscribe 追加一个状态变化监听，触发时拿 (from, to, at) 同步回调。
func (t *Tracker) Subscribe(fn func(from, to Status, at time.Time)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.listeners = append(t.listeners, fn)
}

// FailThreshold 是连续失败判定 down 的次数。偶发失败只降级，持续不可用才停机。
const FailThreshold = 3

// Observer 把 adapter 每次调用的成败折算成 Tracker 迁移（G7 验收③ 的同一份口径：
// 模拟撮合与东方财富文件单两个通道共用）。折算规则：
//   - 成功：连败清零并迁回 connected（恢复只能从 down 经 connected，边表已保证）；
//   - 未建立过连接（idle）的失败只累计，到阈值判 down——degraded 严格保留给
//     「连上过又出问题、仍在重试」，与 edges 注释的映射口径一致；
//   - 连上过之后的失败先 degraded，连败到阈值再 down；
//   - down 之后的失败不再迁移，保持 down 直到一次成功。
//
// Observer 是策略面：迁移错误（不可能的非法迁移）不回传，同态迁移幂等。
type Observer struct {
	tr    *Tracker
	now   func() time.Time
	mu    sync.Mutex
	fails int
}

func NewObserver(tr *Tracker, now func() time.Time) *Observer {
	if now == nil {
		now = time.Now
	}
	return &Observer{tr: tr, now: now}
}

// Ok 记录一次通道成功。Success 是最新的存活证据，无条件迁回 connected；
// 恢复只能从 down 经 connected，边表已保证。
//
// 计数清零与迁移在同一把 o.mu 内完成：与 Fail 的「锁内决策+应用」互斥，
// 每条记账整体线性化——不存在「Ok 已清连败、并发 Fail 拿旧决策照迁」的窗
// （旧窗：Transition(Connected) 幂等空转不改 CAS 前提，旧 Fail 决策仍能落地）。
// 迁移真正发生时监听回调在 o.mu 内同步运行（Tracker 锁已先放），回调耗时
// 只串行本 Observer 的后续记账，不反压 Tracker 本身。
func (o *Observer) Ok() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.fails = 0
	_ = o.tr.Transition(Connected, o.now())
}

// Fail 记录一次通道失败：请求层面的任何错误（连接失败、被拒、回包身份不符）都算。
// 决策（连败计数 + 当前状态读取）与应用在同一把 o.mu 内整体完成，与 Ok 互斥；
// TransitionIf 的 CAS 继续兜底：Observer 之外的写方若动了 Tracker，过期决策照样作废。
func (o *Observer) Fail() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.fails++
	fails := o.fails
	from, _ := o.tr.Current()
	var to Status
	switch {
	case from == Down:
		// 保持 down，直到 Ok 恢复。
	case fails >= FailThreshold:
		to = Down
	case from == Idle:
		// 未建连过的失败只累计（degraded 严格留给「连上过又出问题」）。
	default:
		to = Degraded
	}
	if to == "" {
		return // 无决策（down 保持 / idle 只累计），不打扰 Tracker。
	}
	_ = o.tr.TransitionIf(from, to, o.now())
}

// Transition 迁移状态：合法则更新并逐个通知；同状态幂等返回 nil 不通知；
// 非法迁移返回错误且状态不变。
func (t *Tracker) Transition(to Status, now time.Time) error {
	t.mu.Lock()
	if t.status == to {
		t.mu.Unlock()
		return nil
	}
	if !TransitionOK(t.status, to) {
		from := t.status
		t.mu.Unlock()
		return fmt.Errorf("brokerif: illegal status transition %s → %s", from, to)
	}
	from := t.status
	t.status, t.since = to, now
	listeners := append([]func(Status, Status, time.Time){}, t.listeners...)
	t.mu.Unlock()
	for _, fn := range listeners {
		fn(from, to, now)
	}
	return nil
}

// TransitionIf 以期望的当前状态为条件迁移：决策在锁外被并发改动顶掉时
// （当前状态已不再是决策时刻的 from），过期决策直接作废返回 nil，保持现状；
// 已处于 to 视同幂等。其余语义（边表校验、变化才通知）与 Transition 相同。
func (t *Tracker) TransitionIf(from, to Status, now time.Time) error {
	t.mu.Lock()
	if t.status == to || t.status != from {
		t.mu.Unlock()
		return nil
	}
	if !TransitionOK(from, to) {
		t.mu.Unlock()
		return fmt.Errorf("brokerif: illegal status transition %s → %s", from, to)
	}
	t.status, t.since = to, now
	listeners := append([]func(Status, Status, time.Time){}, t.listeners...)
	t.mu.Unlock()
	for _, fn := range listeners {
		fn(from, to, now)
	}
	return nil
}
