// Package data 是 datahub 的数据访问层：ent 仓储、Redis 快照、外部数据源客户端。
package data

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"server/app/datahub/internal/biz"
	"server/ent"
	"server/ent/backfilljob"
	"server/ent/collectcursor"
	"server/ent/dataqualityissue"
	"server/ent/financereport"
	"server/ent/hotrank"
	"server/ent/hsgttop10"
	"server/ent/lhbseat"
	"server/ent/limitpool"
	"server/ent/macroseries"
	"server/ent/margindetail"
	"server/ent/marketdata"
	"server/ent/moneyflow"
	"server/ent/overseasquote"
	"server/ent/rawresponse"
	"server/ent/sector"
	"server/ent/sectormember"
	"server/ent/sourcehealth"
	"server/ent/stockbasic"
	"server/pkg/events"
	"server/pkg/outbox"
	"server/pkg/tradecal"

	entsql "entgo.io/ent/dialect/sql"
)

const batchSize = 1000

// Repo 用 ent 实现 biz.Repo。
type Repo struct {
	client *ent.Client
}

func NewRepo(client *ent.Client) *Repo { return &Repo{client: client} }

var _ biz.Repo = (*Repo)(nil)

// withReady 在一个事务里执行写入，并把 md.<domain>.ready 写进 outbox。
func (r *Repo) withReady(ctx context.Context, rd biz.Ready, fn func(tx *ent.Tx) (int, error)) (int, error) {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return 0, err
	}
	n, err := fn(tx)
	if err != nil {
		_ = tx.Rollback()
		return 0, err
	}
	if rd.Domain != "" {
		rd.Count = n
		env, err := events.New("datahub", events.SubjectMDReady(rd.Domain), events.TraceID(ctx), rd)
		if err != nil {
			_ = tx.Rollback()
			return 0, err
		}
		if err := outbox.Insert(ctx, tx, env); err != nil {
			_ = tx.Rollback()
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return n, nil
}

func chunks[T any](items []T, fn func(part []T) error) error {
	for i := 0; i < len(items); i += batchSize {
		j := i + batchSize
		if j > len(items) {
			j = len(items)
		}
		if err := fn(items[i:j]); err != nil {
			return err
		}
	}
	return nil
}

func shanghaiLoc() *time.Location { return tradecal.Shanghai() }

func day(t time.Time) time.Time {
	d := t.In(tradecal.Shanghai())
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, tradecal.Shanghai())
}

func (r *Repo) UpsertSecurities(ctx context.Context, items []biz.Security, source string, rd biz.Ready) (int, error) {
	return r.withReady(ctx, rd, func(tx *ent.Tx) (int, error) {
		err := chunks(items, func(part []biz.Security) error {
			bulk := make([]*ent.StockBasicCreate, 0, len(part))
			for _, s := range part {
				c := tx.StockBasic.Create().
					SetStockCode(s.Symbol).
					SetStockName(s.Name).
					SetMarket(s.Market).
					SetStFlag(s.ST).
					SetSuspendFlag(s.Suspended).
					SetUpdateTime(time.Now()).
					SetNillableListDate(s.ListDate).
					SetNillableDelistDate(s.DelistDate).
					SetNillableTotalShare(s.TotalShare).
					SetNillableFloatShare(s.FloatShare)
				if s.Industry != "" {
					c.SetIndustry(s.Industry)
				}
				bulk = append(bulk, c)
			}
			return tx.StockBasic.CreateBulk(bulk...).
				OnConflictColumns(stockbasic.FieldStockCode).
				Update(func(u *ent.StockBasicUpsert) {
					u.UpdateStockName()
					u.UpdateMarket()
					u.UpdateStFlag()
					u.UpdateSuspendFlag()
					u.UpdateUpdateTime()
				}).Exec(ctx)
		})
		if err != nil {
			return 0, fmt.Errorf("upsert stock_basic: %w", err)
		}
		// 各源字段完整度不同，可空字段只在有值时覆盖，避免备源把主源写好的上市日、股本清空。
		for _, s := range items {
			upd := tx.StockBasic.Update().Where(stockbasic.StockCodeEQ(s.Symbol))
			n := 0
			if s.ListDate != nil {
				upd.SetListDate(*s.ListDate)
				n++
			}
			if s.DelistDate != nil {
				upd.SetDelistDate(*s.DelistDate)
				n++
			}
			if s.TotalShare != nil {
				upd.SetTotalShare(*s.TotalShare)
				n++
			}
			if s.FloatShare != nil {
				upd.SetFloatShare(*s.FloatShare)
				n++
			}
			if s.Industry != "" {
				upd.SetIndustry(s.Industry)
				n++
			}
			if n == 0 {
				continue
			}
			if err := upd.Exec(ctx); err != nil {
				return 0, err
			}
		}
		return len(items), nil
	})
}

