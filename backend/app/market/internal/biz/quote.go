package biz

import (
	"context"
	"time"

	"server/pkg/ashare"
	"server/pkg/market"
)

// StockBasicRow 是报价侧对 stock_basic 的只读视图。Found=false 表示库中无此证券。
type StockBasicRow struct {
	Name       string
	Market     string // SH / SZ / BJ
	TotalShare int64
	FloatShare int64
	CircMv     float64
	PeTtm      float64
	Pb         float64
	ST         bool
	Suspended  bool
	ListDate   time.Time
	Found      bool
}

// QuoteRow 单券快照的 biz 视图（对应 api market.v1.Quote，纪律见其 proto 注释）。
// 价格与口径分离：Source 记在库/内存出处，前端对零值一律显「未知」。
type QuoteRow struct {
	Symbol, Name, Market, Status string
	Last, Chg, PctChg            float64
	Open, High, Low, PreClose    float64
	Volume                       int64
	Amount                       float64
	TurnoverRate, Amplitude      float64 // %
	Bid, Ask                     [][2]float64
	PeTtm, Pb, CircMv, TotalMv   float64
	LimitUp, LimitDown           float64
	NoLimit                      bool
	Source                       string // snap / 1d / ""
	AsOf                         time.Time
	Stale                        bool
	Found                        bool
}

// Quote 拼单券快照：静态读 stock_basic；价格盘口取 Redis 实时快照（当日口径），
// 缺当日快照回退最近 1d 收盘行；静态缺失不阻断价格，价格缺失不阻断静态。
func (u *Usecase) Quote(ctx context.Context, symbol string) (QuoteRow, error) {
	now := time.Now()
	base, err := u.repo.StockBasic(ctx, symbol)
	if err != nil {
		return QuoteRow{}, err
	}
	q := QuoteRow{
		Symbol: symbol, Found: base.Found, Name: base.Name, Market: base.Market,
		PeTtm: base.PeTtm, Pb: base.Pb, CircMv: base.CircMv,
		Status: "TRADE",
	}
	if base.Suspended {
		q.Status = "SUSPEND"
	}
	if !base.Found {
		return q, nil
	}

	tradeDay := market.DateOf(now)
	snap := u.lookupSnapshot(ctx, symbol, tradeDay)
	switch {
	case snap != nil:
		q.Source = "snap"
		q.Last, q.Open, q.High, q.Low = snap.Last, snap.Open, snap.High, snap.Low
		q.PreClose, q.Volume, q.Amount = snap.PreClose, snap.Volume, snap.Amount
		q.Bid, q.Ask = levels5(snap.Bid), levels5(snap.Ask)
		q.AsOf = snap.FreshnessTime()
		if q.AsOf.IsZero() {
			q.AsOf = now
		}
		// 停牌长假价/过时报价只在交易时段催陈旧；收盘后按最近口径展示不算陈旧。
		_, inSession := market.Slot(now)
		q.Stale = snap.Stale || (snap.Expired(now, u.cfg.StaleAfter) && inSession)
	default:
		rows, err := u.repo.Bars(ctx, symbol, "1d", time.Time{}, now, 2)
		if err != nil {
			return QuoteRow{}, err
		}
		if len(rows) > 0 {
			b := rows[len(rows)-1]
			q.Source = "1d"
			q.Last, q.Open, q.High, q.Low = b.Close, b.Open, b.High, b.Low
			q.PreClose, q.Volume, q.Amount = b.PreClose, b.Volume, b.Amount
			q.AsOf = b.Time
		}
	}

	if q.PreClose > 0 {
		if q.Last > 0 {
			q.Chg = q.Last - q.PreClose
			q.PctChg = q.Chg / q.PreClose * 100
		}
		if q.High > 0 && q.Low > 0 {
			q.Amplitude = (q.High - q.Low) / q.PreClose * 100
		}
		if market.NoLimit(u.cal, base.ListDate, tradeDay) {
			q.NoLimit = true
		} else if ratio, rerr := ashare.RatioOn(symbol, base.ST, tradeDay); rerr == nil {
			q.LimitUp, q.LimitDown = ashare.Limit(q.PreClose, ratio)
		}
	}
	if base.FloatShare > 0 && q.Volume > 0 {
		q.TurnoverRate = float64(q.Volume) / float64(base.FloatShare) * 100
	}
	if q.Last > 0 && base.TotalShare > 0 {
		q.TotalMv = q.Last * float64(base.TotalShare)
	}
	// 流通市值列缺失时按定义补 last×float_share；两边都缺则保持 0 由前端显「未知」。
	if q.CircMv <= 0 && q.Last > 0 && base.FloatShare > 0 {
		q.CircMv = q.Last * float64(base.FloatShare)
	}
	return q, nil
}

// lookupSnapshot 在实时快照批次里找当日口径的一只。快照陈旧但日期是当日仍采用
// （陈旧由 Stale 表达）；跨日残留快照不如 1d 收盘行口径清楚，弃用。
// 快照源失败降级 1d，不让侧栏因为 Redis 抖动整屏空白。
func (u *Usecase) lookupSnapshot(ctx context.Context, symbol string, tradeDay time.Time) *market.Snapshot {
	all, err := u.snaps.All(ctx)
	if err != nil {
		u.log.Warnf("quote snapshot source failed: %v", err)
		return nil
	}
	for i := range all {
		if all[i].Symbol != symbol || all[i].Last <= 0 {
			continue
		}
		if market.DateOf(all[i].FreshnessTime()) != tradeDay {
			return nil
		}
		s := all[i]
		return &s
	}
	return nil
}

func levels5(book [][2]float64) [][2]float64 {
	if len(book) > 5 {
		return book[:5]
	}
	return book
}
