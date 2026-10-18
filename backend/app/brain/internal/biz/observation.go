package biz

import (
	"errors"
	"server/pkg/observe"
)

// Persisted rule fallback and rejected model output remain visible outcomes;
// neither is counted as a successful model analysis or a trading prediction.
func finishObservation(finish func(observe.Outcome), err error, hasResult, degraded, discarded bool) {
	switch {
	case errors.Is(err, ErrBadRequest), errors.Is(err, ErrNotFound):
		finish(observe.Rejected)
	case err != nil:
		observe.FinishError(finish, err)
	case !hasResult:
		finish(observe.Failed)
	case discarded:
		finish(observe.Rejected)
	case degraded:
		finish(observe.Skipped)
	default:
		finish(observe.Succeeded)
	}
}