func (r *Repo) UpsertBars(ctx context.Context, bars []biz.Bar, source string, rd biz.Ready) (int, error) {
	return r.withReady(ctx, rd, func(tx *ent.Tx) (int, error) {
		err := chunks(bars, func(part []biz.Bar) error {
			bulk := make([]*ent.MarketDataCreate, 0, len(part))
			for _, b := range part {
				bulk = append(bulk, tx.MarketData.Create().
					SetSymbol(b.Symbol).
					SetBarTime(b.Time).
					SetFreq(b.Freq).
					SetOpen(b.Open).
					SetHigh(b.High).
					SetLow(b.Low).
					SetClose(b.Close).
					SetVolume(b.Volume).
					SetAmount(b.Amount).
					SetNillablePreClose(b.PreClose).
					SetSource(source))
			}
			return tx.MarketData.CreateBulk(bulk...).
				OnConflictColumns(marketdata.FieldSymbol, marketdata.FieldBarTime, marketdata.FieldFreq).
				Update(func(u *ent.MarketDataUpsert) {
					u.UpdateOpen()
					u.UpdateHigh()
					u.UpdateLow()
					u.UpdateClose()
					u.UpdateVolume()
					u.UpdateAmount()
					u.UpdateSource()
				}).Exec(ctx)
		})
		if err != nil {
			return 0, fmt.Errorf("upsert market_data: %w", err)
		}
		// 前收只在源头给出时覆盖，通达信文件没有前收。
		for _, b := range bars {
			if b.PreClose == nil || b.Freq != "1d" {
				continue
			}
			if err := tx.MarketData.Update().
				Where(marketdata.SymbolEQ(b.Symbol), marketdata.BarTimeEQ(b.Time), marketdata.FreqEQ(b.Freq)).
				SetPreClose(*b.PreClose).Exec(ctx); err != nil {
				return 0, err
			}
		}
		return len(bars), nil
	})
}

func (r *Repo) UpdateAdjFactors(ctx context.Context, items []biz.AdjFactor, rd biz.Ready) (int, error) {
	return r.withReady(ctx, rd, func(tx *ent.Tx) (int, error) {
		n := 0
		for _, a := range items {
			k, err := tx.MarketData.Update().
				Where(marketdata.SymbolEQ(a.Symbol), marketdata.BarTimeEQ(day(a.Date)), marketdata.FreqEQ("1d")).
				SetAdjFactor(a.Factor).Save(ctx)
			if err != nil {
				return 0, err
			}
			n += k
		}
		return n, nil
	})
}

func (r *Repo) UpsertLimitPool(ctx context.Context, d time.Time, items []biz.LimitEntry, source string, rd biz.Ready) (int, error) {
	now := time.Now()
	return r.withReady(ctx, rd, func(tx *ent.Tx) (int, error) {
		bulk := make([]*ent.LimitPoolCreate, 0, len(items))
		for _, e := range items {
			bulk = append(bulk, tx.LimitPool.Create().
				SetTradeDate(day(d)).
				SetSymbol(e.Symbol).
				SetPool(e.Pool).
				SetName(e.Name).
				SetNillableClose(e.Close).
				SetNillablePctChg(e.PctChg).
				SetNillableAmount(e.Amount).
				SetNillableFirstSealAt(e.FirstSealAt).
				SetNillableLastSealAt(e.LastSealAt).
				SetOpenCount(e.OpenCount).
				SetSealAmount(e.SealAmount).
				SetConsecutive(e.Consecutive).
				SetReason(e.Reason).
				SetSource(source).
				SetAsOf(now))
		}
		err := chunks(bulk, func(part []*ent.LimitPoolCreate) error {
			return tx.LimitPool.CreateBulk(part...).
				OnConflictColumns(limitpool.FieldTradeDate, limitpool.FieldSymbol, limitpool.FieldPool).
				UpdateNewValues().Exec(ctx)
		})
		return len(items), err
	})
}

