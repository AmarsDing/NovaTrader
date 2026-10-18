package biz

import (
	"context"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/log"
)

type snapshotIssueRepo struct {
	Repo
	issues []Issue
}

func (r *snapshotIssueRepo) SaveIssues(_ context.Context, issues []Issue) error {
	r.issues = append(r.issues, issues...)
	return nil
}

type snapshotMemoryStore struct {
	SnapshotStore
	items  []Snapshot
	meta   SnapshotMeta
	writes int
}

func (s *snapshotMemoryStore) WriteSnapshots(_ context.Context, items []Snapshot, meta SnapshotMeta) error {
	s.items = append([]Snapshot(nil), items...)
	s.meta = meta
	s.writes++
	return nil
}

func (s *snapshotMemoryStore) Meta(context.Context) (SnapshotMeta, error) { return s.meta, nil }
func (s *snapshotMemoryStore) MarkStale(_ context.Context, meta SnapshotMeta) error {
	s.meta = meta
	for i := range s.items {
		s.items[i].Stale = true
	}
	return nil
}

func TestCollectSnapshotPreservesSourceAgeAndStale(t *testing.T) {
	now := time.Date(2026, 10, 14, 10, 0, 0, 0, shanghai())
	old := now.Add(-time.Minute)
	source := &fakeSnap{name: SourceTdxLocal, items: []Snapshot{
		{Symbol: "600519.SH", Time: now, Last: 100},
		{Symbol: "000001.SZ", Time: old, Last: 10, Volume: 0},
		{Symbol: "000002.SZ", Time: now, Last: 10, Stale: true},
	}}
	p := testPipe(t, []Source{source}, SourceTdxLocal)
	repo, store := &snapshotIssueRepo{}, &snapshotMemoryStore{}
	c := NewCollector(p, repo, store, nil, nil, nil, Options{MinSnapshotCount: 1}, log.DefaultLogger)
	c.now = func() time.Time { return now }
	meta, err := c.CollectSnapshot(context.Background())
	if err != nil || meta.Stale || meta.Count != 3 || store.writes != 1 {
		t.Fatalf("meta=%+v writes=%d err=%v", meta, store.writes, err)
	}
	if store.items[0].Stale || !store.items[1].Stale || !store.items[2].Stale || !store.items[1].Time.Equal(old) || !store.items[1].AsOf.Equal(now) {
		t.Fatalf("source freshness overwritten: %+v", store.items)
	}
	if len(repo.issues) != 2 || repo.issues[0].Source != SourceTdxLocal {
		t.Fatalf("issues=%+v", repo.issues)
	}
}

func TestCollectSnapshotRecordsStaleSourceBeforeFailover(t *testing.T) {
	now := time.Date(2026, 10, 14, 10, 0, 0, 0, shanghai())
	stale := &fakeSnap{name: SourceTdxLocal, items: []Snapshot{{Symbol: "000001.SZ", Time: now.Add(-time.Minute), Last: 10}}}
	fresh := &fakeSnap{name: SourceTencent, items: []Snapshot{{Symbol: "000001.SZ", Time: now, Last: 10}}}
	p := testPipe(t, []Source{stale, fresh}, SourceTdxLocal, SourceTencent)
	repo, store := &snapshotIssueRepo{}, &snapshotMemoryStore{}
	c := NewCollector(p, repo, store, nil, nil, nil, Options{MinSnapshotCount: 1}, log.DefaultLogger)
	c.now = func() time.Time { return now }
	meta, err := c.CollectSnapshot(context.Background())
	if err != nil || meta.Source != SourceTencent || store.items[0].Stale {
		t.Fatalf("meta=%+v items=%+v err=%v", meta, store.items, err)
	}
	if len(repo.issues) != 2 || repo.issues[0].Source != SourceTdxLocal || repo.issues[1].Check != CheckStale || repo.issues[1].Severity != "error" {
		t.Fatalf("failed source quality lost: %+v", repo.issues)
	}
}

func TestCollectSnapshotAllStaleMarksPreviousBatch(t *testing.T) {
	now := time.Date(2026, 10, 14, 10, 0, 0, 0, shanghai())
	source := &fakeSnap{name: SourceTdxLocal, items: []Snapshot{{Symbol: "000001.SZ", Time: now.Add(-time.Minute), Last: 10}}}
	p := testPipe(t, []Source{source}, SourceTdxLocal)
	repo := &snapshotIssueRepo{}
	store := &snapshotMemoryStore{items: []Snapshot{{Symbol: "000001.SZ", Time: now.Add(-time.Minute), Last: 10}}}
	c := NewCollector(p, repo, store, nil, nil, nil, Options{MinSnapshotCount: 1}, log.DefaultLogger)
	c.now = func() time.Time { return now }
	meta, err := c.CollectSnapshot(context.Background())
	if err == nil || !meta.Stale || store.writes != 0 || !store.items[0].Stale || len(repo.issues) != 2 {
		t.Fatalf("meta=%+v store=%+v issues=%+v err=%v", meta, store, repo.issues, err)
	}
}
