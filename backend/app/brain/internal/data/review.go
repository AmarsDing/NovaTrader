// review.go：M05 收盘定稿与程序版复盘的 data 层落库实现。
// pool/日线/情绪截面读共享表（M01/M02），写 emotion_day_frozen、daily_reviews。
package data

import (
	"context"
	"encoding/json"
	"time"

	"server/app/brain/internal/biz"
	"server/ent"
	"server/ent/dailyreview"
	"server/ent/emotiondayfrozen"
	"server/ent/equityunlock"
	"server/ent/hsgttop10"
	"server/ent/lhbseat"
	"server/ent/limitpool"
	"server/ent/margindetail"
	"server/ent/marketdata"
	"server/ent/marketsentiment"
	"server/ent/newssentiment"
	"server/ent/predictioncheck"
	"server/ent/sourcehealth"
	"server/ent/tradesignal"
	"server/pkg/events"
	"server/pkg/outbox"
	"server/pkg/tradecal"

	entsql "entgo.io/ent/dialect/sql"
)

type reviewRepo struct {
	db *ent.Client
}

func NewReviewRepo(db *ent.Client) biz.ReviewRepo {
	return &reviewRepo{db: db}
}

func (r *reviewRepo) PoolOn(ctx context.Context, day time.Time) ([]biz.PoolRow, error) {
	rows, err := r.db.LimitPool.Query().Where(limitpool.TradeDateEQ(day)).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]biz.PoolRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, biz.PoolRow{
			Symbol:      row.Symbol,
			Name:        row.Name,
			Pool:        row.Pool,
			Consecutive: row.Consecutive,
			Reason:      row.Reason,
			OpenCount:   row.OpenCount,
			SealAmount:  row.SealAmount,
			FirstSealAt: row.FirstSealAt,
			LastSealAt:  row.LastSealAt,
		})
	}
	return out, nil
}

func (r *reviewRepo) BarOn(ctx context.Context, symbols []string, day time.Time) (map[string]biz.TierBar, error) {
	out := map[string]biz.TierBar{}
	if len(symbols) == 0 {
		return out, nil
	}
	// 日线 bar_time 各源时戳不一，按当日区间取，每股取最新一根。
	rows, err := r.db.MarketData.Query().
		Where(
			marketdata.SymbolIn(symbols...),
			marketdata.FreqEQ("1d"),
			marketdata.BarTimeGTE(day),
			marketdata.BarTimeLT(day.Add(24*time.Hour)),
			marketdata.CloseNotNil(),
		).
		Order(marketdata.ByBarTime(entsql.OrderDesc())).
		All(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if _, ok := out[row.Symbol]; ok {
			continue
		}
		out[row.Symbol] = biz.TierBar{Close: deref(row.Close), PreClose: deref(row.PreClose)}
	}
	return out, nil
}

// UpsertFrozen 按 trade_date 冲突覆盖：重跑时新取到的块覆盖旧值，没取到的块保留旧值。
func (r *reviewRepo) UpsertFrozen(ctx context.Context, f *biz.Frozen) error {
	ladder, err := jsonAny(f.Ladder)
	if err != nil {
		return err
	}
	firstBoard, err := jsonAny(f.FirstBoard)
	if err != nil {
		return err
	}
	themeTree, err := jsonAny(f.ThemeTree)
	if err != nil {
		return err
	}
	prevTier, err := jsonAny(f.PrevTier)
	if err != nil {
		return err
	}
	sealQ, err := jsonAny(f.SealQ)
	if err != nil {
		return err
	}
	c := r.db.EmotionDayFrozen.Create().
		SetTradeDate(f.Day).
		SetNillableUpCount(f.UpCount).
		SetNillableDownCount(f.DownCount).
		SetNillableBrokenCount(f.BrokenCount).
		SetNillableSealRate(f.SealRate).
		SetNillableMaxHeight(f.MaxHeight).
		SetNillablePromo12(f.Promo12).
		SetNillablePromo23(f.Promo23).
		SetNillablePromoHigh(f.PromoHigh).
		SetNillableTierAvg(f.TierAvg).
		SetNillableTierMedian(f.TierMedian).
		SetNillableTierRedRate(f.TierRedRate).
		SetNillableTierRelimit(f.TierRelimit).
		SetPartial(f.Partial).
		SetMissing(f.Missing).
		SetSource(f.Source).
		SetCapturedAt(time.Now())
	if ladder != nil {
		c.SetLadder(ladder.(map[string]any))
	}
	if firstBoard != nil {
		c.SetFirstBoard(firstBoard.([]any))
	}
	if themeTree != nil {
		c.SetThemeTree(themeTree.([]any))
	}
	if prevTier != nil {
		c.SetPrevTier(prevTier.([]any))
	}
	if sealQ != nil {
		c.SetSealQuality(sealQ.(map[string]any))
	}
	if f.StructureGaps != nil {
		c.SetStructureGaps(f.StructureGaps)
	}
	return c.OnConflictColumns(emotiondayfrozen.FieldTradeDate).UpdateNewValues().Exec(ctx)
}