func (r *Repo) UpsertMoneyFlows(ctx context.Context, d time.Time, items []biz.MoneyFlow, source string, rd biz.Ready) (int, error) {
	return r.withReady(ctx, rd, func(tx *ent.Tx) (int, error) {
		bulk := make([]*ent.MoneyFlowCreate, 0, len(items))
		for _, m := range items {
			bulk = append(bulk, tx.MoneyFlow.Create().
				SetSymbol(m.Symbol).
				SetTradeDate(day(d)).
				SetMainNet(m.MainNet).
				SetSuperNet(m.SuperNet).
				SetBigNet(m.BigNet).
				SetMidNet(m.MidNet).
				SetSmallNet(m.SmallNet).
				SetSource(source))
		}
		err := chunks(bulk, func(part []*ent.MoneyFlowCreate) error {
			return tx.MoneyFlow.CreateBulk(part...).
				OnConflictColumns(moneyflow.FieldSymbol, moneyflow.FieldTradeDate).
				UpdateNewValues().Exec(ctx)
		})
		return len(items), err
	})
}

func (r *Repo) UpsertLhbSeats(ctx context.Context, d time.Time, items []biz.LhbSeat, rd biz.Ready) (int, error) {
	// 同一席位同时出现在买卖榜时合并成一行。
	type key struct{ sym, reason, seat string }
	merged := map[key]*biz.LhbSeat{}
	var order []key
	for _, s := range items {
		k := key{s.Symbol, truncate(s.Reason, 128), truncate(s.SeatName, 128)}
		if m, ok := merged[k]; ok {
			m.BuyAmount += s.BuyAmount
			m.SellAmount += s.SellAmount
			continue
		}
		c := s
		c.Reason, c.SeatName = k.reason, k.seat
		merged[k] = &c
		order = append(order, k)
	}
	return r.withReady(ctx, rd, func(tx *ent.Tx) (int, error) {
		bulk := make([]*ent.LhbSeatCreate, 0, len(order))
		for _, k := range order {
			s := merged[k]
			bulk = append(bulk, tx.LhbSeat.Create().
				SetSymbol(s.Symbol).
				SetTradeDate(day(d)).
				SetReason(s.Reason).
				SetSeatName(s.SeatName).
				SetBuyAmount(s.BuyAmount).
				SetSellAmount(s.SellAmount))
		}
		err := chunks(bulk, func(part []*ent.LhbSeatCreate) error {
			return tx.LhbSeat.CreateBulk(part...).
				OnConflictColumns(lhbseat.FieldSymbol, lhbseat.FieldTradeDate, lhbseat.FieldReason, lhbseat.FieldSeatName).
				UpdateNewValues().Exec(ctx)
		})
		return len(order), err
	})
}

func (r *Repo) UpsertMargins(ctx context.Context, d time.Time, items []biz.Margin, source string, rd biz.Ready) (int, error) {
	return r.withReady(ctx, rd, func(tx *ent.Tx) (int, error) {
		bulk := make([]*ent.MarginDetailCreate, 0, len(items))
		for _, m := range items {
			bulk = append(bulk, tx.MarginDetail.Create().
				SetSymbol(m.Symbol).
				SetTradeDate(day(d)).
				SetRzye(m.Rzye).
				SetRzmre(m.Rzmre).
				SetRzche(m.Rzche).
				SetRqye(m.Rqye).
				SetRqmcl(m.Rqmcl).
				SetRzrqye(m.Rzrqye).
				SetSource(source))
		}
		err := chunks(bulk, func(part []*ent.MarginDetailCreate) error {
			return tx.MarginDetail.CreateBulk(part...).
				OnConflictColumns(margindetail.FieldSymbol, margindetail.FieldTradeDate).
				UpdateNewValues().Exec(ctx)
		})
		return len(items), err
	})
}

