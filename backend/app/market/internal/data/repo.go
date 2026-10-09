package data

import (
	"context"
	"math"
	"sort"
	"time"

	"server/app/market/internal/biz"
	"server/ent"
	"server/ent/lhbseat"
	"server/ent/limitboard"
	"server/ent/limitpool"
	"server/ent/marketdata"
	"server/ent/marketsentiment"
	"server/ent/moneyflow"
	"server/ent/overseasquote"
	"server/ent/sector"
	"server/ent/sectorheat"
	"server/ent/sectormember"
	"server/ent/stockbasic"
	"server/ent/stockfactor"
	"server/ent/strategyconfig"
	"server/pkg/market"
	"server/pkg/tradecal"

	entsql "entgo.io/ent/dialect/sql"
)

// batchSize 控制每条 INSERT 的行数，避免超过 PostgreSQL 65535 个参数上限。
const batchSize = 1000

// SourceAgg 是本服务由分钟线合成的日线来源名。
const SourceAgg = "market_agg"

// Repo 实现 biz.Repo。
type Repo struct {
	db *ent.Client
}

func NewRepo(db *ent.Client) *Repo { return &Repo{db: db} }

// pgDay 把上海日期转成 UTC 零点。date 列按日期部分存储，与连接时区无关。
func pgDay(t time.Time) time.Time {
	d := market.DateOf(t)
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
}

// fromPgDay 把 date 列读回上海日期零点。
func fromPgDay(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, tradecal.Shanghai())
}

func chunks(n int, fn func(lo, hi int) error) error {
	for lo := 0; lo < n; lo += batchSize {
		hi := lo + batchSize
		if hi > n {
			hi = n
		}
		if err := fn(lo, hi); err != nil {
			return err
		}
	}
	return nil
}

func f64(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

func i64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// clean 去掉 NaN 和无穷大，JSON 无法编码它们。
func clean(v market.Values) map[string]float64 {
	out := make(map[string]float64, len(v))
	for k, x := range v {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			continue
		}
		out[k] = x
	}
	return out
}

func (r *Repo) LoadStocks(ctx context.Context) ([]market.StockInfo, error) {
	rows, err := r.db.StockBasic.Query().Where(stockbasic.DelistDateIsNil()).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]market.StockInfo, 0, len(rows))
	for _, row := range rows {
		info := market.StockInfo{
			Symbol: row.StockCode, Name: row.StockName, ST: row.StFlag, Suspended: row.SuspendFlag,
			FloatShare: i64(row.FloatShare),
		}
		if row.ListDate != nil {
			info.ListDate = fromPgDay(*row.ListDate)
		}
		out = append(out, info)
	}
	return out, nil
}

func toDayBar(row *ent.MarketData) market.DayBar {
	return market.DayBar{
		Symbol: row.Symbol, Time: row.BarTime.In(tradecal.Shanghai()),
		Open: f64(row.Open), High: f64(row.High), Low: f64(row.Low), Close: f64(row.Close),
		PreClose: f64(row.PreClose), Volume: i64(row.Volume), Amount: f64(row.Amount), Adj: row.AdjFactor,
	}
}

// DailyHistory 按 500 只一批读，避免一次把百万行读进内存。
func (r *Repo) DailyHistory(ctx context.Context, before time.Time, n int) (map[string][]market.DayBar, error) {
	syms, err := r.db.StockBasic.Query().Where(stockbasic.DelistDateIsNil()).Select(stockbasic.FieldStockCode).Strings(ctx)
	if err != nil {
		return nil, err
	}
	from := before.AddDate(0, 0, -(n*7/5 + 30))
	out := make(map[string][]market.DayBar, len(syms))
	const per = 500
	for lo := 0; lo < len(syms); lo += per {
		hi := lo + per
		if hi > len(syms) {
			hi = len(syms)
		}
		rows, err := r.db.MarketData.Query().Where(
			marketdata.SymbolIn(syms[lo:hi]...),
			marketdata.FreqEQ("1d"),
			marketdata.BarTimeGTE(from),
			marketdata.BarTimeLT(market.DateOf(before)),
		).Order(marketdata.ByBarTime()).All(ctx)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if row.Close == nil {
				continue
			}
			out[row.Symbol] = append(out[row.Symbol], toDayBar(row))
		}
	}
	for sym, bars := range out {
		if len(bars) > n {
			out[sym] = bars[len(bars)-n:]
		}
	}
	return out, nil
}