func (r *reviewRepo) FrozenOn(ctx context.Context, day time.Time) (*biz.Frozen, error) {
	row, err := r.db.EmotionDayFrozen.Query().Where(emotiondayfrozen.TradeDateEQ(day)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	f := &biz.Frozen{
		Day:           day,
		UpCount:       row.UpCount,
		DownCount:     row.DownCount,
		BrokenCount:   row.BrokenCount,
		SealRate:      row.SealRate,
		MaxHeight:     row.MaxHeight,
		Promo12:       row.Promo12,
		Promo23:       row.Promo23,
		PromoHigh:     row.PromoHigh,
		TierAvg:       row.TierAvg,
		TierMedian:    row.TierMedian,
		TierRedRate:   row.TierRedRate,
		TierRelimit:   row.TierRelimit,
		StructureGaps: row.StructureGaps,
		Partial:       row.Partial,
		Missing:       row.Missing,
		Source:        row.Source,
		CapturedAt:    row.CapturedAt,
	}
	if err := jsonBack(row.Ladder, &f.Ladder); err != nil {
		return nil, err
	}
	if err := jsonBack(row.FirstBoard, &f.FirstBoard); err != nil {
		return nil, err
	}
	if err := jsonBack(row.ThemeTree, &f.ThemeTree); err != nil {
		return nil, err
	}
	if err := jsonBack(row.PrevTier, &f.PrevTier); err != nil {
		return nil, err
	}
	var sq biz.SealQuality
	if len(row.SealQuality) > 0 {
		if err := jsonBack(row.SealQuality, &sq); err != nil {
			return nil, err
		}
		f.SealQ = &sq
	}
	return f, nil
}

func (r *reviewRepo) SentimentOn(ctx context.Context, day time.Time) (*biz.SentimentRow, error) {
	row, err := r.db.MarketSentiment.Query().
		Where(marketsentiment.TradeDateEQ(day)).
		Order(marketsentiment.ByAsOf(entsql.OrderDesc())).
		First(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &biz.SentimentRow{AsOf: row.AsOf, Advance: row.Advance, Decline: row.Decline, Amount: row.Amount}, nil
}

func (r *reviewRepo) SentimentAmounts(ctx context.Context, day time.Time, n int) ([]float64, error) {
	rows, err := r.db.MarketSentiment.Query().
		Where(marketsentiment.TradeDateLTE(day)).
		Order(marketsentiment.ByTradeDate(entsql.OrderDesc()), marketsentiment.ByAsOf(entsql.OrderDesc())).
		Limit(n).
		All(ctx)
	if err != nil {
		return nil, err
	}
	// 同一天可能多条截面，取最新一条，再按日升序返回。
	byDate := map[string]*ent.MarketSentiment{}
	dates := make([]string, 0)
	for _, row := range rows {
		key := row.TradeDate.Format("2006-01-02")
		if _, ok := byDate[key]; !ok {
			byDate[key] = row
			dates = append(dates, key)
		}
	}
	amounts := make([]float64, 0, len(dates))
	for i := len(dates) - 1; i >= 0; i-- {
		amounts = append(amounts, byDate[dates[i]].Amount)
	}
	return amounts, nil
}

// MarginTotal 全市场融资融券余额合计（sum(rzrqye)）。没有当日记录返回 false。
func (r *reviewRepo) MarginTotal(ctx context.Context, day time.Time) (float64, bool, error) {
	ids, err := r.db.MarginDetail.Query().
		Where(margindetail.TradeDateEQ(day)).
		Limit(1).
		IDs(ctx)
	if err != nil {
		return 0, false, err
	}
	if len(ids) == 0 {
		return 0, false, nil
	}
	sum, err := r.db.MarginDetail.Query().
		Where(margindetail.TradeDateEQ(day)).
		Aggregate(ent.Sum(margindetail.FieldRzrqye)).
		Float64(ctx)
	if err != nil {
		return 0, false, err
	}
	return sum, true, nil
}

func (r *reviewRepo) SaveProgramReview(ctx context.Context, day time.Time, content string, stats map[string]any) error {
	return r.saveReview(ctx, day, "program", content, stats, nil, "")
}

// SaveReview 写 daily_reviews(任意 kind) —— P1-7 分段 AI 复盘；同日同 kind 覆盖（与程序版同一语义）。
func (r *reviewRepo) SaveReview(ctx context.Context, day time.Time, kind, content string, stats map[string]any, lessons []string, model string) error {
	return r.saveReview(ctx, day, kind, content, stats, lessons, model)
}

// strategyReviewEvent 是 strategy.review 主题的载荷（M10 通知与桌面推送消费，见 pkg/events）。
type strategyReviewEvent struct {
	Date  string `json:"date"`
	Title string `json:"title"`
	Body  string `json:"body"`
}

// SaveReviewWithEvent 与 SaveReview 同语义，但在同一事务里插一条 outbox 事件（P1-7b 周报 → strategy.review）。
func (r *reviewRepo) SaveReviewWithEvent(ctx context.Context, day time.Time, kind, content string, stats map[string]any, lessons []string, model, traceID, title string) error {
	return r.saveWithEvent(ctx, day, kind, content, stats, lessons, model, traceID, title)
}

// SaveProgramReviewWithEvent 程序版日复盘的 outbox 变体（M13-T07②）：落库与事件同一事务提交。
func (r *reviewRepo) SaveProgramReviewWithEvent(ctx context.Context, day time.Time, content string, stats map[string]any, traceID, title string) error {
	return r.saveWithEvent(ctx, day, "program", content, stats, nil, "", traceID, title)
}

// saveWithEvent 落一行复盘并在同一事务里插 strategy.review outbox 事件。
func (r *reviewRepo) saveWithEvent(ctx context.Context, day time.Time, kind, content string, stats map[string]any, lessons []string, model, traceID, title string) error {
	env, err := events.New("brain", events.SubjectStrategyReview, traceID, strategyReviewEvent{
		Date: day.Format("2006-01-02"), Title: title, Body: content,
	})
	if err != nil {
		return err
	}
	// 复盘链号由 traceID（决策链）持有，这里只补父信封因果，不改链号。
	env = events.StitchWithParent(ctx, env)
	tx, err := r.db.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := saveReviewTx(ctx, tx.DailyReview, day, kind, content, stats, lessons, model); err != nil {
		return err
	}
	if err := outbox.Insert(ctx, tx, env); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *reviewRepo) saveReview(ctx context.Context, day time.Time, kind, content string, stats map[string]any, lessons []string, model string) error {
	return saveReviewTx(ctx, r.db.DailyReview, day, kind, content, stats, lessons, model)
}

// saveReviewTx 落一行复盘；c 可以是主连接的或事务内的 DailyReviewClient。
func saveReviewTx(ctx context.Context, c *ent.DailyReviewClient, day time.Time, kind, content string, stats map[string]any, lessons []string, model string) error {
	now := time.Now()
	u := c.Update().
		Where(dailyreview.DateEQ(day), dailyreview.KindEQ(kind)).
		SetReviewContent(content).
		SetAccuracyStats(stats).
		SetCreatedAt(now)
	if lessons != nil {
		u.SetLessonsLearned(lessons)
	}
	if model != "" {
		u.SetModel(model)
	}
	n, err := u.Save(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	cr := c.Create().
		SetDate(day).
		SetKind(kind).
		SetReviewContent(content).
		SetAccuracyStats(stats).
		SetCreatedAt(now)
	if lessons != nil {
		cr.SetLessonsLearned(lessons)
	}
	if model != "" {
		cr.SetModel(model)
	}
	return cr.Exec(ctx)
}

// ReviewOn 查某日某 kind 复盘。found=false 表示没有该行。
func (r *reviewRepo) ReviewOn(ctx context.Context, day time.Time, kind string) (string, map[string]any, []string, string, bool, error) {
	row, err := r.db.DailyReview.Query().
		Where(dailyreview.DateEQ(day), dailyreview.KindEQ(kind)).
		Only(ctx)
	if ent.IsNotFound(err) {
		return "", nil, nil, "", false, nil
	}
	if err != nil {
		return "", nil, nil, "", false, err
	}
	content := ""
	if row.ReviewContent != nil {
		content = *row.ReviewContent
	}
	return content, row.AccuracyStats, row.LessonsLearned, row.Model, true, nil
}

// HsgtTop10 北向十大成交（龙虎榜段替代读数）。没有当日行返回空切片。
func (r *reviewRepo) HsgtTop10(ctx context.Context, day time.Time) ([]biz.NorthTop, error) {
	rows, err := r.db.HsgtTop10.Query().
		Where(hsgttop10.TradeDateEQ(day)).
		Order(hsgttop10.ByChannel(), hsgttop10.ByRank()).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]biz.NorthTop, 0, len(rows))
	for _, row := range rows {
		out = append(out, biz.NorthTop{
			Symbol: row.Symbol, Name: row.Name, Channel: row.Channel, Rank: row.Rank,
			Net: row.NetAmount, PctChg: row.PctChg, Amount: row.Amount,
		})
	}
	return out, nil
}

// LhbSeatsOn 当日龙虎榜席位明细，并上 seat_tag 画像。没有当日行返回空切片（P2-8）。
func (r *reviewRepo) LhbSeatsOn(ctx context.Context, day time.Time) ([]biz.LhbSeatRow, error) {
	rows, err := r.db.LhbSeat.Query().Where(lhbseat.TradeDateEQ(day)).All(ctx)
	if err != nil {
		return nil, err
	}
	tags, err := r.db.SeatTag.Query().All(ctx)
	if err != nil {
		return nil, err
	}
	profile := map[string]struct{ tag, alias string }{}
	for _, t := range tags {
		profile[t.SeatName] = struct{ tag, alias string }{t.Tag, t.Alias}
	}
	out := make([]biz.LhbSeatRow, 0, len(rows))
	for _, row := range rows {
		item := biz.LhbSeatRow{
			Symbol: row.Symbol, Reason: row.Reason, SeatName: row.SeatName,
			Buy: row.BuyAmount, Sell: row.SellAmount,
		}
		if p, ok := profile[row.SeatName]; ok {
			item.Tag, item.Alias = p.tag, p.alias
		}
		out = append(out, item)
	}
	return out, nil
}

// UnlockWindow [from, to] 窗口内解禁批次，按 free_date 升序。没有返回空切片（P2-8）。
func (r *reviewRepo) UnlockWindow(ctx context.Context, from, to time.Time) ([]biz.UnlockRow, error) {
	rows, err := r.db.EquityUnlock.Query().
		Where(equityunlock.FreeDateGTE(from), equityunlock.FreeDateLTE(to)).
		Order(equityunlock.ByFreeDate()).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]biz.UnlockRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, biz.UnlockRow{
			Symbol: row.Symbol, FreeDate: row.FreeDate, LiftType: row.LiftType,
			Shares: row.Shares, BatchHolderNum: row.BatchHolderNum,
			RatioPct: row.RatioPct, TotalRatioPct: row.TotalRatioPct, MarketValue: row.MarketValue,
		})
	}
	return out, nil
}