func (r *Repo) UpsertHsgtTop10(ctx context.Context, d time.Time, items []biz.HsgtTop, source string, rd biz.Ready) (int, error) {
	return r.withReady(ctx, rd, func(tx *ent.Tx) (int, error) {
		bulk := make([]*ent.HsgtTop10Create, 0, len(items))
		for _, h := range items {
			bulk = append(bulk, tx.HsgtTop10.Create().
				SetTradeDate(day(d)).
				SetSymbol(h.Symbol).
				SetChannel(h.Channel).
				SetRank(h.Rank).
				SetName(h.Name).
				SetNillableClose(h.Close).
				SetNillablePctChg(h.PctChg).
				SetAmount(h.Amount).
				SetNillableNetAmount(h.NetAmount).
				SetSource(source))
		}
		err := tx.HsgtTop10.CreateBulk(bulk...).
			OnConflictColumns(hsgttop10.FieldTradeDate, hsgttop10.FieldSymbol, hsgttop10.FieldChannel).
			UpdateNewValues().Exec(ctx)
		return len(items), err
	})
}

// SyncSectors 写板块与成分：新成分记 in_date，消失的成分记 out_date。只处理本次给出的板块。
// 同时把行业、概念回写 stock_basic，供 M03 关联词典使用。
func (r *Repo) SyncSectors(ctx context.Context, d time.Time, items []biz.Sector, source string, rd biz.Ready) (int, error) {
	today := day(d)
	return r.withReady(ctx, rd, func(tx *ent.Tx) (int, error) {
		bulk := make([]*ent.SectorCreate, 0, len(items))
		codes := make([]string, 0, len(items))
		for _, s := range items {
			bulk = append(bulk, tx.Sector.Create().SetCode(s.Code).SetName(truncate(s.Name, 64)).SetKind(s.Kind).SetSource(source))
			codes = append(codes, s.Code)
		}
		if err := chunks(bulk, func(part []*ent.SectorCreate) error {
			return tx.Sector.CreateBulk(part...).OnConflictColumns(sector.FieldCode).UpdateNewValues().Exec(ctx)
		}); err != nil {
			return 0, fmt.Errorf("upsert sector: %w", err)
		}
		open := map[string]map[string]int{}
		for i := 0; i < len(codes); i += batchSize {
			j := min(i+batchSize, len(codes))
			rows, err := tx.SectorMember.Query().
				Where(sectormember.SectorCodeIn(codes[i:j]...), sectormember.OutDateIsNil()).
				All(ctx)
			if err != nil {
				return 0, err
			}
			for _, m := range rows {
				if open[m.SectorCode] == nil {
					open[m.SectorCode] = map[string]int{}
				}
				open[m.SectorCode][m.Symbol] = m.ID
			}
		}
		var add []*ent.SectorMemberCreate
		var closeIDs []int
		changed := 0
		for _, s := range items {
			now := map[string]bool{}
			for _, sym := range s.Members {
				now[sym] = true
				if _, ok := open[s.Code][sym]; !ok {
					add = append(add, tx.SectorMember.Create().SetSectorCode(s.Code).SetSymbol(sym).SetInDate(today))
				}
			}
			for sym, id := range open[s.Code] {
				if !now[sym] {
					closeIDs = append(closeIDs, id)
				}
			}
		}
		if err := chunks(add, func(part []*ent.SectorMemberCreate) error {
			return tx.SectorMember.CreateBulk(part...).
				OnConflictColumns(sectormember.FieldSectorCode, sectormember.FieldSymbol, sectormember.FieldInDate).
				Update(func(u *ent.SectorMemberUpsert) { u.ClearOutDate() }).Exec(ctx)
		}); err != nil {
			return 0, fmt.Errorf("insert sector_member: %w", err)
		}
		changed += len(add)
		if err := chunks(closeIDs, func(part []int) error {
			return tx.SectorMember.Update().Where(sectormember.IDIn(part...)).SetOutDate(today).Exec(ctx)
		}); err != nil {
			return 0, err
		}
		changed += len(closeIDs)
		if err := writeStockSectors(ctx, tx, items); err != nil {
			return 0, err
		}
		return changed, nil
	})
}

