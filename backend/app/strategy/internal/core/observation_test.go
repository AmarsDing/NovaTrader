package core

import (
	"testing"

	v1 "server/api/platform/v1"
	"server/pkg/events"
	"server/pkg/observe"
)

func strategyOp(t *testing.T, key string) *v1.ConsumerOperation {
	t.Helper()
	for _, op := range observe.ForService("strategy").Snapshot().Operations {
		if op.Key == key {
			return op
		}
	}
	t.Fatalf("operation %s missing", key)
	return nil
}

func TestKillSwitchObservationCountsOutcomes(t *testing.T) {
	r := newTestRunner(t)
	before := strategyOp(t, "kill_switch").Total
	r.onKill(message(t, events.SubjectKillSwitch, map[string]any{"active": true, "reason": "回撤"}))
	r.onKill([]byte("garbage"))
	r.onKill(message(t, events.SubjectKillSwitch, map[string]any{"active": false}))
	after := strategyOp(t, "kill_switch").Total
	if after.Received-before.Received != 3 || after.Attempted-before.Attempted != 3 || after.Succeeded-before.Succeeded != 2 || after.Rejected-before.Rejected != 1 {
		t.Fatalf("before=%+v after=%+v", before, after)
	}
	if r.uc.KillSwitch() {
		t.Fatal("last valid message deactivates the kill switch")
	}
}

func TestMarketAlertObservationSeparatesDupeAndBadPayload(t *testing.T) {
	r := newTestRunner(t)
	before := strategyOp(t, "market_alert_notice").Total
	first := message(t, events.SubjectMarketAlert, map[string]any{"symbol": "600519.SH"})
	r.onAlert(first) // 新证券：排队。
	r.onAlert(first) // 同一事件ID：跳过。
	r.onAlert(message(t, events.SubjectMarketAlert, map[string]any{"symbol": "600519.SH"}))
	r.onAlert([]byte("garbage"))
	after := strategyOp(t, "market_alert_notice").Total
	if after.Received-before.Received != 4 || after.Attempted-before.Attempted != 4 || after.Queued-before.Queued != 1 || after.Coalesced-before.Coalesced != 1 || after.Skipped-before.Skipped != 1 || after.Rejected-before.Rejected != 1 || after.Succeeded-before.Succeeded != 2 {
		t.Fatalf("before=%+v after=%+v", before, after)
	}
}
