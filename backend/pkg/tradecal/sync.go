package tradecal

import (
	"context"
	"fmt"
	"time"

	"server/ent"
	"server/ent/tradecalendar"
)

// Sync 把内置日历写到 trade_calendar。已有的同一天会被覆盖。
func (c *Calendar) Sync(ctx context.Context, client *ent.Client, from, to time.Time) error {
	from = date(from)
	to = date(to)
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		open, err := c.Open(d)
		if err != nil {
			return err
		}
		row, err := client.TradeCalendar.Query().Where(tradecalendar.DayEQ(d)).Only(ctx)
		if ent.IsNotFound(err) {
			if err := client.TradeCalendar.Create().
				SetDay(d).
				SetIsOpen(open).
				SetNote(c.Note(d)).
				Exec(ctx); err != nil {
				return fmt.Errorf("tradecal: insert %s: %w", d.Format("2006-01-02"), err)
			}
			continue
		}
		if err != nil {
			return err
		}
		if err := client.TradeCalendar.UpdateOneID(row.ID).
			SetIsOpen(open).
			SetNote(c.Note(d)).
			Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}
