package data

import (
	"context"
	"time"

	"server/app/market/internal/biz"
	"server/ent"
	"server/ent/dataqualityissue"
	"server/ent/lhbseat"
	"server/ent/limitpremium"
	"server/ent/marketbreadth"
	"server/ent/marketdata"
	"server/ent/seatstats"
	"server/pkg/market"
	"server/pkg/tradecal"

	entsql "entgo.io/ent/dialect/sql"
)

// 本文件是 P2（监控驾驶舱业务监控方案 §7）的仓储：
// K 线连续性检查（缺口日）、昨涨停今溢价快照、20 日均线上占比，
// 及目录册增补 B2 的席位胜率时序（lhb_seat × market_data → seat_stats）。

// DailyBarDays 返回 [from,to) 内全部 1d 收盘行的 symbol+日，只取存在性不取价量。
func (r *Repo) DailyBarDays(ctx context.Context, from, to time.Time) ([]biz.DailyBarDay, error) {
	var rows []struct {
		Symbol  string    `json:"symbol"`
		BarTime time.Time `json:"bar_time"`
	}
	err := r.db.MarketData.Query().
		Where(marketdata.FreqEQ("1d"), marketdata.BarTimeGTE(market.DateOf(from)), marketdata.BarTimeLT(market.DateOf(to))).
		Select(marketdata.FieldSymbol, marketdata.FieldBarTime).Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	out := make([]biz.DailyBarDay, 0, len(rows))
	for _, row := range rows {
		out = append(out, biz.DailyBarDay{Symbol: row.Symbol, Day: row.BarTime.In(tradecal.Shanghai())})
	}
	return out, nil
}

// DailyBarsOn 返回某交易日全部 1d 收盘行，按 symbol 索引。
func (r *Repo) DailyBarsOn(ctx context.Context, day time.Time) (map[string]market.DayBar, error) {
	rows, err := r.db.MarketData.Query().
		Where(marketdata.FreqEQ("1d"), marketdata.BarTimeEQ(market.DateOf(day))).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]market.DayBar, len(rows))
	for _, row := range rows {
		out[row.Symbol] = toDayBar(row)
	}
	return out, nil
}

// SaveIssues 把质量问题 upsert 进 data_quality_issue（与 datahub 口径一致：重复出现只累加 hits、更新 last_seen）。
func (r *Repo) SaveIssues(ctx context.Context, issues []biz.QualityIssue) error {
	now := time.Now()
	type key struct{ domain, check, symbol, day string }
	merged := map[key]biz.QualityIssue{}
	hits := map[key]int{}
	order := make([]key, 0, len(issues))
	for _, is := range issues {
		k := key{is.Domain, is.Check, is.Symbol, pgDay(is.TradeDate).Format("20060102")}
		if _, ok := merged[k]; !ok {
			order = append(order, k)
		}
		merged[k] = is
		hits[k]++
	}
	return chunks(len(order), func(lo, hi int) error {
		bulk := make([]*ent.DataQualityIssueCreate, 0, hi-lo)
		for _, k := range order[lo:hi] {
			is := merged[k]
			c := r.db.DataQualityIssue.Create().
				SetDomain(is.Domain).
				SetCheckName(is.Check).
				SetSymbol(is.Symbol).
				SetTradeDate(pgDay(is.TradeDate)).
				SetSeverity(is.Severity).
				SetSource(is.Source).
				SetMessage(is.Message).
				SetHits(hits[k]).
				SetFirstSeen(now).
				SetLastSeen(now)
			if is.Detail != nil {
				c.SetDetail(is.Detail)
			}
			bulk = append(bulk, c)
		}
		return r.db.DataQualityIssue.CreateBulk(bulk...).
			OnConflictColumns(
				dataqualityissue.FieldDomain, dataqualityissue.FieldCheckName,
				dataqualityissue.FieldSymbol, dataqualityissue.FieldTradeDate,
			).
			Update(func(u *ent.DataQualityIssueUpsert) {
				u.AddHits(1)
				u.UpdateLastSeen()
				u.UpdateMessage()
				u.UpdateDetail()
				u.UpdateSeverity()
				u.UpdateSource()
			}).Exec(ctx)
	})
}

