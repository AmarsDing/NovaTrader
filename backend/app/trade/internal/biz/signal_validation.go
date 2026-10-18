package biz

import (
	"context"
	"time"
)

// SignalOrderValidator locks a bound strategy signal inside the existing ledger
// transaction. It performs no HTTP/Redis calls, and never replaces FinalCheck.
type SignalOrderValidator interface {
	ValidateSignalOrder(context.Context, PlaceRequest, string, time.Time) error
}
