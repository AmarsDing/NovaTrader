package biz

import (
	"context"
	"testing"

	v1 "server/api/platform/v1"
	"server/pkg/observe"
)

func datahubOp(t *testing.T, key string) *v1.ConsumerOperation {
	t.Helper()
	for _, op := range observe.ForService("datahub").Snapshot().Operations {
		if op.Key == key {
			return op
		}
	}
	t.Fatalf("operation %s missing", key)
	return nil
}

// 未知域尝试并失败：计数证明采集入口已接观测，失败不得记成功。
func TestRunDomainObservationCountsFailedAttempt(t *testing.T) {
	before := datahubOp(t, "collect_request").Total
	if _, err := (&Collector{}).RunDomain(context.Background(), "not_a_domain", "2026-10-16", ""); err == nil {
		t.Fatal("unknown domain accepted")
	}
	after := datahubOp(t, "collect_request").Total
	if after.Attempted-before.Attempted != 1 || after.Failed-before.Failed != 1 || after.Succeeded != before.Succeeded {
		t.Fatalf("before=%+v after=%+v", before, after)
	}
}