// setPos 给 nillable 数值列赋值：v>0 才落，否则保持 NULL（缺口不编 0）。
func setPos(v float64) *float64 {
	if v <= 0 {
		return nil
	}
	return &v
}

// SavePremiums upsert limit_premium（trade_date+symbol 唯一），开盘与收盘任务共用。
func (r *Repo) SavePremiums(ctx context.Context, rows []biz.PremiumRow) error {
	return chunks(len(rows), func(lo, hi int) error {
		bulk := make([]*ent.LimitPremiumCreate, 0, hi-lo)
		for _, row := range rows[lo:hi] {
			bulk = append(bulk, r.db.LimitPremium.Create().
				SetSymbol(row.Symbol).SetTradeDate(pgDay(row.TradeDate)).SetBaseDate(pgDay(row.BaseDate)).
				SetName(row.Name).SetConsecutive(row.Consecutive).
				SetNillablePreClose(setPos(row.BaseClose)).
				SetNillableOpen(setPos(row.Open)).SetNillableHigh(setPos(row.High)).SetNillableClose(setPos(row.Close)).
				SetNillableOpenPremium(setPos(row.OpenPremium)).
				SetNillableClosePremium(setPos(row.ClosePremium)).
				SetNillableHighPremium(setPos(row.HighPremium)).
				SetLimitAgain(row.LimitAgain).SetSource(row.Source).SetAsOf(row.AsOf))
		}
		return r.db.LimitPremium.CreateBulk(bulk...).
			OnConflictColumns(limitpremium.FieldTradeDate, limitpremium.FieldSymbol).
			UpdateNewValues().Exec(ctx)
	})
}

func premiumFromEnt(row *ent.LimitPremium) biz.PremiumRow {
	out := biz.PremiumRow{
		Symbol: row.Symbol, Name: row.Name,
		TradeDate: fromPgDay(row.TradeDate), BaseDate: fromPgDay(row.BaseDate),
		Consecutive: row.Consecutive, LimitAgain: row.LimitAgain, Source: row.Source, AsOf: row.AsOf,
	}
	out.BaseClose = f64(row.PreClose)
	out.Open = f64(row.Open)
	out.High = f64(row.High)
	out.Close = f64(row.Close)
	out.OpenPremium = f64(row.OpenPremium)
	out.ClosePremium = f64(row.ClosePremium)
	out.HighPremium = f64(row.HighPremium)
	out.HasClose = row.ClosePremium != nil
	return out
}

// PremiumRows 读某日全部 limit_premium 行。
func (r *Repo) PremiumRows(ctx context.Context, day time.Time) ([]biz.PremiumRow, error) {
	rows, err := r.db.LimitPremium.Query().
		Where(limitpremium.TradeDateEQ(pgDay(day))).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]biz.PremiumRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, premiumFromEnt(row))
	}
	return out, nil
}

// PremiumRange 读 [from,to] 区间内的 limit_premium 行，按 trade_date 倒序。
func (r *Repo) PremiumRange(ctx context.Context, from, to time.Time) ([]biz.PremiumRow, error) {
	rows, err := r.db.LimitPremium.Query().
		Where(limitpremium.TradeDateGTE(pgDay(from)), limitpremium.TradeDateLTE(pgDay(to))).
		Order(limitpremium.ByTradeDate(entsql.OrderDesc()), limitpremium.BySymbol()).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]biz.PremiumRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, premiumFromEnt(row))
	}
	return out, nil
}

// SaveBreadth upsert market_breadth（trade_date 唯一）。
func (r *Repo) SaveBreadth(ctx context.Context, rec biz.BreadthDay) error {
	return r.db.MarketBreadth.Create().
		SetTradeDate(pgDay(rec.Day)).SetAsOf(rec.AsOf).SetWindow(rec.Window).
		SetAbove(rec.Above).SetBelow(rec.Below).SetFlat(rec.Flat).SetTotal(rec.Total).
		SetRatio(rec.Ratio).SetSource(rec.Source).
		OnConflictColumns(marketbreadth.FieldTradeDate).
		UpdateNewValues().Exec(ctx)
}

