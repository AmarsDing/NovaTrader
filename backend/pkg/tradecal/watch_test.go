package tradecal

import (
	"context"
	"errors"
	"testing"
	"time"
)

type capture struct {
	calls []Session
	fail  bool
}

func (c *capture) publish(_ context.Context, s Session) error {
	if c.fail {
		return errors.New("bus down")
	}
	c.calls = append(c.calls, s)
	return nil
}

func on(day string, hm int) time.Time {
	d, err := time.ParseInLocation("2006-01-02", day, Shanghai())
	if err != nil {
		panic(err)
	}
	return d.Add(time.Duration(hm) * time.Minute)
}

func TestWatcherPublishesInitialAndBoundaries(t *testing.T) {
	cap := &capture{}
	w := NewWatcher(Default, time.Second, cap.publish, nil)

	w.step(context.Background(), on("2026-10-09", 9*60+14)) // PRE_OPEN
	if len(cap.calls) != 1 || cap.calls[0].Phase != PreOpen {
		t.Fatalf("初始应发一条 PRE_OPEN: %+v", cap.calls)
	}
	w.step(context.Background(), on("2026-10-09", 9*60+14)) // 同时刻重扫不再发
	w.step(context.Background(), on("2026-10-09", 9*60+16)) // CALL_AUCTION
	if len(cap.calls) != 2 || cap.calls[1].Phase != CallAuction {
		t.Fatalf("跨过 09:15 应发 CALL_AUCTION: %+v", cap.calls)
	}
	w.step(context.Background(), on("2026-10-09", 14*60+29)) // PM_TRADING 未 LATE
	w.step(context.Background(), on("2026-10-09", 14*60+31)) // LATE 翻转也是一次变化
	last := cap.calls[len(cap.calls)-1]
	if last.Phase != PMTrading || !last.Late {
		t.Fatalf("14:30 后应发 PM_TRADING/LATE: %+v", cap.calls[len(cap.calls)-2:])
	}
	if got := len(cap.calls); got != 4 {
		t.Fatalf("LATE 前未发、翻转发一条，共 4 条，实得 %d: %+v", got, cap.calls)
	}
}

func TestWatcherRetriesAfterPublishFailure(t *testing.T) {
	cap := &capture{fail: true}
	w := NewWatcher(Default, time.Second, cap.publish, nil)

	w.step(context.Background(), on("2026-10-09", 11*60)) // 首发失败
	if w.seen {
		t.Fatal("发布失败不得进位")
	}
	cap.fail = false
	w.step(context.Background(), on("2026-10-09", 11*60)) // 同一时刻重试成功
	if len(cap.calls) != 1 || cap.calls[0].Phase != AMTrading {
		t.Fatalf("恢复后应补发同一边界: %+v", cap.calls)
	}
}

func TestWatcherClosedDay(t *testing.T) {
	cap := &capture{}
	w := NewWatcher(Default, time.Second, cap.publish, nil)
	w.step(context.Background(), on("2026-10-10", 10*60)) // 周六闭市
	if len(cap.calls) != 1 || cap.calls[0].Phase != Closed {
		t.Fatalf("闭市日应发 CLOSED: %+v", cap.calls)
	}
}
