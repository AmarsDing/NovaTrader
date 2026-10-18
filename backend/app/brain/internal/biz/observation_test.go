package biz

import (
	"context"
	"testing"

	v1 "server/api/platform/v1"
	"server/pkg/llm"
	"server/pkg/observe"
)

func analysisCounters(t *testing.T) *v1.ConsumerCounters {
	t.Helper()
	for _, op := range observe.ForService("brain").Snapshot().Operations {
		if op.Key == "analyze" {
			if op.Inflight != 0 {
				t.Fatal("analysis still inflight")
			}
			return op.Total
		}
	}
	t.Fatal("analysis missing")
	return nil
}

func TestActualAnalysisObservationDistinguishesFallbackInvalidAndSuccessfulOutput(t *testing.T) {
	before := analysisCounters(t)
	degraded := newEnv(func(llm.Request) (string, error) { return "", llm.ErrUnavailable })
	r, err := degraded.uc.Analyze(context.Background(), AnalyzeInput{Symbol: "600519.SH", RuleScore: 60})
	if err != nil || r == nil || !r.Degraded {
		t.Fatalf("degraded=%+v err=%v", r, err)
	}
	discarded := newEnv(func(llm.Request) (string, error) { return `{"score":999}`, nil })
	r, err = discarded.uc.Analyze(context.Background(), AnalyzeInput{Symbol: "600519.SH", RuleScore: 60})
	if err != nil || r == nil || !r.Discarded {
		t.Fatalf("discarded=%+v err=%v", r, err)
	}
	valid := newEnv(happyReply(map[string]int{"technical": 60, "news": 60, "capital": 60, "external": 60}))
	r, err = valid.uc.Analyze(context.Background(), AnalyzeInput{Symbol: "600519.SH", RuleScore: 60})
	if err != nil || r == nil || r.Degraded || r.Discarded {
		t.Fatalf("valid=%+v err=%v", r, err)
	}
	if _, err = valid.uc.Analyze(context.Background(), AnalyzeInput{Symbol: "invalid"}); err == nil {
		t.Fatal("invalid request accepted")
	}
	after := analysisCounters(t)
	if after.Attempted != before.Attempted+4 || after.Skipped != before.Skipped+1 || after.Rejected != before.Rejected+2 || after.Succeeded != before.Succeeded+1 {
		t.Fatalf("before=%v after=%v", before, after)
	}
}
