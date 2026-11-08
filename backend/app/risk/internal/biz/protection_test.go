package biz

import (
	"testing"
	"time"

	"server/pkg/risk"
)

func fp(v float64) *float64 { return &v }

// protParams 构造只开指定保护规则的参数。
func protParams(streak int, lock, cooldown time.Duration) risk.Params {
	p := risk.Defaults()
	p.ProtectionLossStreak = streak
	p.ProtectionLock = lock
	p.ProtectionCooldown = cooldown
	return p
}

func TestProtectionLossStreak(t *testing.T) {
	base := time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
	p := newProtections()
	params := protParams(3, time.Hour, 0)

	// 前两笔亏损不触发。
	for i := 0; i < 2; i++ {
		at := base.Add(time.Duration(i) * time.Minute)
		if _, hit := p.onClosed("paper", "000001.SZ", i+1, fp(-0.01), &at, params, at); hit {
			t.Fatalf("must not lock on close #%d", i+1)
		}
	}
	// 第三笔连亏触发。
	at := base.Add(2 * time.Minute)
	lock, hit := p.onClosed("paper", "000001.SZ", 3, fp(-0.02), &at, params, at)
	if !hit || lock.Until != at.Add(time.Hour) {
		t.Fatalf("want lock at %v, got hit=%v lock=%+v", at.Add(time.Hour), hit, lock)
	}
	// 已锁期间新闭合不刷新锁（关键：锁内再亏不续命）。
	at2 := at.Add(10 * time.Minute)
	if _, hit := p.onClosed("paper", "000001.SZ", 4, fp(-0.05), &at2, params, at2); hit {
		t.Fatal("must not refresh an active lock")
	}
	if v := p.view("paper", "000001.SZ", at2); !v.Locked || v.Until != at.Add(time.Hour) {
		t.Fatalf("lock must keep original expiry: %+v", v)
	}
}

func TestProtectionWinResetsStreak(t *testing.T) {
	base := time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
	p := newProtections()
	params := protParams(3, time.Hour, 0)
	seq := []float64{-0.01, -0.01, 0.02, -0.01, -0.01} // 中间一笔盈利打断
	for i, v := range seq {
		at := base.Add(time.Duration(i) * time.Minute)
		if _, hit := p.onClosed("paper", "000001.SZ", i+1, fp(v), &at, params, at); hit {
			t.Fatalf("must not lock: win resets streak (close #%d)", i+1)
		}
	}
}

func TestProtectionUnknownPnlSkipped(t *testing.T) {
	base := time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
	p := newProtections()
	params := protParams(3, time.Hour, 0)
	// 亏损、未知、亏损、亏损 → 跳过未知，凑齐 3 笔已知亏损即锁。
	seq := []*float64{fp(-0.01), nil, fp(-0.01), fp(-0.01)}
	var hit bool
	for i, v := range seq {
		at := base.Add(time.Duration(i) * time.Minute)
		_, hit = p.onClosed("paper", "000001.SZ", i+1, v, &at, params, at)
	}
	if !hit {
		t.Fatal("unknown pnl must be skipped, not reset the streak")
	}
}

func TestProtectionCooldown(t *testing.T) {
	base := time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
	p := newProtections()
	params := protParams(0, time.Hour, 15*time.Minute) // 连亏关，只开冷却
	at := base
	lock, hit := p.onClosed("paper", "600519.SH", 1, fp(0.03), &at, params, at) // 盈利也触发冷却
	if !hit || lock.Reason != "平仓冷却期" || lock.Until != at.Add(15*time.Minute) {
		t.Fatalf("cooldown lock wrong: hit=%v lock=%+v", hit, lock)
	}
}

func TestProtectionBookIsolation(t *testing.T) {
	base := time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
	p := newProtections()
	params := protParams(0, time.Hour, 10*time.Minute)
	at := base
	p.onClosed("paper", "000001.SZ", 1, fp(-0.01), &at, params, at) // 只锁 paper
	if v := p.view("live", "000001.SZ", at); v.Locked {
		t.Fatal("live book must not be locked by a paper close")
	}
	if v := p.view("paper", "000001.SZ", at); !v.Locked {
		t.Fatal("paper book should be locked")
	}
}

