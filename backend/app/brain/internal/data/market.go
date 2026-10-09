package data

import (
	"context"
	"strconv"
	"time"

	"server/app/brain/internal/biz"
	"server/ent"
	"server/ent/intellink"
	"server/ent/marketdata"
	"server/ent/marketsentiment"
	"server/ent/newssentiment"
	"server/ent/position"
	"server/ent/stockbasic"
	"server/ent/stockfactor"

	entsql "entgo.io/ent/dialect/sql"
)

type marketRepo struct {
	db *ent.Client
}

func NewMarketRepo(db *ent.Client) biz.MarketRepo {
	return &marketRepo{db: db}
}

func (r *marketRepo) Stock(ctx context.Context, symbol string) (*biz.StockInfo, error) {
	row, err := r.db.StockBasic.Query().Where(stockbasic.StockCodeEQ(symbol)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	info := &biz.StockInfo{Symbol: row.StockCode, Name: row.StockName, CircMV: row.CircMv, PE: row.PeTtm}
	if row.Industry != nil {
		info.Industry = *row.Industry
	}
	if row.Concept != nil {
		info.Concept = *row.Concept
	}
	return info, nil
}

func (r *marketRepo) DailyBars(ctx context.Context, symbol string, asOf time.Time, n int) ([]biz.Bar, error) {
	rows, err := r.db.MarketData.Query().
		Where(
			marketdata.SymbolEQ(symbol),
			marketdata.FreqEQ("1d"),
			marketdata.BarTimeLTE(asOf),
			marketdata.CloseNotNil(),
		).
		Order(marketdata.ByBarTime(entsql.OrderDesc())).
		Limit(n).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]biz.Bar, len(rows))
	for i, row := range rows {
		out[len(rows)-1-i] = biz.Bar{
			Time:      row.BarTime,
			Open:      deref(row.Open),
			High:      deref(row.High),
			Low:       deref(row.Low),
			Close:     deref(row.Close),
			Volume:    derefInt(row.Volume),
			Amount:    deref(row.Amount),
			AdjFactor: row.AdjFactor,
		}
	}
	return out, nil
}

func (r *marketRepo) StockNews(ctx context.Context, symbol string, from, to time.Time, limit int) ([]biz.Intel, error) {
	links, err := r.db.IntelLink.Query().
		Where(intellink.TargetTypeEQ("stock"), intellink.TargetEQ(symbol)).
		Select(intellink.FieldNewsID).
		All(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]int, 0, len(links))
	for _, l := range links {
		ids = append(ids, l.NewsID)
	}
	match := newssentiment.StockCodeEQ(symbol)
	if len(ids) > 0 {
		match = newssentiment.Or(match, newssentiment.IDIn(ids...))
	}
	rows, err := r.db.NewsSentiment.Query().
		Where(
			newssentiment.StatusEQ("scored"),
			match,
			newssentiment.PublishTimeGTE(from),
			newssentiment.PublishTimeLTE(to),
		).
		Order(newssentiment.ByImportance(entsql.OrderDesc()), newssentiment.ByPublishTime(entsql.OrderDesc())).
		Limit(limit).
		All(ctx)
	if err != nil {
		return nil, err
	}
	return toIntel(rows), nil
}

func (r *marketRepo) MarketNews(ctx context.Context, from, to time.Time, minImportance, limit int) ([]biz.Intel, error) {
	rows, err := r.db.NewsSentiment.Query().
		Where(
			newssentiment.StatusEQ("scored"),
			newssentiment.ImportanceGTE(minImportance),
			newssentiment.PublishTimeGTE(from),
			newssentiment.PublishTimeLTE(to),
		).
		Order(newssentiment.ByImportance(entsql.OrderDesc()), newssentiment.ByPublishTime(entsql.OrderDesc())).
		Limit(limit).
		All(ctx)
	if err != nil {
		return nil, err
	}
	return toIntel(rows), nil
}

func (r *marketRepo) Holdings(ctx context.Context) ([]biz.Holding, error) {
	rows, err := r.db.Position.Query().
		Where(position.QuantityGT(0)).
		Order(position.ByBook(), position.BySymbol()).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]biz.Holding, 0, len(rows))
	for _, row := range rows {
		out = append(out, biz.Holding{
			Book: row.Book, Symbol: row.Symbol, Quantity: row.Quantity, Available: row.Available,
			AvgCost: row.AvgCost, Price: row.CurrentPrice, PnL: row.Pnl,
		})
	}
	return out, nil
}

// kindOrder 决定同名因子以哪个截面为准：收盘覆盖盘中，盘中覆盖竞价，资金截面只补自己的键。
var kindOrder = []string{"capital", "auction", "intraday", "close"}

func (r *marketRepo) Factors(ctx context.Context, symbol string, asOf time.Time) (map[string]float64, error) {
	rows, err := r.db.StockFactor.Query().
		Where(stockfactor.SymbolEQ(symbol), stockfactor.AsOfLTE(asOf), stockfactor.StaleEQ(false)).
		Order(stockfactor.ByAsOf(entsql.OrderDesc())).
		Limit(40).
		All(ctx)
	if err != nil {
		return nil, err
	}
	latest := map[string]map[string]float64{}
	for _, row := range rows {
		if _, ok := latest[row.Kind]; ok {
			continue
		}
		latest[row.Kind] = row.Factors
	}
	if len(latest) == 0 {
		return nil, nil
	}
	out := map[string]float64{}
	for _, kind := range kindOrder {
		for k, v := range latest[kind] {
			out[k] = v
		}
	}
	return out, nil
}

func (r *marketRepo) Phase(ctx context.Context, asOf time.Time) (string, error) {
	row, err := r.db.MarketSentiment.Query().
		Where(marketsentiment.AsOfLTE(asOf), marketsentiment.StaleEQ(false)).
		Order(marketsentiment.ByAsOf(entsql.OrderDesc())).
		First(ctx)
	if ent.IsNotFound(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return row.Phase, nil
}

func toIntel(rows []*ent.NewsSentiment) []biz.Intel {
	out := make([]biz.Intel, 0, len(rows))
	for _, row := range rows {
		n := biz.Intel{
			ID:         "N:" + strconv.Itoa(row.ID),
			Title:      derefStr(row.NewsTitle),
			Body:       derefStr(row.Content),
			Source:     derefStr(row.Source),
			Sentiment:  deref(row.SentimentScore),
			Importance: row.Importance,
			Symbol:     derefStr(row.StockCode),
		}
		if row.PublishTime != nil {
			n.Time = *row.PublishTime
		}
		out = append(out, n)
	}
	return out
}

func deref(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

func derefInt(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
