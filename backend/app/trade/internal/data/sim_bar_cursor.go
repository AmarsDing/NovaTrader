package data

import (
	"context"
	"math"
	"time"

	"server/app/trade/internal/biz"
	"server/ent"
	"server/ent/simbarcursor"
)

func (r *Repo) LastConsumedSimBar(ctx context.Context, book, symbol string) (time.Time, error) {
	if !r.InLedger(ctx) || book != biz.BookPaper {
		return time.Time{}, biz.ErrInvalid
	}
	row, err := r.clientFor(ctx).SimBarCursor.Query().Where(simbarcursor.Book(book), simbarcursor.Symbol(symbol)).Only(ctx)
	if ent.IsNotFound(err) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	return row.LastBarStart, nil
}

func (r *Repo) AdvanceSimBar(ctx context.Context, book, symbol string, at time.Time, close float64) error {
	if !r.InLedger(ctx) || book != biz.BookPaper || at.IsZero() || close <= 0 || math.IsNaN(close) || math.IsInf(close, 0) {
		return biz.ErrInvalid
	}
	c := r.clientFor(ctx)
	row, err := c.SimBarCursor.Query().Where(simbarcursor.Book(book), simbarcursor.Symbol(symbol)).Only(ctx)
	if ent.IsNotFound(err) {
		_, err = c.SimBarCursor.Create().SetBook(book).SetSymbol(symbol).SetLastBarStart(at).SetLastClose(close).SetValuationVersion(1).Save(ctx)
		return err
	}
	if err != nil {
		return err
	}
	n, err := c.SimBarCursor.Update().Where(simbarcursor.ID(row.ID), simbarcursor.LastBarStartLT(at)).SetLastBarStart(at).SetLastClose(close).SetValuationVersion(1).Save(ctx)
	if err == nil && n != 1 {
		return biz.ErrConflict
	}
	return err
}