// SealedOn 优先用本服务的 limit_board；当天没有记录（本服务未运行）时用 M01 的 limit_pool。
func (r *Repo) SealedOn(ctx context.Context, day time.Time) (map[string]int, error) {
	rows, err := r.db.LimitBoard.Query().Where(
		limitboard.TradeDateEQ(pgDay(day)),
		limitboard.DirectionEQ(market.DirUp),
		limitboard.StatusEQ(market.StatusSealed),
	).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]int, len(rows))
	for _, row := range rows {
		out[row.Symbol] = row.Consecutive
	}
	if len(out) > 0 {
		return out, nil
	}
	pool, err := r.db.LimitPool.Query().Where(
		limitpool.TradeDateEQ(pgDay(day)),
		limitpool.PoolEQ("up"),
	).All(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range pool {
		c := row.Consecutive
		if c <= 0 {
			c = 1
		}
		out[row.Symbol] = c
	}
	return out, nil
}

func (r *Repo) ClosePhase(ctx context.Context, day time.Time) (market.Phase, error) {
	row, err := r.db.MarketSentiment.Query().
		Where(marketsentiment.TradeDateEQ(pgDay(day))).
		Order(marketsentiment.ByAsOf(entsql.OrderDesc())).
		First(ctx)
	if ent.IsNotFound(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return market.Phase(row.Phase), nil
}

func (r *Repo) Config(ctx context.Context, key string) (string, error) {
	row, err := r.db.StrategyConfig.Query().Where(strategyconfig.ConfigKeyEQ(key)).Only(ctx)
	if ent.IsNotFound(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return row.ConfigValue, nil
}

func (r *Repo) SaveMinuteBars(ctx context.Context, bars []market.Bar, adj map[string]float64) error {
	return chunks(len(bars), func(lo, hi int) error {
		creates := make([]*ent.MarketDataCreate, 0, hi-lo)
		for _, b := range bars[lo:hi] {
			a := adj[b.Symbol]
			if a <= 0 {
				a = 1
			}
			creates = append(creates, r.db.MarketData.Create().
				SetSymbol(b.Symbol).SetBarTime(b.Time).SetFreq("1m").
				SetOpen(b.Open).SetHigh(b.High).SetLow(b.Low).SetClose(b.Close).
				SetVolume(b.Volume).SetAmount(b.Amount).SetAdjFactor(a).SetSource(SourceAgg))
		}
		return r.db.MarketData.CreateBulk(creates...).
			OnConflictColumns(marketdata.FieldSymbol, marketdata.FieldBarTime, marketdata.FieldFreq).
			UpdateNewValues().Exec(ctx)
	})
}

// SaveDailyIfAbsent 写合成日线。M01 已写入的同日日线不覆盖。
func (r *Repo) SaveDailyIfAbsent(ctx context.Context, bars []market.DayBar) error {
	return chunks(len(bars), func(lo, hi int) error {
		creates := make([]*ent.MarketDataCreate, 0, hi-lo)
		for _, b := range bars[lo:hi] {
			a := b.Adj
			if a <= 0 {
				a = 1
			}
			c := r.db.MarketData.Create().
				SetSymbol(b.Symbol).SetBarTime(market.DateOf(b.Time)).SetFreq("1d").
				SetOpen(b.Open).SetHigh(b.High).SetLow(b.Low).SetClose(b.Close).
				SetVolume(b.Volume).SetAmount(b.Amount).SetAdjFactor(a).SetSource(SourceAgg)
			if b.PreClose > 0 {
				c.SetPreClose(b.PreClose)
			}
			creates = append(creates, c)
		}
		return r.db.MarketData.CreateBulk(creates...).
			OnConflictColumns(marketdata.FieldSymbol, marketdata.FieldBarTime, marketdata.FieldFreq).
			DoNothing().Exec(ctx)
	})
}

func (r *Repo) SaveFactors(ctx context.Context, day time.Time, kind string, rows []market.FactorRow) error {
	d := pgDay(day)
	return chunks(len(rows), func(lo, hi int) error {
		creates := make([]*ent.StockFactorCreate, 0, hi-lo)
		for _, row := range rows[lo:hi] {
			creates = append(creates, r.db.StockFactor.Create().
				SetSymbol(row.Symbol).SetTradeDate(d).SetKind(kind).SetAsOf(row.AsOf).
				SetFactors(clean(row.Values)).SetStale(row.Stale))
		}
		return r.db.StockFactor.CreateBulk(creates...).
			OnConflictColumns(stockfactor.FieldSymbol, stockfactor.FieldTradeDate, stockfactor.FieldKind, stockfactor.FieldAsOf).
			UpdateNewValues().Exec(ctx)
	})
}

func nilTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func (r *Repo) SaveBoards(ctx context.Context, boards []market.LimitBoard) error {
	return chunks(len(boards), func(lo, hi int) error {
		creates := make([]*ent.LimitBoardCreate, 0, hi-lo)
		for _, b := range boards[lo:hi] {
			creates = append(creates, r.db.LimitBoard.Create().
				SetSymbol(b.Symbol).SetTradeDate(pgDay(b.TradeDate)).SetDirection(b.Direction).
				SetStatus(b.Status).SetLimitPrice(b.LimitPrice).
				SetNillableFirstSealAt(nilTime(b.FirstSealAt)).SetNillableLastSealAt(nilTime(b.LastSealAt)).
				SetOpenCount(b.OpenCount).SetSealAmount(b.SealAmount).SetConsecutive(b.Consecutive).
				SetAsOf(b.AsOf))
		}
		return r.db.LimitBoard.CreateBulk(creates...).
			OnConflictColumns(limitboard.FieldSymbol, limitboard.FieldTradeDate, limitboard.FieldDirection).
			UpdateNewValues().Exec(ctx)
	})
}

func (r *Repo) SaveSentiment(ctx context.Context, s market.Sentiment) error {
	return r.db.MarketSentiment.Create().
		SetTradeDate(pgDay(s.TradeDate)).SetAsOf(s.AsOf).
		SetUpCount(s.UpCount).SetDownCount(s.DownCount).SetBrokenCount(s.BrokenCount).
		SetBrokenRate(s.BrokenRate).SetMaxHeight(s.MaxHeight).SetProfitEffect(s.ProfitEffect).
		SetAdvance(s.Advance).SetDecline(s.Decline).SetFlat(s.Flat).SetAmount(s.Amount).
		SetPhase(string(s.Phase)).SetScoreCoef(s.ScoreCoef).SetPositionScale(s.PositionScale).
		SetStale(s.Stale).
		OnConflictColumns(marketsentiment.FieldTradeDate, marketsentiment.FieldAsOf).
		UpdateNewValues().Exec(ctx)
}

func (r *Repo) SaveSectorHeat(ctx context.Context, day, asOf time.Time, rows []market.SectorHeat) error {
	d := pgDay(day)
	return chunks(len(rows), func(lo, hi int) error {
		creates := make([]*ent.SectorHeatCreate, 0, hi-lo)
		for _, h := range rows[lo:hi] {
			creates = append(creates, r.db.SectorHeat.Create().
				SetSectorCode(h.SectorCode).SetTradeDate(d).SetAsOf(asOf).
				SetRank(h.Rank).SetHeat(h.Heat).SetAvgPct(h.AvgPct).SetUpLimitCount(h.UpLimitCount).
				SetAdvanceRatio(h.AdvanceRatio).SetAmount(h.Amount).SetMemberCount(h.MemberCount).
				SetLeaderSymbol(h.LeaderSymbol).SetLeaderReason(h.LeaderReason).
				SetOverseasImpulse(h.OverseasImpulse))
		}
		return r.db.SectorHeat.CreateBulk(creates...).
			OnConflictColumns(sectorheat.FieldSectorCode, sectorheat.FieldTradeDate, sectorheat.FieldAsOf).
			UpdateNewValues().Exec(ctx)
	})
}

// SectorMembers 返回 day 当天有效的成分：in_date ≤ day，且 out_date 为空或晚于 day。
func (r *Repo) SectorMembers(ctx context.Context, day time.Time) (map[string][]string, map[string]string, error) {
	d := pgDay(day)
	rows, err := r.db.SectorMember.Query().Where(
		sectormember.InDateLTE(d),
		sectormember.Or(sectormember.OutDateIsNil(), sectormember.OutDateGT(d)),
	).All(ctx)
	if err != nil {
		return nil, nil, err
	}
	members := map[string][]string{}
	for _, row := range rows {
		members[row.SectorCode] = append(members[row.SectorCode], row.Symbol)
	}
	secs, err := r.db.Sector.Query().Select(sector.FieldCode, sector.FieldName).All(ctx)
	if err != nil {
		return nil, nil, err
	}
	names := make(map[string]string, len(secs))
	for _, s := range secs {
		names[s.Code] = s.Name
	}
	return members, names, nil
}

// MoneyFlows 只返回当天有资金流记录的股票，每只取截至当天最近 n 天，升序。
func (r *Repo) MoneyFlows(ctx context.Context, day time.Time, n int) (map[string][]market.Flow, error) {
	d := pgDay(day)
	rows, err := r.db.MoneyFlow.Query().Where(
		moneyflow.TradeDateGTE(d.AddDate(0, 0, -(n*7/5+14))),
		moneyflow.TradeDateLTE(d),
	).Order(moneyflow.ByTradeDate()).All(ctx)
	if err != nil {
		return nil, err
	}
	type item struct {
		day  time.Time
		flow market.Flow
	}
	by := map[string][]item{}
	for _, row := range rows {
		by[row.Symbol] = append(by[row.Symbol], item{day: row.TradeDate, flow: market.Flow{MainNet: row.MainNet}})
	}
	out := make(map[string][]market.Flow, len(by))
	for sym, items := range by {
		if !items[len(items)-1].day.UTC().Equal(d) {
			continue
		}
		if len(items) > n {
			items = items[len(items)-n:]
		}
		flows := make([]market.Flow, len(items))
		for i, it := range items {
			flows[i] = it.flow
		}
		out[sym] = flows
	}
	return out, nil
}

func (r *Repo) DayAmounts(ctx context.Context, day time.Time) (map[string]float64, error) {
	start := market.DateOf(day)
	rows, err := r.db.MarketData.Query().Where(
		marketdata.FreqEQ("1d"),
		marketdata.BarTimeGTE(start),
		marketdata.BarTimeLT(start.Add(24*time.Hour)),
	).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]float64, len(rows))
	for _, row := range rows {
		out[row.Symbol] = f64(row.Amount)
	}
	return out, nil
}

