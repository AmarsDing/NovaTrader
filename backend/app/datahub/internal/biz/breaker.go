package biz

import (
	"sync"
	"time"
)

// 断路状态。
const (
	StateClosed      = "closed"
	StateOpen        = "open"
	StateHalfOpen    = "half_open"
	StateDisabled    = "disabled"
	StateUnavailable = "unavailable"
)

// Breaker 是「数据域 + 源」的断路器。连续失败 failures 次打开；
// 打开 cooldown 后放一次探测，成功关闭（回切），失败重新计时。FR-01-02。
type Breaker struct {
	mu       sync.Mutex
	failures int
	cooldown time.Duration
	state    string
	fails    int
	openedAt time.Time
	probing  bool
}

// Transition 描述一次状态变化，供告警使用。
type Transition struct {
	From string
	To   string
}

func NewBreaker(failures int, cooldown time.Duration) *Breaker {
	if failures <= 0 {
		failures = 3
	}
	if cooldown <= 0 {
		cooldown = time.Minute
	}
	return &Breaker{failures: failures, cooldown: cooldown, state: StateClosed}
}

// Allow 判断这次能否调用。打开且冷却已过时转为半开，只放行一个探测请求。
func (b *Breaker) Allow(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case StateClosed:
		return true
	case StateOpen:
		if now.Sub(b.openedAt) < b.cooldown {
			return false
		}
		b.state = StateHalfOpen
		b.probing = true
		return true
	case StateHalfOpen:
		if b.probing {
			return false
		}
		b.probing = true
		return true
	}
	return true
}

// Record 记录一次调用结果，返回状态是否变化。
func (b *Breaker) Record(ok bool, now time.Time) (Transition, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	from := b.state
	b.probing = false
	if ok {
		b.fails = 0
		b.state = StateClosed
	} else {
		b.fails++
		if b.state == StateHalfOpen || b.fails >= b.failures {
			b.state = StateOpen
			b.openedAt = now
		}
	}
	// 半开探测失败仍算「断开中」，不重复告警。
	if from == b.state || (from == StateHalfOpen && b.state == StateOpen) {
		return Transition{}, false
	}
	return Transition{From: from, To: b.state}, true
}

// Release 放弃一次已放行的探测（例如被配额或上下文取消挡住），不计成败。
func (b *Breaker) Release() {
	b.mu.Lock()
	b.probing = false
	b.mu.Unlock()
}

// State 返回当前状态和连续失败次数。
func (b *Breaker) State() (string, int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state, b.fails
}
