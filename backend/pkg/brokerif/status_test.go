package brokerif

import (
	"sync"
	"testing"
	"time"
)

var st0 = time.Date(2026, 10, 19, 9, 30, 0, 0, time.UTC)

func TestTransitionTable(t *testing.T) {
	legal := [][2]Status{
		{Idle, Connected}, {Idle, Down},
		{Connected, Degraded}, {Connected, Down},
		{Degraded, Connected}, {Degraded, Down},
		{Down, Connected},
	}
	for _, pair := range legal {
		from, to := pair[0], pair[1]
		if !TransitionOK(from, to) {
			t.Errorf("%s → %s should be legal", from, to)
		}
	}
	illegal := [][2]Status{
		{Idle, Degraded},  // 没连上过无 degraded
		{Connected, Idle}, // 回到未连接只能先 down
		{Degraded, Idle},
		{Down, Degraded}, // 恢复必须先回 connected
		{Down, Idle},
	}
	for _, pair := range illegal {
		from, to := pair[0], pair[1]
		if TransitionOK(from, to) {
			t.Errorf("%s → %s should be illegal", from, to)
		}
	}
	for _, s := range []Status{Idle, Connected, Degraded, Down} {
		if !TransitionOK(s, s) {
			t.Errorf("same-state %s should be an idempotent refresh", s)
		}
	}
}

func TestTrackerNotifiesOnChangeOnly(t *testing.T) {
	tr := NewTracker(st0)
	type event struct{ from, to Status }
	var got []event
	tr.Subscribe(func(from, to Status, at time.Time) { got = append(got, event{from, to}) })

	// 同状态幂等：不通知、不前进 since。
	if err := tr.Transition(Idle, st0.Add(time.Minute)); err != nil {
		t.Fatalf("same-state should be a no-op, got %v", err)
	}
	if s, since := tr.Current(); s != Idle || !since.Equal(st0) {
		t.Fatalf("same-state must not move since, got %s at %v", s, since)
	}
	if len(got) != 0 {
		t.Fatalf("no event for same-state, got %v", got)
	}

	path := []Status{Connected, Degraded, Connected, Down, Connected}
	for i, s := range path {
		if err := tr.Transition(s, st0.Add(time.Duration(i+1)*time.Minute)); err != nil {
			t.Fatalf("step %d → %s: %v", i, s, err)
		}
	}
	want := []event{{Idle, Connected}, {Connected, Degraded}, {Degraded, Connected}, {Connected, Down}, {Down, Connected}}
	if len(got) != len(want) {
		t.Fatalf("events %v, want %v", got, want)
	}
	for i, e := range want {
		if got[i] != e {
			t.Fatalf("event %d = %v, want %v", i, got[i], e)
		}
	}
	if s, since := tr.Current(); s != Connected || !since.Equal(st0.Add(5*time.Minute)) {
		t.Fatalf("final state %s at %v", s, since)
	}
}

func TestTrackerRejectsIllegalWithoutStateChange(t *testing.T) {
	tr := NewTracker(st0)
	called := 0
	tr.Subscribe(func(Status, Status, time.Time) { called++ })
	if err := tr.Transition(Degraded, st0); err == nil {
		t.Fatal("idle → degraded must be rejected")
	}
	if s, _ := tr.Current(); s != Idle || called != 0 {
		t.Fatalf("failed transition must leave state untouched, got %s called=%d", s, called)
	}
}