func writeStockSectors(ctx context.Context, tx *ent.Tx, items []biz.Sector) error {
	industry := map[string]string{}
	concepts := map[string][]string{}
	for _, s := range items {
		for _, sym := range s.Members {
			switch s.Kind {
			case biz.SectorIndustry:
				if _, ok := industry[sym]; !ok {
					industry[sym] = s.Name
				}
			case biz.SectorConcept:
				concepts[sym] = append(concepts[sym], s.Name)
			}
		}
	}
	syms := map[string]bool{}
	for s := range industry {
		syms[s] = true
	}
	for s := range concepts {
		syms[s] = true
	}
	for sym := range syms {
		upd := tx.StockBasic.Update().Where(stockbasic.StockCodeEQ(sym))
		if v, ok := industry[sym]; ok {
			upd.SetIndustry(truncate(v, 64))
		}
		if v, ok := concepts[sym]; ok {
			sort.Strings(v)
			upd.SetConcept(strings.Join(v, ","))
		}
		if err := upd.Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repo) UpsertFinance(ctx context.Context, items []biz.FinanceItem, source string, rd biz.Ready) (int, error) {
	return r.withReady(ctx, rd, func(tx *ent.Tx) (int, error) {
		bulk := make([]*ent.FinanceReportCreate, 0, len(items))
		seen := map[string]bool{}
		for _, f := range items {
			k := f.Symbol + f.EndDate.Format("20060102") + f.Kind + f.AnnDate.Format("20060102")
			if seen[k] {
				continue
			}
			seen[k] = true
			c := tx.FinanceReport.Create().
				SetSymbol(f.Symbol).
				SetEndDate(day(f.EndDate)).
				SetKind(f.Kind).
				SetAnnDate(day(f.AnnDate)).
				SetForecastType(truncate(f.ForecastType, 16)).
				SetNillableNetProfit(f.NetProfit).
				SetNillableNetProfitYoy(f.NetProfitYoY).
				SetSummary(f.Summary).
				SetSource(source)
			if f.Data != nil {
				c.SetData(f.Data)
			}
			bulk = append(bulk, c)
		}
		err := chunks(bulk, func(part []*ent.FinanceReportCreate) error {
			return tx.FinanceReport.CreateBulk(part...).
				OnConflictColumns(financereport.FieldSymbol, financereport.FieldEndDate, financereport.FieldKind, financereport.FieldAnnDate).
				UpdateNewValues().Exec(ctx)
		})
		return len(bulk), err
	})
}

func (r *Repo) UpsertOverseas(ctx context.Context, items []biz.OverseasQuote, source string, rd biz.Ready) (int, error) {
	return r.withReady(ctx, rd, func(tx *ent.Tx) (int, error) {
		bulk := make([]*ent.OverseasQuoteCreate, 0, len(items))
		for _, q := range items {
			close := q.Close
			bulk = append(bulk, tx.OverseasQuote.Create().
				SetCode(q.Code).
				SetName(truncate(q.Name, 64)).
				SetTradeDate(time.Date(q.TradeDate.Year(), q.TradeDate.Month(), q.TradeDate.Day(), 0, 0, 0, 0, tradecal.Shanghai())).
				SetClose(close).
				SetPctChg(q.PctChg).
				SetAsOf(q.AsOf).
				SetSource(source))
		}
		err := tx.OverseasQuote.CreateBulk(bulk...).
			OnConflictColumns(overseasquote.FieldCode, overseasquote.FieldTradeDate).
			UpdateNewValues().Exec(ctx)
		return len(items), err
	})
}

func (r *Repo) UpsertMacro(ctx context.Context, items []biz.MacroPoint, source string, rd biz.Ready) (int, error) {
	now := time.Now()
	return r.withReady(ctx, rd, func(tx *ent.Tx) (int, error) {
		bulk := make([]*ent.MacroSeriesCreate, 0, len(items))
		for _, m := range items {
			bulk = append(bulk, tx.MacroSeries.Create().
				SetCode(m.Code).
				SetName(truncate(m.Name, 64)).
				SetObsDate(day(m.Date)).
				SetValue(m.Value).
				SetUnit(m.Unit).
				SetSource(source).
				SetAsOf(now))
		}
		err := chunks(bulk, func(part []*ent.MacroSeriesCreate) error {
			return tx.MacroSeries.CreateBulk(part...).
				OnConflictColumns(macroseries.FieldCode, macroseries.FieldObsDate).
				UpdateNewValues().Exec(ctx)
		})
		return len(items), err
	})
}

func (r *Repo) InsertHotRank(ctx context.Context, at time.Time, items []biz.HotItem, source string) (int, error) {
	bulk := make([]*ent.HotRankCreate, 0, len(items))
	for _, h := range items {
		bulk = append(bulk, r.client.HotRank.Create().
			SetBoard(h.Board).SetSnapTime(at).SetRank(h.Rank).SetSymbol(h.Symbol).SetNillableHeat(h.Heat).SetSource(source))
	}
	err := r.client.HotRank.CreateBulk(bulk...).
		OnConflictColumns(hotrank.FieldBoard, hotrank.FieldSnapTime, hotrank.FieldRank).
		UpdateNewValues().Exec(ctx)
	return len(items), err
}

func (r *Repo) ActiveSymbols(ctx context.Context, d time.Time) ([]string, error) {
	dd := day(d)
	q := r.client.StockBasic.Query().Where(
		stockbasic.Or(stockbasic.ListDateIsNil(), stockbasic.ListDateLTE(dd)),
		stockbasic.Or(stockbasic.DelistDateIsNil(), stockbasic.DelistDateGT(dd)),
	)
	// 停牌标记只代表当前状态，补历史时不按它过滤。
	if dd.Equal(day(time.Now())) {
		q = q.Where(stockbasic.SuspendFlagEQ(false))
	}
	return q.Select(stockbasic.FieldStockCode).Strings(ctx)
}

func (r *Repo) DailyBars(ctx context.Context, d time.Time, symbols []string) (map[string]biz.Bar, map[string]string, error) {
	q := r.client.MarketData.Query().Where(marketdata.FreqEQ("1d"), marketdata.BarTimeEQ(day(d)))
	if len(symbols) > 0 {
		q = q.Where(marketdata.SymbolIn(symbols...))
	}
	rows, err := q.All(ctx)
	if err != nil {
		return nil, nil, err
	}
	bars := make(map[string]biz.Bar, len(rows))
	src := make(map[string]string, len(rows))
	for _, m := range rows {
		bars[m.Symbol] = biz.Bar{Symbol: m.Symbol, Time: m.BarTime, Freq: m.Freq,
			Open: deref(m.Open), High: deref(m.High), Low: deref(m.Low), Close: deref(m.Close),
			Volume: derefInt(m.Volume), Amount: deref(m.Amount), PreClose: m.PreClose}
		src[m.Symbol] = m.Source
	}
	return bars, src, nil
}

func (r *Repo) BarSymbols(ctx context.Context, d time.Time, freq string) ([]string, error) {
	start := day(d)
	return r.client.MarketData.Query().
		Where(marketdata.FreqEQ(freq), marketdata.BarTimeGTE(start), marketdata.BarTimeLT(start.AddDate(0, 0, 1))).
		Unique(true).Select(marketdata.FieldSymbol).Strings(ctx)
}

func (r *Repo) SaveIssues(ctx context.Context, issues []biz.Issue) error {
	// 同一批里同键的问题先合并，避免 ON CONFLICT 在一条语句里命中两次。
	type key struct{ d, c, s, t string }
	merged := map[key]biz.Issue{}
	hits := map[key]int{}
	var order []key
	for _, is := range issues {
		k := key{is.Domain, is.Check, is.Symbol, day(is.TradeDate).Format("20060102")}
		if _, ok := merged[k]; !ok {
			order = append(order, k)
		}
		merged[k] = is
		hits[k]++
	}
	now := time.Now()
	bulk := make([]*ent.DataQualityIssueCreate, 0, len(order))
	for _, k := range order {
		is := merged[k]
		c := r.client.DataQualityIssue.Create().
			SetDomain(is.Domain).
			SetCheckName(is.Check).
			SetSymbol(is.Symbol).
			SetTradeDate(day(is.TradeDate)).
			SetSeverity(is.Severity).
			SetSource(is.Source).
			SetOtherSource(is.OtherSource).
			SetMessage(truncate(is.Message, 2000)).
			SetHits(hits[k]).
			SetFirstSeen(now).
			SetLastSeen(now)
		if is.Detail != nil {
			c.SetDetail(is.Detail)
		}
		bulk = append(bulk, c)
	}
	return chunks(bulk, func(part []*ent.DataQualityIssueCreate) error {
		return r.client.DataQualityIssue.CreateBulk(part...).
			OnConflictColumns(dataqualityissue.FieldDomain, dataqualityissue.FieldCheckName, dataqualityissue.FieldSymbol, dataqualityissue.FieldTradeDate).
			Update(func(u *ent.DataQualityIssueUpsert) {
				u.AddHits(1)
				u.UpdateLastSeen()
				u.UpdateMessage()
				u.UpdateDetail()
				u.UpdateSeverity()
				u.UpdateSource()
				u.UpdateOtherSource()
			}).Exec(ctx)
	})
}

func (r *Repo) ListIssues(ctx context.Context, d *time.Time, domain string, limit int) ([]biz.StoredIssue, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	q := r.client.DataQualityIssue.Query()
	if d != nil {
		q = q.Where(dataqualityissue.TradeDateEQ(day(*d)))
	}
	if domain != "" {
		q = q.Where(dataqualityissue.DomainEQ(domain))
	}
	rows, err := q.Order(dataqualityissue.ByLastSeen(entsql.OrderDesc())).Limit(limit).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]biz.StoredIssue, 0, len(rows))
	for _, x := range rows {
		out = append(out, biz.StoredIssue{ID: x.ID, Hits: x.Hits, LastSeen: x.LastSeen, Issue: biz.Issue{
			Domain: x.Domain, Check: x.CheckName, Symbol: x.Symbol, TradeDate: x.TradeDate, Severity: x.Severity,
			Source: x.Source, OtherSource: x.OtherSource, Message: x.Message, Detail: x.Detail}})
	}
	return out, nil
}

