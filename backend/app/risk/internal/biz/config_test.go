package biz

import (
	"context"
	"errors"
	"server/pkg/params"
	"server/pkg/risk"
	"testing"
)

type configRepoFixture struct {
	*fakeRepo
	snapshot params.Snapshot
	loadErr  error
	statuses []string
}

func (r *configRepoFixture) ConfigSnapshot(context.Context) (params.Snapshot, error) {
	return r.snapshot, r.loadErr
}
func (r *configRepoFixture) RecordConfig(_ context.Context, _ int, status string) error {
	r.statuses = append(r.statuses, status)
	return nil
}
func TestConfigLoadFailureNeverAcknowledgesApplied(t *testing.T) {
	rig := newRig(t, nil)
	repo := &configRepoFixture{fakeRepo: rig.repo, snapshot: params.Snapshot{Revision: 42, Values: map[string]string{"risk.max_single_pct": "0.03"}}}
	rig.e.repo = repo
	if err := rig.e.ReloadConfig(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rig.e.State().ConfigRevision != 42 || rig.e.params.MaxSinglePct != 0.03 || repo.statuses[0] != "applied" {
		t.Fatal("real load not installed")
	}
	repo.snapshot = params.Snapshot{Revision: 43, Values: map[string]string{"risk.max_single_pct": "garbage"}}
	if err := rig.e.ReloadConfig(context.Background()); err == nil {
		t.Fatal("bad config loaded")
	}
	if rig.e.ready.Load() || rig.e.State().ConfigRevision != 42 || repo.statuses[1] != "failed" {
		t.Fatal("bad config restored ready or revision")
	}
	repo.loadErr = errors.New("db down")
	if rig.e.ReloadConfig(context.Background()) == nil || rig.e.ready.Load() {
		t.Fatal("read failure accepted")
	}
	repo.loadErr = nil
	repo.snapshot = params.Snapshot{Revision: 44, Values: map[string]string{"risk.live_admitted": "false"}}
	rig.e.mode = risk.L2
	if err := rig.e.ReloadConfig(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rig.e.State().Mode != risk.L1 || !rig.e.ready.Load() {
		t.Fatal("admission removal not applied")
	}
}
