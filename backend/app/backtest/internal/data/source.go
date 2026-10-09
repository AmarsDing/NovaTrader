package data

import (
	"context"
	"fmt"
	"sort"
	"time"

	"server/ent"
	"server/ent/marketdata"
	"server/ent/marketsentiment"
	"server/ent/stockbasic"
	"server/ent/tradecalendar"
	"server/pkg/backtest"
	"server/pkg/tradecal"
)

// Source 从 market_data、trade_calendar、stock_name_history、stock_basic、market_sentiment 读回测数据。
type Source struct{ client *ent.Client }

func NewSource(client *ent.Client) *Source { return &Source{client: client} }

var _ backtest.Source = (*Source)(nil)

// TradingDays 优先用 trade_calendar；表里缺的日子用内置日历补。两者都没有的年份报错，不按周末推断。
func (s *Source) TradingDays(ctx context.Context, from, to time.Time) ([]time.Time, error) {
	from, to = backtest.Day(from), backtest.Day(to)
	rows, err := s.client.TradeCalendar.Query().
		Where(tradecalendar.DayGTE(from), tradecalendar.DayLTE(to)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	known := make(map[string]bool, len(rows))
	for _, r := range rows {
		known[backtest.Day(r.Day).Format("2006-01-02")] = r.IsOpen
	}
	var out []time.Time
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		open, ok := known[d.Format("2006-01-02")]
		if !ok {
			if open, err = tradecal.Default.Open(d); err != nil {
				return nil, fmt.Errorf("缺 %d 年交易日历：先同步 trade_calendar（%w）", d.Year(), err)
			}
		}
		if open {
			out = append(out, d)
		}
	}
	return out, nil
}

type barRow struct {
	Symbol   string    `json:"symbol"`
	BarTime  time.Time `json:"bar_time"`
	Open     *float64  `json:"open"`
	High     *float64  `json:"high"`
	Low      *float64  `json:"low"`
	Close    *float64  `json:"close"`
	Volume   *int64    `json:"volume"`
	Amount   *float64  `json:"amount"`
	Adj      float64   `json:"adj_factor"`
	PreClose *float64  `json:"pre_close"`
}

func (r barRow) ok() bool {
	return r.Open != nil && r.High != nil && r.Low != nil && r.Close != nil && *r.Open > 0 && *r.Close > 0
}

func val[T int64 | float64](p *T) T {
	if p == nil {
		return 0
	}
	return *p
}

var barFields = []string{
	marketdata.FieldSymbol, marketdata.FieldBarTime, marketdata.FieldOpen, marketdata.FieldHigh, marketdata.FieldLow,
	marketdata.FieldClose, marketdata.FieldVolume, marketdata.FieldAmount, marketdata.FieldAdjFactor, marketdata.FieldPreClose,
}

// DailyBars 按月分批读全市场日线，减少单次结果集。
func (s *Source) DailyBars(ctx context.Context, from, to time.Time) (map[string][]backtest.DayBar, error) {
	out := map[string][]backtest.DayBar{}
	end := backtest.Day(to).AddDate(0, 0, 1)
	for lo := backtest.Day(from); lo.Before(end); {
		hi := lo.AddDate(0, 1, 0)
		if hi.After(end) {
			hi = end
		}
		var rows []barRow
		if err := s.client.MarketData.Query().
			Where(marketdata.Freq(backtest.Freq1d), marketdata.BarTimeGTE(lo), marketdata.BarTimeLT(hi)).
			Order(marketdata.BySymbol(), marketdata.ByBarTime()).
			Select(barFields...).
			Scan(ctx, &rows); err != nil {
			return nil, err
		}
		for _, r := range rows {
			if !r.ok() {
				continue
			}
			out[r.Symbol] = append(out[r.Symbol], backtest.DayBar{
				Day: backtest.Day(r.BarTime), Open: *r.Open, High: *r.High, Low: *r.Low, Close: *r.Close,
				Volume: val(r.Volume), Amount: val(r.Amount), Adj: r.Adj, PreClose: val(r.PreClose),
			})
		}
		lo = hi
	}
	for sym, bars := range out {
		sort.SliceStable(bars, func(i, j int) bool { return bars[i].Day.Before(bars[j].Day) })
		out[sym] = bars
	}
	return out, nil
}

func (s *Source) MinuteBars(ctx context.Context, day time.Time, symbols []string) (map[string][]backtest.MinBar, error) {
	out := map[string][]backtest.MinBar{}
	lo := backtest.Day(day)
	hi := lo.AddDate(0, 0, 1)
	for i := 0; i < len(symbols); i += 500 {
		j := i + 500
		if j > len(symbols) {
			j = len(symbols)
		}
		var rows []barRow
		if err := s.client.MarketData.Query().
			Where(marketdata.Freq(backtest.Freq1m), marketdata.SymbolIn(symbols[i:j]...), marketdata.BarTimeGTE(lo), marketdata.BarTimeLT(hi)).
			Order(marketdata.BySymbol(), marketdata.ByBarTime()).
			Select(barFields...).
			Scan(ctx, &rows); err != nil {
			return nil, err
		}
		for _, r := range rows {
			if !r.ok() {
				continue
			}
			out[r.Symbol] = append(out[r.Symbol], backtest.MinBar{
				Time: r.BarTime.In(tradecal.Shanghai()), Open: *r.Open, High: *r.High, Low: *r.Low, Close: *r.Close,
				Volume: val(r.Volume), Amount: val(r.Amount),
			})
		}
	}
	return out, nil
}

func (s *Source) Names(ctx context.Context) (map[string][]backtest.NameSpan, error) {
	rows, err := s.client.StockNameHistory.Query().All(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string][]backtest.NameSpan{}
	for _, r := range rows {
		sp := backtest.NameSpan{Name: r.Name, Start: backtest.Day(r.StartDate)}
		if r.EndDate != nil {
			sp.End = backtest.Day(*r.EndDate)
		}
		out[r.Symbol] = append(out[r.Symbol], sp)
	}
	for sym, spans := range out {
		sort.Slice(spans, func(i, j int) bool { return spans[i].Start.Before(spans[j].Start) })
		out[sym] = spans
	}
	return out, nil
}

func (s *Source) ListDates(ctx context.Context) (map[string]time.Time, error) {
	rows, err := s.client.StockBasic.Query().Where(stockbasic.ListDateNotNil()).
		Select(stockbasic.FieldStockCode, stockbasic.FieldListDate).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]time.Time, len(rows))
	for _, r := range rows {
		out[r.StockCode] = backtest.Day(*r.ListDate)
	}
	return out, nil
}

func (s *Source) Phases(ctx context.Context, from, to time.Time) ([]backtest.PhasePoint, error) {
	rows, err := s.client.MarketSentiment.Query().
		Where(marketsentiment.AsOfGTE(from), marketsentiment.AsOfLTE(to), marketsentiment.Stale(false)).
		Order(marketsentiment.ByAsOf()).
		Select(marketsentiment.FieldAsOf, marketsentiment.FieldPhase).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]backtest.PhasePoint, len(rows))
	for i, r := range rows {
		out[i] = backtest.PhasePoint{AsOf: r.AsOf, Phase: r.Phase}
	}
	return out, nil
}