func (r *Repo) SaveHealth(ctx context.Context, rows []biz.Health) error {
	if len(rows) == 0 {
		return nil
	}
	bulk := make([]*ent.SourceHealthCreate, 0, len(rows))
	for _, h := range rows {
		c := r.client.SourceHealth.Create().
			SetDomain(h.Domain).
			SetSource(h.Source).
			SetPriority(h.Priority).
			SetEnabled(h.Enabled).
			SetOfficial(h.Official).
			SetState(h.State).
			SetConsecutiveFailures(h.Failures).
			SetSuccessCount(h.Success).
			SetFailureCount(h.Failure).
			SetLastLatencyMs(h.LastLatency.Milliseconds()).
			SetAvgLatencyMs(h.AvgLatency.Milliseconds()).
			SetLastError(h.LastError).
			SetQuotaUsed(h.QuotaUsed).
			SetQuotaLimit(h.QuotaLimit).
			SetUpdatedAt(time.Now())
		if !h.LastSuccessAt.IsZero() {
			c.SetLastSuccessAt(h.LastSuccessAt)
		}
		if !h.LastFailureAt.IsZero() {
			c.SetLastFailureAt(h.LastFailureAt)
		}
		bulk = append(bulk, c)
	}
	return r.client.SourceHealth.CreateBulk(bulk...).
		OnConflictColumns(sourcehealth.FieldDomain, sourcehealth.FieldSource).
		UpdateNewValues().Exec(ctx)
}

