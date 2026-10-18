package biz

import (
	"context"
	"testing"

	"github.com/go-kratos/kratos/v2/log"
	"server/pkg/observe"
)

type failingExecutionRepo struct {
	Repo
	claimed  bool
	terminal string
}

func (r *failingExecutionRepo) ClaimTask(context.Context) (*Task, error) {
	if r.claimed {
		return nil, nil
	}
	r.claimed = true
	return &Task{ID: 42, Kind: KindSingle, Status: StatusRunning}, nil
}
func (r *failingExecutionRepo) FinishTask(_ context.Context, _ int, status, _, _ string) error {
	r.terminal = status
	return nil
}

func TestRunNextBusinessFailureIsNotNilReturnSuccessMetric(t *testing.T) {
	var beforeSuccess, beforeFailed, beforeSkipped uint64
	for _, op := range observe.ForService("backtest").Snapshot().Operations {
		if op.Key == "run" {
			beforeSuccess, beforeFailed = op.Total.Succeeded, op.Total.Failed
		}
		if op.Key == "task_claim" {
			beforeSkipped = op.Total.Skipped
		}
	}
	repo := &failingExecutionRepo{}
	uc := NewUsecase(repo, nil, nil, NewSettings(nil), log.DefaultLogger)
	ran, err := uc.RunNext(context.Background())
	if !ran || err != nil || repo.terminal != StatusFailed {
		t.Fatalf("expected durable handled business failure: ran=%v err=%v status=%s", ran, err, repo.terminal)
	}
	if ran, err := uc.RunNext(context.Background()); ran || err != nil {
		t.Fatalf("expected idle skip: %v %v", ran, err)
	}
	for _, op := range observe.ForService("backtest").Snapshot().Operations {
		if op.Key == "run" && (op.Total.Succeeded != beforeSuccess || op.Total.Failed != beforeFailed+1) {
			t.Fatalf("nil lifecycle return became success: %v", op)
		}
		if op.Key == "task_claim" && op.Total.Skipped != beforeSkipped+1 {
			t.Fatalf("idle poll counted task execution: %v", op)
		}
	}
}