// UnlockFresh 解禁域最近 days 个自然日内是否采集成功过（P2-8，没取到≠0 的判定依据）。
func (r *reviewRepo) UnlockFresh(ctx context.Context, day time.Time, days int) (bool, error) {
	return r.db.SourceHealth.Query().
		Where(
			sourcehealth.DomainEQ("unlock"),
			sourcehealth.LastSuccessAtNotNil(),
			sourcehealth.LastSuccessAtGTE(day.AddDate(0, 0, -days)),
		).
		Exist(ctx)
}

// HoldingDecreaseRecent [from, to] 内减持公告情报，按重要度、发布时间倒序取前 n 条（P2-8）。
func (r *reviewRepo) HoldingDecreaseRecent(ctx context.Context, from, to time.Time, n int) ([]biz.Intel, error) {
	rows, err := r.db.NewsSentiment.Query().
		Where(
			newssentiment.EventTypeEQ("holding_decrease"),
			newssentiment.PublishTimeNotNil(),
			newssentiment.PublishTimeGTE(from),
			newssentiment.PublishTimeLTE(to),
		).
		Order(newssentiment.ByImportance(entsql.OrderDesc()), newssentiment.ByPublishTime(entsql.OrderDesc())).
		Limit(n).
		All(ctx)
	if err != nil {
		return nil, err
	}
	return toIntel(rows), nil
}