func (r *Repo) LhbSeats(ctx context.Context, day time.Time) (map[string][]market.Seat, error) {
	rows, err := r.db.LhbSeat.Query().Where(lhbseat.TradeDateEQ(pgDay(day))).All(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string][]market.Seat{}
	for _, row := range rows {
		out[row.Symbol] = append(out[row.Symbol], market.Seat{Name: row.SeatName, Buy: row.BuyAmount, Sell: row.SellAmount})
	}
	return out, nil
}

func (r *Repo) SeatTags(ctx context.Context) (map[string]string, error) {
	rows, err := r.db.SeatTag.Query().All(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		out[row.SeatName] = row.Tag
	}
	return out, nil
}

func (r *Repo) OverseasQuotes(ctx context.Context, since time.Time) ([]market.Quote, error) {
	rows, err := r.db.OverseasQuote.Query().Where(overseasquote.TradeDateGTE(pgDay(since))).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]market.Quote, 0, len(rows))
	for _, row := range rows {
		out = append(out, market.Quote{Code: row.Code, TradeDate: fromPgDay(row.TradeDate), PctChg: row.PctChg, AsOf: row.AsOf})
	}
	return out, nil
}

func (r *Repo) OverseasMappings(ctx context.Context) ([]market.Mapping, error) {
	rows, err := r.db.OverseasMapping.Query().All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]market.Mapping, 0, len(rows))
	for _, row := range rows {
		out = append(out, market.Mapping{Asset: row.AssetCode, Sector: row.SectorCode, Weight: row.Weight})
	}
	return out, nil
}