func (r *Repo) LoadHealth(ctx context.Context) ([]biz.Health, error) {
	rows, err := r.client.SourceHealth.Query().All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]biz.Health, 0, len(rows))
	for _, x := range rows {
		h := biz.Health{Domain: x.Domain, Source: x.Source, Success: x.SuccessCount, Failure: x.FailureCount,
			AvgLatency: time.Duration(x.AvgLatencyMs) * time.Millisecond, LastError: x.LastError}
		if x.LastSuccessAt != nil {
			h.LastSuccessAt = *x.LastSuccessAt
		}
		if x.LastFailureAt != nil {
			h.LastFailureAt = *x.LastFailureAt
		}
		if day(x.UpdatedAt).Equal(day(time.Now())) {
			h.QuotaUsed = x.QuotaUsed
		}
		out = append(out, h)
	}
	return out, nil
}

func (r *Repo) Cursor(ctx context.Context, name string) (string, error) {
	row, err := r.client.CollectCursor.Query().Where(collectcursor.NameEQ(name)).Only(ctx)
	if ent.IsNotFound(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return row.Value, nil
}

func (r *Repo) SetCursor(ctx context.Context, name, value string) error {
	return r.client.CollectCursor.Create().SetName(name).SetValue(value).SetUpdatedAt(time.Now()).
		OnConflictColumns(collectcursor.FieldName).UpdateNewValues().Exec(ctx)
}

func (r *Repo) CreateBackfill(ctx context.Context, j biz.BackfillJob) (biz.BackfillJob, error) {
	row, err := r.client.BackfillJob.Create().
		SetDomain(j.Domain).
		SetSymbols(j.Symbols).
		SetStartDate(day(j.Start)).
		SetEndDate(day(j.End)).
		SetSource(j.Source).
		SetStatus(j.Status).
		SetTotal(j.Total).
		SetRequestedBy(truncate(j.RequestedBy, 32)).
		Save(ctx)
	if err != nil {
		return biz.BackfillJob{}, err
	}
	return toJob(row), nil
}

func (r *Repo) UpdateBackfill(ctx context.Context, j biz.BackfillJob) error {
	return r.client.BackfillJob.UpdateOneID(j.ID).
		SetStatus(j.Status).
		SetDone(j.Done).
		SetRows(j.Rows).
		SetError(j.Error).
		SetNillableStartedAt(j.StartedAt).
		SetNillableFinishedAt(j.FinishedAt).
		Exec(ctx)
}

func (r *Repo) GetBackfill(ctx context.Context, id int) (biz.BackfillJob, error) {
	row, err := r.client.BackfillJob.Get(ctx, id)
	if err != nil {
		return biz.BackfillJob{}, err
	}
	return toJob(row), nil
}

// PendingBackfills 返回待执行的任务。服务重启时停在 running 的任务也重新执行，写入是幂等的。
func (r *Repo) PendingBackfills(ctx context.Context) ([]biz.BackfillJob, error) {
	rows, err := r.client.BackfillJob.Query().
		Where(backfilljob.StatusIn(biz.JobPending, biz.JobRunning)).
		Order(backfilljob.ByCreatedAt()).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]biz.BackfillJob, 0, len(rows))
	for _, x := range rows {
		j := toJob(x)
		if j.Status == biz.JobRunning {
			j.Done, j.Rows = 0, 0
		}
		out = append(out, j)
	}
	return out, nil
}

func toJob(x *ent.BackfillJob) biz.BackfillJob {
	return biz.BackfillJob{ID: x.ID, Domain: x.Domain, Symbols: x.Symbols, Start: x.StartDate, End: x.EndDate,
		Source: x.Source, Status: x.Status, Total: x.Total, Done: x.Done, Rows: x.Rows, Error: x.Error,
		RequestedBy: x.RequestedBy, CreatedAt: x.CreatedAt, StartedAt: x.StartedAt, FinishedAt: x.FinishedAt}
}

func (r *Repo) SaveRaw(ctx context.Context, raw biz.RawRecord) error {
	return r.client.RawResponse.Create().
		SetSource(raw.Source).SetDomain(raw.Domain).SetRequest(raw.Request).
		SetStatus(raw.Status).SetBody(raw.Body).SetFetchedAt(raw.At).Exec(ctx)
}

func (r *Repo) PurgeRaw(ctx context.Context, before time.Time) (int, error) {
	return r.client.RawResponse.Delete().Where(rawresponse.FetchedAtLT(before)).Exec(ctx)
}

func (r *Repo) DispatchOutbox(ctx context.Context, pub biz.Publisher, limit int) (int, error) {
	return outbox.Dispatch(ctx, r.client, pub, limit)
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

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