// BreadthRange 读 [from,to] 区间内的 market_breadth 行，按 trade_date 倒序。
func (r *Repo) BreadthRange(ctx context.Context, from, to time.Time) ([]biz.BreadthRecord, error) {
	rows, err := r.db.MarketBreadth.Query().
		Where(marketbreadth.TradeDateGTE(pgDay(from)), marketbreadth.TradeDateLTE(pgDay(to))).
		Order(marketbreadth.ByTradeDate(entsql.OrderDesc())).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]biz.BreadthRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, biz.BreadthRecord{
			Day: fromPgDay(row.TradeDate), AsOf: row.AsOf, Window: row.Window,
			Above: row.Above, Below: row.Below, Flat: row.Flat, Total: row.Total,
			Ratio: row.Ratio, Source: row.Source,
		})
	}
	return out, nil
}

/* ---------- 席位胜率时序（目录册增补 B2） ---------- */

// LhbSeatRange 读 [from,to) 区间内的 lhb_seat 行（symbol+交易日+席位名），供胜率聚合。
func (r *Repo) LhbSeatRange(ctx context.Context, from, to time.Time) ([]biz.SeatRecord, error) {
	var rows []struct {
		Symbol    string    `json:"symbol"`
		TradeDate time.Time `json:"trade_date"`
		SeatName  string    `json:"seat_name"`
	}
	err := r.db.LhbSeat.Query().
		Where(lhbseat.TradeDateGTE(market.DateOf(from)), lhbseat.TradeDateLT(market.DateOf(to))).
		Select(lhbseat.FieldSymbol, lhbseat.FieldTradeDate, lhbseat.FieldSeatName).
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	out := make([]biz.SeatRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, biz.SeatRecord{
			Symbol: row.Symbol, Day: row.TradeDate.In(tradecal.Shanghai()), Seat: row.SeatName,
		})
	}
	return out, nil
}

// SaveSeatStats upsert seat_stats（seat_name+as_of 唯一）。空集合不动表。
// 六收益率列直传指针——负收益是合法值，样本=0 的期保持 NULL（不落 0）。
func (r *Repo) SaveSeatStats(ctx context.Context, rows []biz.SeatStatRow) error {
	return chunks(len(rows), func(lo, hi int) error {
		bulk := make([]*ent.SeatStatsCreate, 0, hi-lo)
		for _, row := range rows[lo:hi] {
			bulk = append(bulk, r.db.SeatStats.Create().
				SetSeatName(row.Seat).SetAsOf(pgDay(row.AsOf)).
				SetLookback(row.Lookback).SetSample(row.Sample).SetSampleT5(row.SampleT5).
				SetNillableWinRateT1(row.WinRateT1).SetNillableAvgRetT1(row.AvgRetT1).SetNillableMedRetT1(row.MedRetT1).
				SetNillableWinRateT5(row.WinRateT5).SetNillableAvgRetT5(row.AvgRetT5).SetNillableMedRetT5(row.MedRetT5).
				SetSource(row.Source))
		}
		return r.db.SeatStats.CreateBulk(bulk...).
			OnConflictColumns(seatstats.FieldSeatName, seatstats.FieldAsOf).
			UpdateNewValues().Exec(ctx)
	})
}

// SeatStatsRows 读某基准日全部 seat_stats 行。
func (r *Repo) SeatStatsRows(ctx context.Context, day time.Time) ([]biz.SeatStatRow, error) {
	rows, err := r.db.SeatStats.Query().
		Where(seatstats.AsOfEQ(pgDay(day))).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]biz.SeatStatRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, biz.SeatStatRow{
			Seat: row.SeatName, AsOf: fromPgDay(row.AsOf), Lookback: row.Lookback,
			Sample: row.Sample, SampleT5: row.SampleT5,
			WinRateT1: row.WinRateT1, AvgRetT1: row.AvgRetT1, MedRetT1: row.MedRetT1,
			WinRateT5: row.WinRateT5, AvgRetT5: row.AvgRetT5, MedRetT5: row.MedRetT5,
			Source: row.Source,
		})
	}
	return out, nil
}
