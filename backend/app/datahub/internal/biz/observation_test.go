package biz

import (
	"context"
	"errors"
	"testing"
	"time"

	v1 "server/api/platform/v1"
	"server/pkg/events"
	"server/pkg/observe"

	"github.com/go-kratos/kratos/v2/log"
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

type failedSnapshotWrite struct{ *snapshotMemoryStore }

func (*failedSnapshotWrite) WriteSnapshots(context.Context, []Snapshot, SnapshotMeta) error {
	return errors.New("isolated Redis write failure")
}

type failedSnapshotNotice struct{}

func (failedSnapshotNotice) Publish(context.Context, events.Envelope) error {
	return errors.New("isolated NATS publish failure")
}
func snapshotCounters(t *testing.T) *v1.ConsumerCounters {
	t.Helper()
	for _, op := range observe.ForService("datahub").Snapshot().Operations {
		if op.Key == "snapshot_collect" {
			if op.Inflight != 0 {
				t.Fatal("snapshot still inflight")
			}
			if op.CompletionScope != "validated_snapshot_written_to_redis_not_all_symbols_fresh_or_nats_publish_confirmed" {
				t.Fatal(op.CompletionScope)
			}
			return op.Total
		}
	}
	t.Fatal("snapshot observation missing")
	return nil
}
func TestSnapshotObservationUsesActualWriteResultAndDeclaresPublishBoundary(t *testing.T) {
	before := snapshotCounters(t)
	now := time.Date(2026, 10, 14, 10, 0, 0, 0, shanghai())
	for _, failWrite := range []bool{false, true} {
		source := &fakeSnap{name: SourceTdxLocal, items: []Snapshot{{Symbol: "600519.SH", Time: now, Last: 100}}}
		p := testPipe(t, []Source{source}, SourceTdxLocal)
		memory := &snapshotMemoryStore{}
		var store SnapshotStore = memory
		if failWrite {
			store = &failedSnapshotWrite{memory}
		}
		c := NewCollector(p, &snapshotIssueRepo{}, store, failedSnapshotNotice{}, nil, nil, Options{MinSnapshotCount: 1}, log.DefaultLogger)
		c.now = func() time.Time { return now }
		_, err := c.CollectSnapshot(context.Background())
		if failWrite && err == nil {
			t.Fatal("failed write counted as successful collection")
		}
		if !failWrite && (err != nil || memory.writes != 1) {
			t.Fatalf("persisted snapshot: %v", err)
		}
	}
	after := snapshotCounters(t)
	if after.Attempted != before.Attempted+2 || after.Succeeded != before.Succeeded+1 || after.Failed != before.Failed+1 {
		t.Fatalf("before=%v after=%v", before, after)
	}
}
