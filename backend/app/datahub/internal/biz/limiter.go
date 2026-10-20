package biz

import (
	"context"
	"sync"
	"time"

	"server/pkg/tradecal"
)

// Limiter 按源限频：两次请求之间至少间隔 1/qps。qps<=0 不限。FR-01-03。
type Limiter struct {
	mu   sync.Mutex
	gap  time.Duration
	next time.Time
}

func NewLimiter(qps float64) *Limiter {
	l := &Limiter{}
	if qps > 0 {
		l.gap = time.Duration(float64(time.Second) / qps)
	}
	return l
}

// SetQPS 运行时改限频：重算间隔。qps<=0 恢复不限。当日已排队的等待点不追溯。
// gap 有了写路径后，Wait 的读也必须在锁内（见下）。
func (l *Limiter) SetQPS(qps float64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if qps > 0 {
		l.gap = time.Duration(float64(time.Second) / qps)
		return
	}
	l.gap = 0
}

// Wait 阻塞到可以发下一次请求，或 ctx 结束。
func (l *Limiter) Wait(ctx context.Context) error {
	if l == nil {
		return ctx.Err()
	}
	l.mu.Lock()
	if l.gap == 0 {
		l.mu.Unlock()
		return ctx.Err()
	}
	now := time.Now()
	at := l.next
	if at.Before(now) {
		at = now
	}
	l.next = at.Add(l.gap)
	l.mu.Unlock()
	d := time.Until(at)
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Quota 按上海自然日计数。limit<=0 不限。FR-01-03。
type Quota struct {
	mu    sync.Mutex
	limit int64
	day   string
	used  int64
}

func NewQuota(limit int64) *Quota { return &Quota{limit: limit} }

// SetLimit 运行时改当日配额上限：只改上限，不清当日已用计数。limit<=0 不限。
// 与 yaml daily_quota 同口径（0=不限）；app.ds.<source>.quota 运行时盖写走这里。
func (q *Quota) SetLimit(limit int64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.limit = limit
}

// Seed 用库里当天已用次数恢复计数，避免重启后配额被重置。
func (q *Quota) Seed(day time.Time, used int64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.day = dayOf(day)
	q.used = used
}

// Take 占用一次。超过当日上限时返回 false。
func (q *Quota) Take(now time.Time) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	d := dayOf(now)
	if d != q.day {
		q.day = d
		q.used = 0
	}
	if q.limit > 0 && q.used >= q.limit {
		return false
	}
	q.used++
	return true
}

// Usage 返回当日已用和上限。
func (q *Quota) Usage(now time.Time) (int64, int64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if dayOf(now) != q.day {
		return 0, q.limit
	}
	return q.used, q.limit
}

func dayOf(t time.Time) string {
	return t.In(tradecal.Shanghai()).Format("2006-01-02")
}
