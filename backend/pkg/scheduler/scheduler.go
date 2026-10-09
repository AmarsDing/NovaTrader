package scheduler

import (
	"context"
	"sync"
	"time"

	"server/pkg/tradecal"
)

// Job 是一项计划任务。Spec 为五段 cron，时区按上海时间解释。
type Job struct {
	Name           string
	Spec           string
	TradingDayOnly bool
	DependsOn      []string
	Timeout        time.Duration
	Retry          int
	MaxDelay       time.Duration
	Run            func(ctx context.Context) error
}

// Store 保存执行记录，用来补跑和避免同一时刻重复执行。
type Store interface {
	SucceededOn(ctx context.Context, name string, day time.Time) (bool, error)
	Start(ctx context.Context, name string, slot time.Time, attempt int) error
	Finish(ctx context.Context, name string, slot time.Time, attempt int, runErr error) error
}

// Scheduler 按交易日历触发任务。节假日上的仅交易日任务不会执行。
type Scheduler struct {
	cal   *tradecal.Calendar
	jobs  []Job
	store Store
}

func New(cal *tradecal.Calendar, store Store, jobs ...Job) *Scheduler {
	if cal == nil {
		cal = tradecal.Default
	}
	return &Scheduler{cal: cal, store: store, jobs: jobs}
}

// Tick 检查当前时刻到期的任务。错过的时刻在 MaxDelay 内会补跑一次。
// 同一轮里会多扫几遍，让依赖任务排在后面也能在这次 Tick 里执行。
func (s *Scheduler) Tick(ctx context.Context, now time.Time) error {
	now = now.In(tradecal.Shanghai())
	for pass := 0; pass < len(s.jobs); pass++ {
		progress := false
		for _, job := range s.jobs {
			ran, err := s.runJob(ctx, job, now)
			if err != nil {
				return err
			}
			if ran {
				progress = true
			}
		}
		if !progress {
			return nil
		}
	}
	return nil
}

func (s *Scheduler) runJob(ctx context.Context, job Job, now time.Time) (bool, error) {
	slot, err := Previous(job.Spec, now)
	if err != nil {
		return false, err
	}
	delay := job.MaxDelay
	if delay <= 0 {
		delay = 6 * time.Hour
	}
	if now.Sub(slot) > delay {
		return false, nil
	}
	if job.TradingDayOnly {
		open, err := s.cal.Open(slot)
		if err != nil {
			return false, err
		}
		if !open {
			return false, nil
		}
	}
	done, err := s.store.SucceededOn(ctx, job.Name, slot)
	if err != nil {
		return false, err
	}
	if done {
		return false, nil
	}
	for _, dep := range job.DependsOn {
		ok, err := s.store.SucceededOn(ctx, dep, slot)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	attempts := job.Retry + 1
	if attempts < 1 {
		attempts = 1
	}
	timeout := job.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	var runErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := s.store.Start(ctx, job.Name, slot, attempt); err != nil {
			return false, err
		}
		runCtx, cancel := context.WithTimeout(ctx, timeout)
		runErr = job.Run(runCtx)
		cancel()
		if err := s.store.Finish(ctx, job.Name, slot, attempt, runErr); err != nil {
			return false, err
		}
		if runErr == nil {
			return true, nil
		}
	}
	return false, runErr
}

// Memory 是测试和单进程用的执行记录。
type Memory struct {
	mu   sync.Mutex
	ok   map[string]map[string]bool
	runs []string
}

func NewMemory() *Memory {
	return &Memory{ok: map[string]map[string]bool{}}
}

func dayKey(t time.Time) string {
	return t.In(tradecal.Shanghai()).Format("2006-01-02")
}

func (m *Memory) SucceededOn(_ context.Context, name string, day time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ok[name][dayKey(day)], nil
}

func (m *Memory) Start(context.Context, string, time.Time, int) error { return nil }

func (m *Memory) Finish(_ context.Context, name string, slot time.Time, attempt int, runErr error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runs = append(m.runs, name)
	if runErr == nil {
		if m.ok[name] == nil {
			m.ok[name] = map[string]bool{}
		}
		m.ok[name][dayKey(slot)] = true
	}
	_ = attempt
	return nil
}

func (m *Memory) Calls(name string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, item := range m.runs {
		if item == name {
			n++
		}
	}
	return n
}