// TestObserverPolicy 策略面：idle 期的失败不降级（degraded 保留给连上过又出问题），
// 连败到阈值判 down；Ok 清零连败并回 connected；down 后失败不再迁移。
func TestObserverPolicy(t *testing.T) {
	at := st0
	tick := func() time.Time { at = at.Add(time.Second); return at }
	tr := NewTracker(st0)
	type event struct{ from, to Status }
	var got []event
	tr.Subscribe(func(from, to Status, _ time.Time) { got = append(got, event{from, to}) })
	obs := NewObserver(tr, tick)

	// 未建立过连接：单次/两次失败只累计不动迁，到阈值判 down。
	obs.Fail()
	obs.Fail()
	if s, _ := tr.Current(); s != Idle || len(got) != 0 {
		t.Fatalf("pre-connect failures must stay idle, got %s events=%v", s, got)
	}
	obs.Fail()
	if s, _ := tr.Current(); s != Down {
		t.Fatalf("pre-connect failure #%d should be down, got %s", FailThreshold, s)
	}
	// down 后失败不再迁移；一次成功恢复 connected 并清零连败。
	obs.Fail()
	obs.Ok()
	if s, _ := tr.Current(); s != Connected {
		t.Fatalf("ok after down must recover connected, got %s", s)
	}
	// 连败已清零：再失败一次只 degraded。
	obs.Fail()
	if s, _ := tr.Current(); s != Degraded {
		t.Fatalf("fail counter must reset on ok, got %s", s)
	}
	// degraded 上持续失败：到阈值 down。
	obs.Fail()
	obs.Fail()
	if s, _ := tr.Current(); s != Down {
		t.Fatalf("sustained failures must reach down, got %s", s)
	}
	want := []event{{Idle, Down}, {Down, Connected}, {Connected, Degraded}, {Degraded, Down}}
	if len(got) != len(want) {
		t.Fatalf("events %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("event %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestObserverDefaultsClock(t *testing.T) {
	tr := NewTracker(st0)
	NewObserver(tr, nil).Ok()
	if s, _ := tr.Current(); s != Connected {
		t.Fatalf("nil clock must default to time.Now, got %s", s)
	}
}

func TestTransitionIfDiscardsStaleDecisions(t *testing.T) {
	tr := NewTracker(st0)
	type event struct{ from, to Status }
	var got []event
	tr.Subscribe(func(from, to Status, _ time.Time) { got = append(got, event{from, to}) })

	// 期望状态已不在：过期决策作废，不动状态不通知。
	if err := tr.TransitionIf(Connected, Degraded, st0); err != nil {
		t.Fatalf("stale decision must be a silent no-op, got %v", err)
	}
	if s, _ := tr.Current(); s != Idle || len(got) != 0 {
		t.Fatalf("stale decision must not move state, got %s events=%v", s, got)
	}
	// 命中期望状态：与 Transition 一样迁移、前进 since 并通知。
	if err := tr.TransitionIf(Idle, Connected, st0.Add(time.Minute)); err != nil {
		t.Fatalf("expected-state transition should apply, got %v", err)
	}
	if s, since := tr.Current(); s != Connected || !since.Equal(st0.Add(time.Minute)) {
		t.Fatalf("expected-state decision must apply, got %s at %v", s, since)
	}
	// 已处目标态：幂等 nil 不通知。
	if err := tr.TransitionIf(Idle, Connected, st0.Add(2*time.Minute)); err != nil {
		t.Fatalf("already-at-target must be idempotent, got %v", err)
	}
	// 命中期望但迁移非法：报错、状态与通知都不动。
	if err := tr.TransitionIf(Connected, Idle, st0); err == nil {
		t.Fatal("illegal hop must error even when the expected from matches")
	}
	if s, _ := tr.Current(); s != Connected || len(got) != 1 {
		t.Fatalf("illegal hop must leave state and events untouched, got %s events=%v", s, got)
	}
	if got[0] != (event{Idle, Connected}) {
		t.Fatalf("events %v, want only {Idle→Connected}", got)
	}
}

// TestObserverConcurrentOps 真并发下混跑 Ok/Fail：数据竞争由 -race 裁决，
// 每条通知的迁移都必须合法；全部落下后一次 Ok 必回复 connected。
func TestObserverConcurrentOps(t *testing.T) {
	tr := NewTracker(st0)
	var mu sync.Mutex
	var hops [][2]Status
	tr.Subscribe(func(from, to Status, _ time.Time) {
		mu.Lock()
		hops = append(hops, [2]Status{from, to})
		mu.Unlock()
	})
	obs := NewObserver(tr, time.Now)
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				obs.Ok()
			} else {
				obs.Fail()
			}
		}(i)
	}
	wg.Wait()
	obs.Ok()
	if s, _ := tr.Current(); s != Connected {
		t.Fatalf("final ok must recover connected, got %s", s)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, h := range hops {
		if h[0] == h[1] || !TransitionOK(h[0], h[1]) {
			t.Fatalf("notified illegal hop %s → %s", h[0], h[1])
		}
	}
}
