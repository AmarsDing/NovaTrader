package risk

import (
	"sync"
	"time"

	"server/pkg/tradecal"
)

// Rate 统计申报加撤单笔数：近 1 秒和当日（按上海日期换日）。并发安全。
type Rate struct {
	mu     sync.Mutex
	recent []time.Time
	day    string
	today  int
}

// Record 记一笔申报或撤单。
func (r *Rate) Record(t time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.roll(t)
	r.recent = append(r.recent, t)
	r.today++
}

// Counts 返回此刻近 1 秒和当日的笔数。
func (r *Rate) Counts(t time.Time) (lastSecond, today int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.roll(t)
	return len(r.recent), r.today
}

func (r *Rate) roll(t time.Time) {
	if d := t.In(tradecal.Shanghai()).Format("2006-01-02"); d != r.day {
		r.day, r.today, r.recent = d, 0, r.recent[:0]
	}
	cut := 0
	for cut < len(r.recent) && t.Sub(r.recent[cut]) >= time.Second {
		cut++
	}
	r.recent = r.recent[cut:]
}
