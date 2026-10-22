package tradecal

import (
	"context"
	"time"
)

// Watcher 轮询本地时段，发生变化时由 Publish 发出 sys.session.changed。
// 时段边界都在整点/5 分钟刻度上，15s 轮询足够贴近；发布失败不进位，
// 下一拍重试，绝不静默丢一次边界。
type Watcher struct {
	cal      *Calendar
	interval time.Duration
	publish  func(ctx context.Context, s Session) error
	logf     func(format string, args ...any)
	now      func() time.Time // 测试注入；生产默认 time.Now
	prev     Session
	seen     bool
}

// NewWatcher 建一个时段监察器。cal 为 nil 时用内置日历 Default。
// publish 必须只在事件真正发出后返回 nil；logf 可传 nil。
func NewWatcher(cal *Calendar, interval time.Duration, publish func(context.Context, Session) error, logf func(string, ...any)) *Watcher {
	if cal == nil {
		cal = Default
	}
	if interval <= 0 {
		interval = 15 * time.Second
	}
	return &Watcher{cal: cal, interval: interval, publish: publish, logf: logf, now: time.Now}
}

// Run 启动后立即发一条当前时段（停机期间滞留的收盘消息能立刻清算），随后进入轮询。
func (w *Watcher) Run(ctx context.Context) error {
	w.step(ctx, w.now())
	t := time.NewTicker(w.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case now := <-t.C:
			w.step(ctx, now)
		}
	}
}

func (w *Watcher) step(ctx context.Context, now time.Time) {
	cur, err := w.cal.SessionAt(now)
	if err != nil {
		w.warn("session: %v", err)
		return
	}
	// seen 前的第一条永远发：流程重启后消费端需要一次边界信号来清算滞留消息，
	// 即使时段相对上次发布没变（上次状态随进程消亡）。
	if w.seen && !Changed(w.prev, cur) {
		return
	}
	if err := w.publish(ctx, cur); err != nil {
		w.warn("session publish: %v", err)
		return // 不进位，下一拍重试同一边界
	}
	w.prev, w.seen = cur, true
}

func (w *Watcher) warn(format string, args ...any) {
	if w.logf != nil {
		w.logf(format, args...)
	}
}