// Bars 从 end 往前取 limit 根，按时间升序返回。
func (r *Repo) Bars(ctx context.Context, symbol, freq string, start, end time.Time, limit int) ([]biz.BarRow, error) {
	q := r.db.MarketData.Query().Where(marketdata.SymbolEQ(symbol), marketdata.FreqEQ(freq))
	if !start.IsZero() {
		q = q.Where(marketdata.BarTimeGTE(start))
	}
	if !end.IsZero() {
		q = q.Where(marketdata.BarTimeLTE(end))
	}
	rows, err := q.Order(marketdata.ByBarTime(entsql.OrderDesc())).Limit(limit).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]biz.BarRow, 0, len(rows))
	for i := len(rows) - 1; i >= 0; i-- {
		row := rows[i]
		if row.Close == nil {
			continue
		}
		out = append(out, biz.BarRow{
			Bar: market.Bar{
				Symbol: row.Symbol, Time: row.BarTime.In(tradecal.Shanghai()),
				Open: f64(row.Open), High: f64(row.High), Low: f64(row.Low), Close: f64(row.Close),
				Volume: i64(row.Volume), Amount: f64(row.Amount),
			},
			Adj: row.AdjFactor, PreClose: f64(row.PreClose),
		})
	}
	return out, nil
}

// FactorsAt 按时点读截面：每只股票、每个类别取 as_of ≤ asOf 的最新一行。
func (r *Repo) FactorsAt(ctx context.Context, day time.Time, kind string, asOf time.Time, symbols []string) ([]biz.FactorRecord, error) {
	q := r.db.StockFactor.Query().Where(
		stockfactor.TradeDateEQ(pgDay(day)),
		stockfactor.AsOfLTE(asOf),
	)
	if kind != "" {
		q = q.Where(stockfactor.KindEQ(kind))
	}
	if len(symbols) > 0 {
		q = q.Where(stockfactor.SymbolIn(symbols...))
	}
	rows, err := q.Order(stockfactor.ByAsOf(entsql.OrderDesc())).All(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}
	var out []biz.FactorRecord
	for _, row := range rows {
		key := row.Symbol + "|" + row.Kind
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, biz.FactorRecord{
			FactorRow: market.FactorRow{Symbol: row.Symbol, AsOf: row.AsOf, Values: row.Factors, Stale: row.Stale},
			Kind:      row.Kind,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Symbol != out[j].Symbol {
			return out[i].Symbol < out[j].Symbol
		}
		return out[i].Kind < out[j].Kind
	})
	return out, nil
}

