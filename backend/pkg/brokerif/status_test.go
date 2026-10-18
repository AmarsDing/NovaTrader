package brokerif

import (
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
