package data

import (
	"context"
	"fmt"
	"time"

	"server/app/strategy/internal/biz"
	"server/ent"
	"server/ent/marketdata"
	"server/ent/marketsentiment"
	"server/ent/newssentiment"
	"server/ent/stockbasic"
	"server/pkg/market"
	"server/pkg/rules"
	"server/pkg/tradecal"

	"entgo.io/ent/dialect/sql"
	"github.com/go-kratos/kratos/v2/log"
)

const (
	historyDays  = 120 // 往前取多少个自然日的日线，约 80 个交易日
	keepBars     = 60
	snapMaxAge   = 2 * time.Minute
	stageMaxAge  = 7 * 24 * time.Hour
	badNewsLevel = 4    // 重要度达到即算突发利空
	badNewsScore = -0.5 // 情感不高于此值
	badNewsHours = 24
	symbolFilter = 1000 // 代码数超过这个就不加 IN 条件，直接按时间取全市场
)

// marketSource 用库里的日线、Redis 最新快照、情绪截面和情报拼出 rules.Snapshot。
// 只读 asOf 之前的数据；asOf 不是今天时不读 Redis，避免把现在的价格混进过去的快照。
type marketSource struct {
	d   *Data
	log *log.Helper
}

func NewMarketSource(d *Data, logger log.Logger) biz.MarketSource {
	return &marketSource{d: d, log: log.NewHelper(log.With(logger, "module", "strategy/market"))}
}

func dayStart(t time.Time) time.Time {
	d := t.In(tradecal.Shanghai())
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, tradecal.Shanghai())
}

// localDate 把 date 列读回的 UTC 零点换成同一日期的上海零点。
func localDate(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, tradecal.Shanghai())
}

func listed(asOf time.Time) func(*sql.Selector) {
	return stockbasic.Or(stockbasic.DelistDateIsNil(), stockbasic.DelistDateGT(asOf))
}

func (m *marketSource) Universe(ctx context.Context, asOf time.Time) ([]rules.Snapshot, error) {
	basics, err := m.d.Client.StockBasic.Query().Where(listed(asOf)).All(ctx)
	if err != nil {
		return nil, err
	}
	return m.build(ctx, basics, asOf)
}

func (m *marketSource) Snapshots(ctx context.Context, symbols []string, asOf time.Time) ([]rules.Snapshot, error) {
	if len(symbols) == 0 {
		return nil, nil
	}
	basics, err := m.d.Client.StockBasic.Query().Where(stockbasic.StockCodeIn(symbols...), listed(asOf)).All(ctx)
	if err != nil {
		return nil, err
	}
	return m.build(ctx, basics, asOf)
}

func (m *marketSource) build(ctx context.Context, basics []*ent.StockBasic, asOf time.Time) ([]rules.Snapshot, error) {
	if len(basics) == 0 {
		return nil, nil
	}
	codes := make([]string, len(basics))
	for i, b := range basics {
		codes[i] = b.StockCode
	}
	bars, err := m.dailyBars(ctx, codes, asOf)
	if err != nil {
		return nil, err
	}
	today := m.todayBars(ctx, codes, asOf)
	stage := m.stage(ctx, asOf)
	bad, err := m.badNews(ctx, codes, asOf)
	if err != nil {
		return nil, err
	}
	out := make([]rules.Snapshot, 0, len(basics))
	for _, b := range basics {
		s := rules.Snapshot{
			Symbol: b.StockCode, Name: b.StockName, ST: b.StFlag, Suspended: b.SuspendFlag,
			AsOf: asOf, Bars: bars[b.StockCode], Today: today[b.StockCode], Stage: stage,
			NegativeNews: bad[b.StockCode],
		}
		if b.ListDate != nil {
			s.ListDate = localDate(*b.ListDate)
		}
		out = append(out, s)
	}
	return out, nil
}

// dailyBars 取 asOf 当天之前的日线，按复权因子换成以最后一根为基准的前复权价。
func (m *marketSource) dailyBars(ctx context.Context, codes []string, asOf time.Time) (map[string][]rules.Bar, error) {
	day := dayStart(asOf)
	q := m.d.Client.MarketData.Query().Where(
		marketdata.Freq("1d"),
		marketdata.BarTimeGTE(day.AddDate(0, 0, -historyDays)),
		marketdata.BarTimeLT(day),
	)
	if len(codes) <= symbolFilter {
		q = q.Where(marketdata.SymbolIn(codes...))
	}
	rows, err := q.Order(marketdata.BySymbol(), marketdata.ByBarTime()).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("daily bars: %w", err)
	}
	raw := map[string][]*ent.MarketData{}
	for _, r := range rows {
		if r.Close == nil || r.Open == nil || r.High == nil || r.Low == nil {
			continue
		}
		raw[r.Symbol] = append(raw[r.Symbol], r)
	}
	out := make(map[string][]rules.Bar, len(raw))
	for sym, list := range raw {
		if len(list) > keepBars {
			list = list[len(list)-keepBars:]
		}
		base := list[len(list)-1].AdjFactor
		if base <= 0 {
			base = 1
		}
		bars := make([]rules.Bar, len(list))
		for i, r := range list {
			k := 1.0
			if r.AdjFactor > 0 {
				k = r.AdjFactor / base
			}
			b := rules.Bar{
				Time: r.BarTime, Open: *r.Open * k, High: *r.High * k, Low: *r.Low * k, Close: *r.Close * k,
			}
			if r.Volume != nil {
				b.Volume = *r.Volume
			}
			if r.Amount != nil {
				b.Amount = *r.Amount
			}
			bars[i] = b
		}
		out[sym] = bars
	}
	return out, nil
}

