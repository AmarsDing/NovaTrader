package biz

import (
	"errors"
	"sort"
	"sync"
	"time"
)

// Health 是一个「数据域 + 源」的运行统计。FR-01-08。
type Health struct {
	Domain        string
	Source        string
	Priority      int
	Enabled       bool
	Official      bool
	State         string
	Failures      int
	Success       int64
	Failure       int64
	LastLatency   time.Duration
	AvgLatency    time.Duration
	LastSuccessAt time.Time
	LastFailureAt time.Time
	LastError     string
	QuotaUsed     int64
	QuotaLimit    int64
	Active        bool
	unavailable   bool
}

// SuccessRate 返回成功率，没有调用时为 0。
func (h Health) SuccessRate() float64 {
	n := h.Success + h.Failure
	if n == 0 {
		return 0
	}
	return float64(h.Success) / float64(n)
}

type healthBook struct {
	mu     sync.Mutex
	rows   map[string]*Health
	active map[string]string
}

func newHealthBook() *healthBook {
	return &healthBook{rows: map[string]*Health{}, active: map[string]string{}}
}

func healthKey(domain, source string) string { return domain + "|" + source }

func (b *healthBook) ensure(domain, source string, priority int, official bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	k := healthKey(domain, source)
	if _, ok := b.rows[k]; !ok {
		b.rows[k] = &Health{Domain: domain, Source: source, Priority: priority, Official: official, Enabled: true, State: StateClosed}
	}
}

// record 记一次调用。平均延迟用指数平滑，权重 0.2。
func (b *healthBook) record(domain, source string, latency time.Duration, err error, now time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	h := b.rows[healthKey(domain, source)]
	if h == nil {
		return
	}
	h.LastLatency = latency
	if h.AvgLatency == 0 {
		h.AvgLatency = latency
	} else {
		h.AvgLatency = time.Duration(0.8*float64(h.AvgLatency) + 0.2*float64(latency))
	}
	if err == nil {
		h.Success++
		h.LastSuccessAt = now
		h.LastError = ""
		h.unavailable = false
		b.active[domain] = source
		return
	}
	h.Failure++
	h.LastFailureAt = now
	msg := err.Error()
	if len(msg) > 500 {
		msg = msg[:500]
	}
	h.LastError = msg
	var ue *UnavailableError
	h.unavailable = errors.As(err, &ue)
}

func (b *healthBook) note(domain, source, msg string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if h := b.rows[healthKey(domain, source)]; h != nil {
		h.LastError = msg
	}
}

func (b *healthBook) list(fill func(h *Health)) []Health {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Health, 0, len(b.rows))
	for _, h := range b.rows {
		c := *h
		c.Active = b.active[h.Domain] == h.Source
		if fill != nil {
			fill(&c)
		}
		if c.State == StateClosed && c.unavailable {
			c.State = StateUnavailable
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Domain != out[j].Domain {
			return out[i].Domain < out[j].Domain
		}
		return out[i].Priority < out[j].Priority
	})
	return out
}

// seed 用库里的历史计数恢复统计。
func (b *healthBook) seed(h Health) {
	b.mu.Lock()
	defer b.mu.Unlock()
	row := b.rows[healthKey(h.Domain, h.Source)]
	if row == nil {
		return
	}
	row.Success = h.Success
	row.Failure = h.Failure
	row.AvgLatency = h.AvgLatency
	row.LastSuccessAt = h.LastSuccessAt
	row.LastFailureAt = h.LastFailureAt
	row.LastError = h.LastError
}
