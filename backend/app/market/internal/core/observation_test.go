package core

import (
	"testing"

	platform "server/api/platform/v1"
	"server/pkg/observe"
)

func marketOp(key string) *platform.ConsumerOperation {
	for _, op := range observe.ForService("market").Snapshot().Operations {
		if op.Key == key {
			return op
		}
	}
	return nil
}

func TestSnapshotWakeCoalescesWithoutReportingRefreshSuccess(t *testing.T) {
	w := &Worker{kick: make(chan struct{}, 1)}
	before, refresh := marketOp("snapshot_notice"), marketOp("refresh")
	w.onSnapshot()
	w.onSnapshot()
	after := marketOp("snapshot_notice")
	if len(w.kick) != 1 || after.Total.Received-before.Total.Received != 2 || after.Total.Queued-before.Total.Queued != 1 || after.Total.Coalesced-before.Total.Coalesced != 1 || after.Total.Succeeded != before.Total.Succeeded || marketOp("refresh").Total.Attempted != refresh.Total.Attempted {
		t.Fatalf("wake confused with completion: %v", after)
	}
}