// UpsertPredictionCheck 按 (check_date, source) 冲突覆盖。
func (r *reviewRepo) UpsertPredictionCheck(ctx context.Context, c *biz.Check) error {
	cands, err := jsonAny(c.Candidates)
	if err != nil {
		return err
	}
	results, err := jsonAny(c.Results)
	if err != nil {
		return err
	}
	uc := r.db.PredictionCheck.Create().
		SetSignalDate(c.SignalDate).
		SetCheckDate(c.CheckDate).
		SetSource(c.Source).
		SetNillableCandidateCount(c.CandidateCount).
		SetNillableHitCount(c.HitCount).
		SetNillableRelimitCount(c.RelimitCount).
		SetNillableHitRate(c.HitRate).
		SetNillableAvgPct(c.AvgPct).
		SetNillableMaxPct(c.MaxPct).
		SetPartial(c.Partial).
		SetMissing(c.Missing).
		SetComputedAt(time.Now())
	if cands != nil {
		uc.SetCandidates(cands.([]any))
	}
	if results != nil {
		uc.SetResults(results.([]any))
	}
	return uc.OnConflictColumns(predictioncheck.FieldCheckDate, predictioncheck.FieldSource).UpdateNewValues().Exec(ctx)
}

func (r *reviewRepo) PredictionChecks(ctx context.Context, n int) ([]*biz.Check, error) {
	rows, err := r.db.PredictionCheck.Query().
		Order(predictioncheck.ByCheckDate(entsql.OrderDesc())).
		Limit(n).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*biz.Check, 0, len(rows))
	for _, row := range rows {
		c, err := checkFromRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

func (r *reviewRepo) PredictionCheckOn(ctx context.Context, checkDay time.Time, source string) (*biz.Check, error) {
	row, err := r.db.PredictionCheck.Query().
		Where(predictioncheck.CheckDateEQ(checkDay), predictioncheck.SourceEQ(source)).
		Only(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return checkFromRow(row)
}

func checkFromRow(row *ent.PredictionCheck) (*biz.Check, error) {
	c := &biz.Check{
		SignalDate:     row.SignalDate,
		CheckDate:      row.CheckDate,
		Source:         row.Source,
		CandidateCount: row.CandidateCount,
		HitCount:       row.HitCount,
		RelimitCount:   row.RelimitCount,
		HitRate:        row.HitRate,
		AvgPct:         row.AvgPct,
		MaxPct:         row.MaxPct,
		Partial:        row.Partial,
		Missing:        row.Missing,
	}
	if err := jsonBack(row.Candidates, &c.Candidates); err != nil {
		return nil, err
	}
	if err := jsonBack(row.Results, &c.Results); err != nil {
		return nil, err
	}
	return c, nil
}

// jsonAny 把 biz 结构体转成 jsonb 可存的 map/slice。
func jsonAny(v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// jsonBack 把 jsonb 值解回 biz 结构。
func jsonBack(src any, dst any) error {
	if src == nil {
		return nil
	}
	raw, err := json.Marshal(src)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, dst)
}

// SignalsOnDays 到期终态买入信号（M13-T08）。trade_date 为 NULL 的行按 signal_time 的
// 上海日期回退匹配 days 集合（数据纪律：显式回退，不猜 0）。没有返回空切片。
func (r *reviewRepo) SignalsOnDays(ctx context.Context, days []time.Time) ([]biz.SignalRow, error) {
	out := []biz.SignalRow{}
	if len(days) == 0 {
		return out, nil
	}
	minDay, maxDay := days[0], days[0]
	wanted := map[string]bool{}
	for _, d := range days {
		if d.Before(minDay) {
			minDay = d
		}
		if d.After(maxDay) {
			maxDay = d
		}
		wanted[shanghaiDate(d).Format("2006-01-02")] = true
	}
	rows, err := r.db.TradeSignal.Query().
		Where(
			tradesignal.StatusIn("done", "closed"),
			tradesignal.SignalTypeEQ(tradesignal.SignalTypeBuy),
			tradesignal.Or(
				tradesignal.TradeDateIn(days...),
				tradesignal.And(
					tradesignal.TradeDateIsNil(),
					tradesignal.SignalTimeGTE(minDay),
					tradesignal.SignalTimeLT(maxDay.Add(24*time.Hour)),
				),
			),
		).
		All(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		eff := row.TradeDate
		var day time.Time
		if eff != nil {
			day = shanghaiDate(*eff)
		} else {
			day = shanghaiDate(row.SignalTime)
		}
		if !wanted[day.Format("2006-01-02")] {
			continue
		}
		out = append(out, biz.SignalRow{
			ID: int64(row.ID), StockCode: row.StockCode, StockName: row.StockName, Strategy: row.Strategy,
			SignalTime: row.SignalTime, TradeDate: row.TradeDate,
			EntryPrice: row.EntryPrice, StopLoss: row.StopLoss, TakeProfit: row.TakeProfit,
			EntryLow: row.EntryLow, EntryHigh: row.EntryHigh, Status: row.Status,
		})
	}
	return out, nil
}

// shanghaiDate 归一到上海时区的日期零点（trade_date 是 date 类型，signal_time 是 timestamptz）。
func shanghaiDate(t time.Time) time.Time {
	t = t.In(tradecal.Shanghai())
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, tradecal.Shanghai())
}
