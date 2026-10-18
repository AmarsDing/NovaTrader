package data

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"server/app/trade/internal/biz"
	"server/ent"
	"server/ent/tradesignal"
)

func (r *Repo) ValidateSignalOrder(ctx context.Context, req biz.PlaceRequest, book string, now time.Time) error {
	if !r.InLedger(ctx) {
		return fmt.Errorf("%w: signal validation requires ledger transaction", biz.ErrInvalid)
	}
	id, err := strconv.Atoi(req.SignalID)
	if err != nil || id <= 0 {
		return fmt.Errorf("%w: bound signal ID must be positive numeric identity", biz.ErrInvalid)
	}
	s, err := r.clientFor(ctx).TradeSignal.Query().Where(tradesignal.ID(id)).ForShare().Only(ctx)
	if ent.IsNotFound(err) {
		return fmt.Errorf("%w: bound signal unavailable", biz.ErrInvalid)
	}
	if err != nil {
		return err
	}
	if s.Book != book || s.StockCode != req.Symbol || string(s.SignalType) != req.Side {
		return fmt.Errorf("%w: bound signal account/symbol/side mismatch", biz.ErrInvalid)
	}
	version := "0"
	if s.StrategyVersionID != nil {
		version = strconv.Itoa(*s.StrategyVersionID)
	}
	if req.StrategyVersion != version && !(version == "0" && req.StrategyVersion == "") {
		return fmt.Errorf("%w: bound signal strategy version changed", biz.ErrInvalid)
	}
	chase := strings.HasPrefix(req.BusinessKey, "chase:") && req.Source == "watch"
	if !chase {
		if s.Status != "pending" && s.Status != "approved" {
			return fmt.Errorf("%w: signal no longer executable", biz.ErrInvalid)
		}
		if s.ValidUntil == nil || !now.Before(*s.ValidUntil) {
			return fmt.Errorf("%w: signal validity elapsed or unknown", biz.ErrInvalid)
		}
	} else if s.Status != "pending" && s.Status != "approved" && s.Status != "executing" && s.Status != "done" {
		return fmt.Errorf("%w: chase cannot revive terminal signal", biz.ErrInvalid)
	}
	return nil
}

var _ biz.SignalOrderValidator = (*Repo)(nil)