func (r *Repo) SentimentAt(ctx context.Context, day, asOf time.Time) (market.Sentiment, bool, error) {
	row, err := r.db.MarketSentiment.Query().Where(
		marketsentiment.TradeDateEQ(pgDay(day)),
		marketsentiment.AsOfLTE(asOf),
	).Order(marketsentiment.ByAsOf(entsql.OrderDesc())).First(ctx)
	if ent.IsNotFound(err) {
		return market.Sentiment{}, false, nil
	}
	if err != nil {
		return market.Sentiment{}, false, err
	}
	return market.Sentiment{
		TradeDate: fromPgDay(row.TradeDate), AsOf: row.AsOf,
		UpCount: row.UpCount, DownCount: row.DownCount, BrokenCount: row.BrokenCount,
		BrokenRate: row.BrokenRate, MaxHeight: row.MaxHeight, ProfitEffect: row.ProfitEffect,
		Advance: row.Advance, Decline: row.Decline, Flat: row.Flat, Amount: row.Amount,
		Phase: market.Phase(row.Phase), ScoreCoef: row.ScoreCoef, PositionScale: row.PositionScale,
		Stale: row.Stale,
	}, true, nil
}

func (r *Repo) Boards(ctx context.Context, day time.Time, direction, status string) ([]market.LimitBoard, error) {
	q := r.db.LimitBoard.Query().Where(limitboard.TradeDateEQ(pgDay(day)))
	if direction != "" {
		q = q.Where(limitboard.DirectionEQ(direction))
	}
	if status != "" {
		q = q.Where(limitboard.StatusEQ(status))
	}
	rows, err := q.Order(limitboard.BySymbol()).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]market.LimitBoard, 0, len(rows))
	for _, row := range rows {
		b := market.LimitBoard{
			Symbol: row.Symbol, TradeDate: fromPgDay(row.TradeDate), Direction: row.Direction, Status: row.Status,
			LimitPrice: row.LimitPrice, OpenCount: row.OpenCount, SealAmount: row.SealAmount,
			Consecutive: row.Consecutive, AsOf: row.AsOf,
		}
		if row.FirstSealAt != nil {
			b.FirstSealAt = *row.FirstSealAt
		}
		if row.LastSealAt != nil {
			b.LastSealAt = *row.LastSealAt
		}
		out = append(out, b)
	}
	return out, nil
}

// SectorHeatAt 取 as_of ≤ asOf 的最近一次排名。
func (r *Repo) SectorHeatAt(ctx context.Context, day, asOf time.Time, top int) ([]market.SectorHeat, error) {
	d := pgDay(day)
	latest, err := r.db.SectorHeat.Query().Where(
		sectorheat.TradeDateEQ(d), sectorheat.AsOfLTE(asOf),
	).Order(sectorheat.ByAsOf(entsql.OrderDesc())).First(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := r.db.SectorHeat.Query().Where(
		sectorheat.TradeDateEQ(d), sectorheat.AsOfEQ(latest.AsOf),
	).Order(sectorheat.ByRank()).Limit(top).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]market.SectorHeat, 0, len(rows))
	for _, h := range rows {
		out = append(out, market.SectorHeat{
			SectorCode: h.SectorCode, Rank: h.Rank, Heat: h.Heat, AvgPct: h.AvgPct,
			UpLimitCount: h.UpLimitCount, AdvanceRatio: h.AdvanceRatio, Amount: h.Amount,
			MemberCount: h.MemberCount, LeaderSymbol: h.LeaderSymbol, LeaderReason: h.LeaderReason,
			OverseasImpulse: h.OverseasImpulse,
		})
	}
	return out, nil
}
