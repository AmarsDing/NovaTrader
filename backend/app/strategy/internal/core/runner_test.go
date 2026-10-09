package core

import (
	"testing"
	"time"

	"server/app/strategy/internal/biz"
	"server/app/strategy/internal/data"
	"server/pkg/events"

	"github.com/go-kratos/kratos/v2/log"
)

func newTestRunner(t *testing.T) *Runner {
	t.Helper()
	uc := biz.NewUsecase(biz.NewSettings(nil), nil, nil, nil, nil, nil, nil, nil, log.DefaultLogger)
	return NewRunner(uc, &data.Data{}, nil, log.DefaultLogger)
}

func message(t *testing.T, subject string, payload any) []byte {
	t.Helper()
	env, err := events.New("test", subject, "", payload)
	if err != nil {
		t.Fatal(err)
	}
	b, err := env.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestKillSwitchPayload(t *testing.T) {
	r := newTestRunner(t)
	r.onKill(message(t, events.SubjectKillSwitch, map[string]any{"active": true, "reason": "回撤"}))
	if !r.uc.KillSwitch() {
		t.Fatal("active=true")
	}
	r.onKill(message(t, events.SubjectKillSwitch, map[string]any{"active": false}))
	if r.uc.KillSwitch() {
		t.Fatal("active=false")
	}
	r.onKill(message(t, events.SubjectKillSwitch, map[string]any{"reason": "缺字段"}))
	if !r.uc.KillSwitch() {
		t.Fatal("missing active should mean active")
	}
	r.onKill([]byte("garbage"))
	if !r.uc.KillSwitch() {
		t.Fatal("bad message must not reset state")
	}
}

func TestAlertDebounce(t *testing.T) {
	r := newTestRunner(t)
	now := time.Date(2026, 10, 9, 10, 0, 0, 0, time.Local)
	r.now = func() time.Time { return now }
	body := message(t, events.SubjectMarketAlert, map[string]any{"symbol": "600000.SH", "kind": "limit_up"})
	r.onAlert(body)
	r.onAlert(body) // 同一 event_id 去重
	r.onAlert(message(t, events.SubjectMarketAlert, map[string]any{"symbol": "000001.SZ", "kind": "surge"}))
	r.onAlert(message(t, events.SubjectMarketAlert, map[string]any{"kind": "surge"}))

	if got := r.takeAlerts(now.Add(time.Second), false); got != nil {
		t.Fatalf("within debounce %v", got)
	}
	got := r.takeAlerts(now.Add(alertDebounce), false)
	if len(got) != 2 {
		t.Fatalf("after debounce %v", got)
	}
	if got := r.takeAlerts(now.Add(time.Hour), true); got != nil {
		t.Fatalf("drained %v", got)
	}
}