func TestProtectionLockBaseTimeAndExpiry(t *testing.T) {
	now := time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
	p := newProtections()
	params := protParams(0, time.Hour, 10*time.Minute)

	// 锁的基准时刻取事件 closed_at：一笔 2 小时前的旧闭合，锁早已过期。
	old := now.Add(-2 * time.Hour)
	lock, hit := p.onClosed("paper", "000001.SZ", 1, fp(-0.01), &old, params, now)
	if !hit {
		t.Fatal("a close should still register as a trigger")
	}
	if !lock.Until.Equal(old.Add(10 * time.Minute)) {
		t.Fatalf("lock base must be closed_at, got %v", lock.Until)
	}
	// 回放旧事件产生的锁已过期，view 应为未锁定。
	if v := p.view("paper", "000001.SZ", now); v.Locked {
		t.Fatalf("stale lock must not be visible: %+v", v)
	}
}

func TestProtectionCooldownZeroClosesRule(t *testing.T) {
	base := time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
	p := newProtections()
	params := protParams(0, time.Hour, 0) // 两个规则都关
	at := base
	if _, hit := p.onClosed("paper", "000001.SZ", 1, fp(-0.9), &at, params, at); hit {
		t.Fatal("all protections off must never lock")
	}
}

// TestProtectionSignalIDDedup 覆盖同一 signal_id 的重复闭合事件只计一次。
func TestProtectionSignalIDDedup(t *testing.T) {
	base := time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
	p := newProtections()
	params := protParams(2, time.Hour, 0)

	// 同一 signal_id=7 投递三次（重复），只应计 1 笔，不足连亏 2，故不锁。
	for i := 0; i < 3; i++ {
		at := base.Add(time.Duration(i) * time.Minute)
		if _, hit := p.onClosed("paper", "000001.SZ", 7, fp(-0.01), &at, params, at); hit {
			t.Fatal("duplicate signal_id must not be counted more than once")
		}
	}
	// 换一个 signal_id 补第二笔，凑齐连亏 2 → 锁。
	at := base.Add(3 * time.Minute)
	if _, hit := p.onClosed("paper", "000001.SZ", 8, fp(-0.01), &at, params, at); !hit {
		t.Fatal("a distinct second loss should lock at streak 2")
	}
}

// TestProtectionRingOrderByClosedAt 覆盖乱序到达的判决差异：最新一笔是盈利却先到，
// 若按到达顺序扫描会把更老的两笔亏损误判为「最近两笔连亏」。按 closed_at 重排后应不锁。
func TestProtectionRingOrderByClosedAt(t *testing.T) {
	base := time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
	p := newProtections()
	params := protParams(2, time.Hour, 0)

	// 真实时序：t0 亏 → t1 亏 → t2 盈（最新）。正确结论：最新是盈，不连亏，不锁。
	// 到达顺序却反过来：先到 t2 盈，再到 t0 亏、t1 亏。
	// 若按到达顺序，环为 [t2W, t0L, t1L]，从尾部扫会误判 t1/t0 两笔连亏（假阳性）。
	t2 := base.Add(2 * time.Minute)
	if _, hit := p.onClosed("paper", "000001.SZ", 1, fp(0.02), &t2, params, t2); hit {
		t.Fatal("single win must not lock")
	}
	t0 := base
	p.onClosed("paper", "000001.SZ", 2, fp(-0.01), &t0, params, t0)
	t1 := base.Add(time.Minute)
	if _, hit := p.onClosed("paper", "000001.SZ", 3, fp(-0.01), &t1, params, t1); hit {
		t.Fatal("newest trade is a win: out-of-order older losses must not fabricate a streak")
	}
	// 序列应按 closed_at 升序存放。
	sp := p.bySymbol["paper"]["000001.SZ"]
	for i := 1; i < len(sp.closed); i++ {
		if sp.closed[i].at.Before(sp.closed[i-1].at) {
			t.Fatalf("ring not ordered by closed_at: %+v", sp.closed)
		}
	}
}
