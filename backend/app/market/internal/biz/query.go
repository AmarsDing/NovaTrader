package biz

import (
	"context"
	"fmt"
	"time"

	"server/pkg/market"
)

const (
	maxBars     = 10000
	defaultBars = 1000
)

// Bars 返回 K 线。前复权以返回区间最后一根的因子为基准。
func (u *Usecase) Bars(ctx context.Context, symbol, freq string, start, end time.Time, mode market.Adjust, limit int) ([]BarRow, error) {
	if limit <= 0 {
		limit = defaultBars
	}
	if limit > maxBars {
		limit = maxBars
	}
	rows, err := u.repo.Bars(ctx, symbol, freq, start, end, limit)
	if err != nil || len(rows) == 0 {
		return rows, err
	}
	base := rows[len(rows)-1].Adj
	for i := range rows {
		rows[i].Bar = market.AdjustBar(rows[i].Bar, rows[i].Adj, base, mode)
	}
	return rows, nil
}

// maxBatchSymbols / maxBatchBars 是 GetBarsBatch 的批量闸：每码默认/上限条数与整批码数。
const (
	maxBatchSymbols = 500
	maxBatchBars    = 500
)

// BarsBatch 一次取回多码日线（M13 W2 留账：T01 全市场读放大收敛）。
// 每码独立尾截 limit、独立以自身窗口末根因子折算（与 Bars 同口径）；
// start 空时按 limit×3 自然日自 end 反推起算（250 交易日窗口余量）；
// 窗口内无行的码在结果中缺席——缺席即缺数，调用方不得补 0 行。
func (u *Usecase) BarsBatch(ctx context.Context, symbols []string, start, end time.Time, mode market.Adjust, limit int) (map[string][]BarRow, error) {
	if len(symbols) == 0 {
		return nil, fmt.Errorf("symbols required")
	}
	if len(symbols) > maxBatchSymbols {
		symbols = symbols[:maxBatchSymbols]
	}
	if limit <= 0 {
		limit = 250
	}
	if limit > maxBatchBars {
		limit = maxBatchBars
	}
	if end.IsZero() {
		end = time.Now()
	}
	if start.IsZero() {
		start = end.AddDate(0, 0, -3*limit)
	}
	rows, err := u.repo.BarsMulti(ctx, symbols, "1d", start, end)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]BarRow, len(rows))
	for sym, list := range rows {
		if len(list) > limit {
			list = list[len(list)-limit:]
		}
		if len(list) == 0 {
			continue
		}
		base := list[len(list)-1].Adj
		for i := range list {
			list[i].Bar = market.AdjustBar(list[i].Bar, list[i].Adj, base, mode)
		}
		out[sym] = list
	}
	return out, nil
}

// IndicatorPoint 是一根日线上的指标。
type IndicatorPoint struct {
	Time   time.Time
	Values market.Values
}

// Indicators 用截至 end 的全部日线递推，再截出 [start, end]。与实时计算是同一份公式。
func (u *Usecase) Indicators(ctx context.Context, symbol string, start, end time.Time, mode market.Adjust, names []string) ([]IndicatorPoint, error) {
	rows, err := u.repo.Bars(ctx, symbol, "1d", time.Time{}, end, maxBars)
	if err != nil {
		return nil, err
	}
	bars := make([]market.DayBar, len(rows))
	for i, r := range rows {
		bars[i] = market.DayBar{
			Symbol: symbol, Time: r.Time, Open: r.Open, High: r.High, Low: r.Low, Close: r.Close,
			PreClose: r.PreClose, Volume: r.Volume, Amount: r.Amount, Adj: r.Adj,
		}
	}
	vals := market.Series(bars, mode)
	var out []IndicatorPoint
	for i, b := range bars {
		if !start.IsZero() && b.Time.Before(start) {
			continue
		}
		out = append(out, IndicatorPoint{Time: b.Time, Values: pick(vals[i], names)})
	}
	return out, nil
}

func pick(v market.Values, names []string) market.Values {
	if len(names) == 0 {
		return v
	}
	out := market.Values{}
	for _, n := range names {
		if x, ok := v[n]; ok {
			out[n] = x
		}
	}
	return out
}

// live 判断查询能否直接用内存里的引擎：没有指定 as_of，且查询日就是引擎所在交易日。
func (u *Usecase) live(day, asOf time.Time) (*market.Engine, bool) {
	if !asOf.IsZero() {
		return nil, false
	}
	eng := u.Engine()
	if eng == nil || !sameDate(eng.Day(), day) {
		return nil, false
	}
	return eng, true
}

// FactorsCross 返回因子截面。as_of 非空时按时点读库：每只股票取 as_of 之前最新的一行。
func (u *Usecase) FactorsCross(ctx context.Context, day, asOf time.Time, kind string, symbols, names []string) ([]FactorRecord, error) {
	if eng, ok := u.live(day, asOf); ok && (kind == "" || kind == KindIntraday) {
		rows := eng.Cross(symbols)
		out := make([]FactorRecord, len(rows))
		for i, r := range rows {
			r.Values = pick(r.Values, names)
			out[i] = FactorRecord{FactorRow: r, Kind: "live"}
		}
		return out, nil
	}
	if asOf.IsZero() {
		asOf = time.Now()
	}
	rows, err := u.repo.FactorsAt(ctx, day, kind, asOf, symbols)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].Values = pick(rows[i].Values, names)
	}
	return rows, nil
}

// Sentiment 返回情绪截面。
func (u *Usecase) Sentiment(ctx context.Context, day, asOf time.Time) (market.Sentiment, bool, error) {
	if eng, ok := u.live(day, asOf); ok {
		return eng.Sentiment(time.Now()), true, nil
	}
	if asOf.IsZero() {
		asOf = time.Now()
	}
	return u.repo.SentimentAt(ctx, day, asOf)
}

// LimitBoards 返回涨跌停列表。
// 盘中引擎若尚未吃到快照（Boards 为空），回退读库 / M01 limit_pool，避免桌面端整天空表。
func (u *Usecase) LimitBoards(ctx context.Context, day time.Time, direction, status string) ([]market.LimitBoard, error) {
	if eng, ok := u.live(day, time.Time{}); ok {
		var out []market.LimitBoard
		for _, b := range eng.Boards() {
			if (direction == "" || b.Direction == direction) && (status == "" || b.Status == status) {
				out = append(out, b)
			}
		}
		if len(out) > 0 {
			return out, nil
		}
	}
	return u.repo.Boards(ctx, day, direction, status)
}

// SectorHeat 返回板块热度排名。
func (u *Usecase) SectorHeat(ctx context.Context, day, asOf time.Time, top int) ([]market.SectorHeat, time.Time, error) {
	if top <= 0 {
		top = 50
	}
	if _, ok := u.live(day, asOf); ok {
		now := time.Now()
		h := u.SectorHeatLive(now)
		if len(h) > top {
			h = h[:top]
		}
		return h, now, nil
	}
	if asOf.IsZero() {
		asOf = time.Now()
	}
	rows, err := u.repo.SectorHeatAt(ctx, day, asOf, top)
	return rows, asOf, err
}

// SetWatchlist 登记候选池。
func (u *Usecase) SetWatchlist(owner string, symbols []string, ttl time.Duration) time.Time {
	if ttl <= 0 {
		ttl = u.cfg.WatchTTL
	}
	return u.watch.Set(owner, symbols, ttl, time.Now())
}

// GetWatchlist 读取候选池未到期租约（owner 为空=全部 owner）。
func (u *Usecase) GetWatchlist(owner string) []LeaseInfo {
	return u.watch.Leases(time.Now(), owner)
}
