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
	High52, Low52                *float64 // 近 250 交易日前复权极值（B-2）；窗口无效缺省
	Source                       string   // snap / 1d / ""
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

	// 52 周极值（B-2）：与实时价无关的历史窗口，先算好再拼盘中口径。
	q.High52, q.Low52 = u.week52Extremes(ctx, symbol, now)

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

// lookupSnapshot 拉取实时快照批次并找当日口径的一只。快照源失败降级 1d，
// 不让侧栏因为 Redis 抖动整屏空白。
func (u *Usecase) lookupSnapshot(ctx context.Context, symbol string, tradeDay time.Time) *market.Snapshot {
	all, err := u.snaps.All(ctx)
	if err != nil {
		u.log.Warnf("quote snapshot source failed: %v", err)
		return nil
	}
	return findSnapshot(all, symbol, tradeDay)
}

// findSnapshot 在一批快照里找 symbol 的当日口径快照。快照陈旧但日期是当日仍采用
// （陈旧由 Stale 表达）；跨日残留快照不如 1d 收盘行口径清楚，弃用。
func findSnapshot(all []market.Snapshot, symbol string, tradeDay time.Time) *market.Snapshot {
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

const (
	week52Window   = 250 // 约一年的交易日数（B-2 “52 周”）
	week52MinValid = 20  // 窗口有效行不足则整体缺省
)

// week52Extremes 近 250 交易日窗口的前复权最高/最低价（B-2）。复权基准取
// 窗口内最后一根有效因子；Adj<=0 的缺因子行跳过；有效行不足 week52MinValid
// 或取数失败整体缺省（nil 透出，proto optional 不落 0）。
func (u *Usecase) week52Extremes(ctx context.Context, symbol string, now time.Time) (hi, lo *float64) {
	rows, err := u.repo.Bars(ctx, symbol, "1d", now.AddDate(0, 0, -week52Window*2), now, week52Window)
	if err != nil {
		u.log.Warnf("week52 bars read failed symbol=%s: %v", symbol, err)
		return nil, nil
	}
	base := 0.0
	for i := len(rows) - 1; i >= 0; i-- {
		if rows[i].Adj > 0 {
			base = rows[i].Adj
			break
		}
	}
	if base <= 0 {
		return nil, nil
	}
	maxH, minL, valid := 0.0, 0.0, 0
	for _, r := range rows {
		if r.Adj <= 0 {
			continue
		}
		b := market.AdjustBar(r.Bar, r.Adj, base, market.AdjustForward)
		if b.High <= 0 || b.Low <= 0 {
			continue
		}
		valid++
		if maxH <= 0 || b.High > maxH {
			maxH = b.High
		}
		if minL <= 0 || b.Low < minL {
			minL = b.Low
		}
	}
	if valid < week52MinValid {
		return nil, nil
	}
	h, l := maxH, minL
	return &h, &l
}

/* ---------- 三指数快照（BE-10） ---------- */

// indexUniverse 是侧栏固定展示的三只 A 股指数，顺序即回包顺序（与 brain/review.go 约定一致）。
var indexUniverse = []struct {
	Code string
	Name string
}{
	{Code: "000001.SH", Name: "上证指数"},
	{Code: "399001.SZ", Name: "深证成指"},
	{Code: "399006.SZ", Name: "创业板指"},
}

// IndexQuoteRow 是一只指数的快照 biz 视图（对应 api market.v1.IndexQuote）。
// 口径与 QuoteRow 同源：Source 记 snap/1d 出处，Found=false 表示两边都缺数。
// High/Low/PreClose 仅内部计算用，不随接口透出。
type IndexQuoteRow struct {
	Code, Name                string
	Last, Chg, PctChg         float64
	Amplitude                 float64 // %
	High, Low, PreClose       float64
	AsOf                      time.Time
	Source                    string // snap / 1d / ""
	Stale, Found              bool
}

// IndexSnapshot 拼三只固定指数的快照：共享一次 snaps.All 拉取（避免三次 Redis
// 往返），每只先找当日快照、缺则回退最近 1d 收盘行，两边都缺 Found=false。
// stale 与回退纪律与单券 Quote 一致；快照源失败降级 1d，1d 读库错误整批上抛。
func (u *Usecase) IndexSnapshot(ctx context.Context) ([]IndexQuoteRow, error) {
	now := time.Now()
	tradeDay := market.DateOf(now)
	_, inSession := market.Slot(now)

	var all []market.Snapshot
	if rows, err := u.snaps.All(ctx); err != nil {
		u.log.Warnf("index snapshot source failed: %v", err)
	} else {
		all = rows
	}

	out := make([]IndexQuoteRow, 0, len(indexUniverse))
	for _, idx := range indexUniverse {
		row := IndexQuoteRow{Code: idx.Code, Name: idx.Name}
		if snap := findSnapshot(all, idx.Code, tradeDay); snap != nil {
			row.Source = "snap"
			row.Last, row.High, row.Low, row.PreClose = snap.Last, snap.High, snap.Low, snap.PreClose
			row.AsOf = snap.FreshnessTime()
			if row.AsOf.IsZero() {
				row.AsOf = now
			}
			row.Stale = snap.Stale || (snap.Expired(now, u.cfg.StaleAfter) && inSession)
			row.Found = true
		} else {
			rows, err := u.repo.Bars(ctx, idx.Code, "1d", time.Time{}, now, 2)
			if err != nil {
				return nil, err
			}
			if len(rows) > 0 {
				b := rows[len(rows)-1]
				row.Source = "1d"
				row.Last, row.High, row.Low, row.PreClose = b.Close, b.High, b.Low, b.PreClose
				row.AsOf = b.Time
				row.Found = true
			}
		}
		if row.PreClose > 0 {
			if row.Last > 0 {
				row.Chg = row.Last - row.PreClose
				row.PctChg = row.Chg / row.PreClose * 100
			}
			if row.High > 0 && row.Low > 0 {
				row.Amplitude = (row.High - row.Low) / row.PreClose * 100
			}
		}
		out = append(out, row)
	}
	return out, nil
}