// todayBars 读 Redis 最新快照。只在 asOf 与快照同一天、快照不晚于 asOf 且未过期时使用。
func (m *marketSource) todayBars(ctx context.Context, codes []string, asOf time.Time) map[string]*rules.Bar {
	out := map[string]*rules.Bar{}
	if m.d.Redis == nil || !dayStart(asOf).Equal(dayStart(time.Now())) {
		return out
	}
	vals, err := m.d.Redis.HMGet(ctx, market.SnapshotKey, codes...).Result()
	if err != nil {
		m.log.Warnf("redis snapshots: %v", err)
		return out
	}
	for i, v := range vals {
		raw, ok := v.(string)
		if !ok {
			continue
		}
		s, err := market.ParseSnapshot([]byte(raw))
		if err != nil || s.Expired(asOf, snapMaxAge) || s.Time.After(asOf) || !dayStart(s.Time).Equal(dayStart(asOf)) {
			continue
		}
		if s.Last <= 0 || s.Open <= 0 {
			continue
		}
		out[codes[i]] = &rules.Bar{
			Time: s.Time, Open: s.Open, High: s.High, Low: s.Low, Close: s.Last,
			Volume: s.Volume, Amount: s.Amount,
		}
	}
	return out
}

// stage 取 asOf 之前最近一次未过期的情绪截面。没有时按 WARM。
func (m *marketSource) stage(ctx context.Context, asOf time.Time) rules.Stage {
	row, err := m.d.Client.MarketSentiment.Query().
		Where(marketsentiment.AsOfLTE(asOf), marketsentiment.AsOfGT(asOf.Add(-stageMaxAge)), marketsentiment.Stale(false)).
		Order(marketsentiment.ByAsOf(sql.OrderDesc())).
		First(ctx)
	if err != nil {
		if !ent.IsNotFound(err) {
			m.log.Warnf("market sentiment: %v", err)
		}
		return rules.StageWarm
	}
	return rules.Stage(row.Phase)
}

func (m *marketSource) badNews(ctx context.Context, codes []string, asOf time.Time) (map[string]bool, error) {
	q := m.d.Client.NewsSentiment.Query().Where(
		newssentiment.PublishTimeGTE(asOf.Add(-badNewsHours*time.Hour)),
		newssentiment.PublishTimeLTE(asOf),
		newssentiment.ImportanceGTE(badNewsLevel),
		newssentiment.SentimentScoreLTE(badNewsScore),
		newssentiment.StockCodeNotNil(),
	)
	if len(codes) <= symbolFilter {
		q = q.Where(newssentiment.StockCodeIn(codes...))
	}
	hit, err := q.Unique(true).Select(newssentiment.FieldStockCode).Strings(ctx)
	if err != nil {
		return nil, fmt.Errorf("bad news: %w", err)
	}
	out := make(map[string]bool, len(hit))
	for _, c := range hit {
		out[c] = true
	}
	return out, nil
}

// DailyCloses 按 from 前最后一根日线的复权因子折算，除权日落在区间内也不会算出假涨跌。
func (m *marketSource) DailyCloses(ctx context.Context, symbol string, from, to time.Time) (map[string]float64, error) {
	rows, err := m.d.Client.MarketData.Query().
		Where(marketdata.Symbol(symbol), marketdata.Freq("1d"),
			marketdata.BarTimeGTE(dayStart(from)), marketdata.BarTimeLT(dayStart(to).AddDate(0, 0, 1))).
		All(ctx)
	if err != nil {
		return nil, err
	}
	base := 0.0
	prev, err := m.d.Client.MarketData.Query().
		Where(marketdata.Symbol(symbol), marketdata.Freq("1d"), marketdata.BarTimeLT(dayStart(from))).
		Order(marketdata.ByBarTime(sql.OrderDesc())).
		First(ctx)
	switch {
	case err == nil:
		base = prev.AdjFactor
	case !ent.IsNotFound(err):
		return nil, err
	}
	out := make(map[string]float64, len(rows))
	for _, r := range rows {
		if r.Close == nil {
			continue
		}
		px := *r.Close
		if base > 0 && r.AdjFactor > 0 {
			px *= r.AdjFactor / base
		}
		out[dayStart(r.BarTime).Format("2006-01-02")] = px
	}
	return out, nil
}
