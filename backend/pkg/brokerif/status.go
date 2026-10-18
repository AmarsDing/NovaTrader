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
