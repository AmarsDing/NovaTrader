package data

import (
	"context"
	"testing"

	v1 "server/api/platform/v1"
	"server/app/backtest/internal/biz"
	"server/pkg/observe"
)

func researchObservation(t *testing.T) *v1.ConsumerCounters {
	t.Helper()
	for _, op := range observe.ForService("backtest").Snapshot().Operations {
		if op.Key == "research_execute" {
			return op.Total
		}
	}
	t.Fatal("missing executor observation")
	return nil
}
func TestActualUnconfiguredExecutorIsRejectedNotSuccessful(t *testing.T) {
	before := researchObservation(t)
	result := NewResearchExecutor(biz.ResearchSettings{}).Execute(context.Background(), nil, nil)
	if result.Status != "unavailable" {
		t.Fatalf("wrong result %+v", result)
	}
	after := researchObservation(t)
	if after.Rejected != before.Rejected+1 || after.Succeeded != before.Succeeded || after.Attempted != before.Attempted+1 {
		t.Fatalf("unavailable worker called success before=%v after=%v", before, after)
	}
}
