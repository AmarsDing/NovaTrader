package biz

import (
	"context"
	"time"
)

const (
	defaultSignalLimit    = 100
	defaultCandidateLimit = 200
	maxListLimit          = 1000
)

func clampLimit(n, def int) int {
	if n <= 0 {
		return def
	}
	if n > maxListLimit {
		return maxListLimit
	}
	return n
}

// ListSignals 只看本服务的账本。Day 为零时取今天。
func (uc *Usecase) ListSignals(ctx context.Context, f SignalFilter) ([]*Signal, error) {
	f.Book = uc.cfg.Book
	if f.Day.IsZero() {
		f.Day = tradeDay(time.Now())
	}
	f.Limit = clampLimit(f.Limit, defaultSignalLimit)
	return uc.signals.List(ctx, f)
}

func (uc *Usecase) GetSignal(ctx context.Context, id int) (*Signal, error) {
	return uc.signals.Get(ctx, id)
}

// ListCandidates 的 Day 为零时取今天。
func (uc *Usecase) ListCandidates(ctx context.Context, f CandidateFilter) ([]Candidate, error) {
	if f.Day.IsZero() {
		f.Day = tradeDay(time.Now())
	}
	f.Limit = clampLimit(f.Limit, defaultCandidateLimit)
	return uc.candidates.List(ctx, f)
}
