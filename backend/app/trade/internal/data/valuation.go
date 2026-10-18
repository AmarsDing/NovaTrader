package data

import (
	"context"
	"math"
	"server/ent"
	"server/ent/position"
	"server/ent/simbarcursor"
	"server/pkg/accountstate"
	"time"
)

func snapshotValuation(ctx context.Context, c *ent.Client, book string, s *accountstate.Snapshot) error {
	if book != "paper" {
		return nil
	}
	p := &accountstate.ValuationProof{Version: 1, Source: "market.validated_minute_bar", Complete: true, Marks: []accountstate.ValuationMark{}, Reasons: []string{}}
	if len(s.Holdings) == 0 {
		p.Source = "ledger_cash_only"
		s.Valuation = p
		return nil
	}
	for _, h := range s.Holdings {
		cursor, err := c.SimBarCursor.Query().Where(simbarcursor.Book(book), simbarcursor.Symbol(h.Symbol)).Only(ctx)
		if err != nil && !ent.IsNotFound(err) {
			return err
		}
		if ent.IsNotFound(err) || cursor.ValuationVersion != 1 || cursor.LastClose <= 0 || cursor.LastBarStart.Add(time.Minute).After(s.AsOf) {
			p.Complete = false
			p.Reasons = append(p.Reasons, h.Symbol+":validated_minute_mark_unknown")
			continue
		}
		pos, err := c.Position.Query().Where(position.Book(book), position.Symbol(h.Symbol)).Only(ctx)
		if err != nil && !ent.IsNotFound(err) {
			return err
		}
		if ent.IsNotFound(err) || pos.CurrentPrice == nil || pos.Quantity != h.Quantity || math.Abs(*pos.CurrentPrice-cursor.LastClose) > 0.00005 || math.Abs(h.MarketValue-cursor.LastClose*float64(h.Quantity)) > 0.02 {
			p.Complete = false
			p.Reasons = append(p.Reasons, h.Symbol+":ledger_mark_does_not_match_validated_bar")
			continue
		}
		p.Marks = append(p.Marks, accountstate.ValuationMark{Symbol: h.Symbol, Quantity: h.Quantity, Price: cursor.LastClose, BarStart: cursor.LastBarStart, BarEnd: cursor.LastBarStart.Add(time.Minute)})
	}
	s.Valuation = p
	return nil
}
