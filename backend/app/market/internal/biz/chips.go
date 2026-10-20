package biz

import (
	"context"
	"time"

	"server/pkg/market"
)

/* ---------- 筹码分布（目录册增补 B3） ---------- */

const (
	// chipStatusMissing = 库中无该券或该窗口一根日线都没有（没取到，显式缺失）。
	chipStatusMissing = "missing"

	chipDefaultDays = 250
	chipMaxDays     = 400
)

// ChipReport 是筹码分布的 biz 视图。Status!=market.ChipOK 时价格/衍生列全 nil——不落 0 冒充。
type ChipReport struct {
	Symbol          string
	Status          string // chipStatusMissing / market.ChipOK / market.ChipNotMeaningful / market.ChipError
	Reason          string
	Profit          *float64
	AvgCost         *float64
	Cost90Lo        *float64
	Cost90Hi        *float64
	Cost70Lo        *float64
	Cost70Hi        *float64
	Concentration90 *float64
	Concentration70 *float64
	PeakPrice       *float64
	Price           *float64
	DaysUsed        int
	CumTurnoverPct  float64
	WindowStart     time.Time
	WindowEnd       time.Time
}

// mapChipResult 把 pkg 纯函数三态映射到报告（指针直传，不解引不托底）。
func mapChipResult(symbol string, res market.ChipResult) *ChipReport {
	rep := &ChipReport{
		Symbol:         symbol,
		Status:         res.Status,
		Reason:         res.Reason,
		DaysUsed:       res.Days,
		CumTurnoverPct: res.CumTurnoverPct,
		WindowStart:    res.WindowStart,
		WindowEnd:      res.WindowEnd,
	}
	if res.Status == market.ChipOK {
		rep.Profit = res.Profit
		rep.AvgCost = res.AvgCost
		rep.Cost90Lo = res.Cost90Lo
		rep.Cost90Hi = res.Cost90Hi
		rep.Cost70Lo = res.Cost70Lo
		rep.Cost70Hi = res.Cost70Hi
		rep.Concentration90 = res.Concentration90
		rep.Concentration70 = res.Concentration70
		rep.PeakPrice = res.PeakPrice
		p := res.Price
		rep.Price = &p
	}
	return rep
}

// ChipDistribution 以换手衰减模型算某券 asOf 及之前最近 days 根 1d 收盘的筹码分布。
// 纯计算，不落库。dtype「没取到≠0」全链：
//   - 库中无该券、窗口内无日线 → missing；
//   - float_share 缺失/为 0（无法算换手率）、adj_factor 缺失（不可复权，§4.4 禁以 1 冒充） → not_meaningful；
//   - grid_size 越域、序列过短（<MinPoints）等 → 由 pkg 纯函数回 error / not_meaningful。
//
// 前复权口径 = 序列末根因子（通达信默认）；adjusted 价乘 adj/base 不会改换手率。
func (u *Usecase) ChipDistribution(ctx context.Context, symbol string, asOf time.Time, days, gridSize int) (*ChipReport, error) {
	if days <= 0 {
		days = chipDefaultDays
	}
	if days > chipMaxDays {
		days = chipMaxDays
	}
	basic, err := u.repo.StockBasic(ctx, symbol)
	if err != nil {
		return nil, err
	}
	if !basic.Found {
		return &ChipReport{Symbol: symbol, Status: chipStatusMissing, Reason: "stock_basic 无该证券"}, nil
	}
	if basic.FloatShare <= 0 {
		return &ChipReport{Symbol: symbol, Status: market.ChipNotMeaningful, Reason: "float_share 缺失，无法计算换手率"}, nil
	}
	rows, err := u.repo.Bars(ctx, symbol, "1d", time.Time{}, asOf.AddDate(0, 0, 1), days)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return &ChipReport{Symbol: symbol, Status: chipStatusMissing, Reason: "窗口内无日线"}, nil
	}
	// 前复权（§4.4：adj_factor 缺失不可复权，整体 not_meaningful，禁止以 0/1 冒充因子后递推）。
	base := rows[len(rows)-1].Adj
	if base <= 0 {
		return &ChipReport{Symbol: symbol, Status: market.ChipNotMeaningful, Reason: "末根 adj_factor 缺失，不可前复权"}, nil
	}
	bars := make([]market.ChipBar, 0, len(rows))
	for _, r := range rows {
		if r.Adj <= 0 {
			return &ChipReport{Symbol: symbol, Status: market.ChipNotMeaningful, Reason: "adj_factor 缺失，不可前复权"}, nil
		}
		k := r.Adj / base
		bars = append(bars, market.ChipBar{
			Day:   market.DateOf(r.Time),
			High:  r.High * k,
			Low:   r.Low * k,
			Close: r.Close * k,
			Turn:  float64(r.Volume) / float64(basic.FloatShare) * 100,
		})
	}
	res := market.ChipDistribution(bars, market.ChipOptions{GridSize: gridSize})
	return mapChipResult(symbol, res), nil
}
