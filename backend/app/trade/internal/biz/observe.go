package biz

import (
	"context"
	"errors"

	"server/pkg/decision"
	"server/pkg/observe"
)

func executionOutcome(out Order, err error) observe.Outcome {
	switch {
	case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		return observe.Cancelled
	case errors.Is(err, decision.ErrNotTriggered) || errors.Is(err, decision.ErrUnavailable):
		return observe.Skipped
	case errors.Is(err, ErrInvalid) || errors.Is(err, ErrConflict) || errors.Is(err, decision.ErrVersionChanged) || errors.Is(err, decision.ErrLedgerChanged) || errors.Is(err, decision.ErrExpired):
		return observe.Rejected
	case err != nil:
		return observe.Failed
	case out.Status == StatusRejected:
		return observe.Rejected
	case out.ClientOrderID == "":
		return observe.Skipped
	default:
		return observe.Succeeded
	}
}
