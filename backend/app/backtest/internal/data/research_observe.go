package data

import (
	"context"
	"server/app/backtest/internal/biz"
	"server/pkg/observe"
)

func researchExecutionOutcome(ctx context.Context, result biz.ResearchResult) observe.Outcome {
	if ctx.Err() != nil {
		return observe.Cancelled
	}
	switch result.Status {
	case "succeeded":
		return observe.Succeeded
	case "unavailable", "rejected":
		return observe.Rejected
	case "cancelled", "timed_out":
		return observe.Cancelled
	default:
		return observe.Failed
	}
}
