package data

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"server/app/backtest/internal/biz"
	"server/ent"
	"server/ent/accountsnapshot"
	"server/ent/agentdecision"
	"server/ent/backtestreport"
	"server/ent/backtesttask"
	"server/ent/backtesttrade"
	"server/ent/introspection"
	"server/ent/marketdata"
	"server/ent/marketsentiment"
	"server/ent/paramproposal"
	"server/ent/shadowsignal"
	"server/ent/strategycandidate"
	"server/ent/strategyconfig"
	"server/ent/strategyfeedback"
	"server/ent/tradesignal"
	"server/pkg/backtest"
	"server/pkg/params"
)

// Repo 实现 biz.Repo。
type Repo struct{ client *ent.Client }

func NewRepo(client *ent.Client) *Repo { return &Repo{client: client} }

func missing(err error) error {
	if ent.IsNotFound(err) {
		return biz.ErrNotFound
	}
	return err
}

func (r *Repo) CreateTask(ctx context.Context, t *biz.Task) (*biz.Task, error) {
	raw, err := json.Marshal(t.Spec)
	if err != nil {
		return nil, err
	}
	row, err := r.client.BacktestTask.Create().
		SetKind(t.Kind).SetName(t.Name).SetStrategy(t.Strategy).SetFreq(t.Freq).
		SetStartDate(t.Start).SetEndDate(t.End).SetConfig(raw).SetStatus(t.Status).
		SetParamsHash(t.ParamsHash).SetEngineVersion(t.EngineVersion).SetBuildVersion(t.BuildVersion).
		SetSeed(t.Seed).SetNillableRerunOf(t.RerunOf).SetCreatedBy(t.CreatedBy).
		Save(ctx)
	if err != nil {
		return nil, err
	}
	return toTask(row, nil), nil
}

func (r *Repo) GetTask(ctx context.Context, id int) (*biz.Task, error) {
	row, err := r.client.BacktestTask.Get(ctx, id)
	if err != nil {
		return nil, missing(err)
	}
	sum, err := r.summaries(ctx, []int{id})
	if err != nil {
		return nil, err
	}
	return toTask(row, sum[id]), nil
}

func (r *Repo) ListTasks(ctx context.Context, f biz.TaskFilter) ([]*biz.Task, int, error) {
	q := r.client.BacktestTask.Query()
	q = filterTasks(q, f)
	total, err := q.Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := filterTasks(r.client.BacktestTask.Query(), f).
		Order(ent.Desc(backtesttask.FieldID)).
		Limit(f.Limit).Offset(f.Offset).
		All(ctx)
	if err != nil {
		return nil, 0, err
	}
	ids := make([]int, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}
	sum, err := r.summaries(ctx, ids)
	if err != nil {
		return nil, 0, err
	}
	out := make([]*biz.Task, len(rows))
	for i, row := range rows {
		out[i] = toTask(row, sum[row.ID])
	}
	return out, total, nil
}

func filterTasks(q *ent.BacktestTaskQuery, f biz.TaskFilter) *ent.BacktestTaskQuery {
	if f.Status != "" {
		q = q.Where(backtesttask.StatusEQ(f.Status))
	}
	if f.Strategy != "" {
		q = q.Where(backtesttask.StrategyEQ(f.Strategy))
	}
	if f.Kind != "" {
		q = q.Where(backtesttask.KindEQ(f.Kind))
	}
	return q
}

func (r *Repo) summaries(ctx context.Context, ids []int) (map[int]*biz.Summary, error) {
	out := map[int]*biz.Summary{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := r.client.BacktestReport.Query().Where(backtestreport.TaskIDIn(ids...)).All(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		s := &biz.Summary{
			TotalReturn: row.TotalReturn, AnnualReturn: row.AnnualReturn, MaxDrawdown: row.MaxDrawdown,
			Sharpe: row.Sharpe, WinRate: row.WinRate, ProfitFactor: row.ProfitFactor, Rounds: row.TradeCount,
			LookaheadPass: row.LookaheadPass, OOSDecay: row.OosDecay, FillsHash: row.FillsHash,
		}
		out[row.TaskID] = s
	}
	return out, nil
}

func toTask(row *ent.BacktestTask, sum *biz.Summary) *biz.Task {
	t := &biz.Task{
		ID: row.ID, Kind: row.Kind, Name: row.Name, Strategy: row.Strategy, Freq: row.Freq,
		Start: backtest.Day(row.StartDate), End: backtest.Day(row.EndDate),
		Status: row.Status, Progress: row.Progress, Message: row.Message, Error: row.Error,
		ParamsHash: row.ParamsHash, EngineVersion: row.EngineVersion, BuildVersion: row.BuildVersion,
		Seed: row.Seed, DataHash: row.DataHash, RerunOf: row.RerunOf, CreatedBy: row.CreatedBy,
		CreatedAt: row.CreatedAt, StartedAt: row.StartedAt, FinishedAt: row.FinishedAt, Summary: sum,
	}
	if row.DataCutoff != nil {
		d := backtest.Day(*row.DataCutoff)
		t.DataCutoff = &d
	}
	_ = json.Unmarshal(row.Config, &t.Spec)
	return t
}

// ClaimTask 认领最早的排队任务。条件更新保证只有一个工作协程成功。
func (r *Repo) ClaimTask(ctx context.Context) (*biz.Task, error) {
	for {
		row, err := r.client.BacktestTask.Query().
			Where(backtesttask.StatusEQ(biz.StatusQueued)).
			Order(ent.Asc(backtesttask.FieldID)).
			First(ctx)
		if ent.IsNotFound(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		n, err := r.client.BacktestTask.Update().
			Where(backtesttask.IDEQ(row.ID), backtesttask.StatusEQ(biz.StatusQueued)).
			SetStatus(biz.StatusRunning).SetStartedAt(time.Now()).SetMessage("运行中").
			Save(ctx)
		if err != nil {
			return nil, err
		}
		if n == 1 {
			return r.GetTask(ctx, row.ID)
		}
	}
}

func (r *Repo) ProgressTask(ctx context.Context, id int, progress float64, msg string) error {
	_, err := r.client.BacktestTask.Update().
		Where(backtesttask.IDEQ(id), backtesttask.StatusEQ(biz.StatusRunning)).
		SetProgress(progress).SetMessage(trunc(msg, 256)).
		Save(ctx)
	return err
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func (r *Repo) CancelQueued(ctx context.Context, id int) (bool, error) {
	n, err := r.client.BacktestTask.Update().
		Where(backtesttask.IDEQ(id), backtesttask.StatusEQ(biz.StatusQueued)).
		SetStatus(biz.StatusCancelled).SetMessage("已取消").SetFinishedAt(time.Now()).
		Save(ctx)
	return n == 1, err
}

func (r *Repo) FinishTask(ctx context.Context, id int, status, msg, errText string) error {
	n, err := r.client.BacktestTask.Update().
		Where(backtesttask.IDEQ(id), backtesttask.StatusEQ(biz.StatusRunning)).
		SetStatus(status).SetMessage(trunc(msg, 256)).SetError(errText).SetFinishedAt(time.Now()).
		Save(ctx)
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("backtest: task %d is not running", id)
	}
	return nil
}

func (r *Repo) FailRunning(ctx context.Context, msg string) (int, error) {
	return r.client.BacktestTask.Update().
		Where(backtesttask.StatusEQ(biz.StatusRunning)).
		SetStatus(biz.StatusFailed).SetError(msg).SetMessage(trunc(msg, 256)).SetFinishedAt(time.Now()).
		Save(ctx)
}

func (r *Repo) ActiveEvolve(ctx context.Context, strategy string) (bool, error) {
	return r.client.BacktestTask.Query().
		Where(backtesttask.StrategyEQ(strategy), backtesttask.KindEQ(biz.KindEvolve),
			backtesttask.StatusIn(biz.StatusQueued, biz.StatusRunning)).
		Exist(ctx)
}

// SaveResult 在一个事务里写成交、报告，并把任务置为成功。
func (r *Repo) SaveResult(ctx context.Context, id int, rep *backtest.Report, trades []backtest.Trade, sum biz.Summary) error {
	raw, err := json.Marshal(rep)
	if err != nil {
		return err
	}
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.BacktestTrade.Delete().Where(backtesttrade.TaskIDEQ(id)).Exec(ctx); err != nil {
		return err
	}
	for i := 0; i < len(trades); i += 500 {
		j := i + 500
		if j > len(trades) {
			j = len(trades)
		}
		bulk := make([]*ent.BacktestTradeCreate, 0, j-i)
		for _, t := range trades[i:j] {
			bulk = append(bulk, tx.BacktestTrade.Create().
				SetTaskID(id).SetSeq(t.Seq).SetSymbol(t.Symbol).SetSide(t.Side).
				SetTradeTime(t.Time).SetPrice(t.Price).SetQuantity(t.Qty).
				SetAmount(t.Amount).SetFee(t.Fee).SetReason(trunc(t.Reason, 256)).
				SetPnl(t.PnL).SetRoundID(t.RoundID))
		}
		if err := tx.BacktestTrade.CreateBulk(bulk...).Exec(ctx); err != nil {
			return err
		}
	}
	rc := tx.BacktestReport.Create().
		SetTaskID(id).
		SetTotalReturn(sum.TotalReturn).SetAnnualReturn(sum.AnnualReturn).SetMaxDrawdown(sum.MaxDrawdown).
		SetSharpe(sum.Sharpe).SetWinRate(sum.WinRate).SetProfitFactor(sum.ProfitFactor).
		SetTradeCount(sum.Rounds).SetNillableLookaheadPass(sum.LookaheadPass).SetOosDecay(sum.OOSDecay).
		SetFillsHash(sum.FillsHash).SetResult(raw)
	if err := rc.OnConflictColumns(backtestreport.FieldTaskID).
		UpdateNewValues().Exec(ctx); err != nil {
		return err
	}
	u := tx.BacktestTask.Update().
		Where(backtesttask.IDEQ(id), backtesttask.StatusEQ(biz.StatusRunning)).
		SetStatus(biz.StatusSucceeded).SetProgress(1).SetMessage("完成").SetError("").
		SetDataHash(rep.DataHash).SetFinishedAt(time.Now())
	if !rep.DataCutoff.IsZero() {
		u.SetDataCutoff(backtest.Day(rep.DataCutoff))
	}
	n, err := u.Save(ctx)
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("backtest: task %d is not running", id)
	}
	return tx.Commit()
}

func (r *Repo) Report(ctx context.Context, taskID int) (json.RawMessage, error) {
	row, err := r.client.BacktestReport.Query().Where(backtestreport.TaskIDEQ(taskID)).Only(ctx)
	if err != nil {
		return nil, missing(err)
	}
	return row.Result, nil
}

func (r *Repo) Trades(ctx context.Context, f biz.TradeFilter) ([]backtest.Trade, int, error) {
	q := r.client.BacktestTrade.Query().Where(backtesttrade.TaskIDEQ(f.TaskID))
	if f.Symbol != "" {
		q = q.Where(backtesttrade.SymbolEQ(f.Symbol))
	}
	total, err := q.Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	q = r.client.BacktestTrade.Query().Where(backtesttrade.TaskIDEQ(f.TaskID))
	if f.Symbol != "" {
		q = q.Where(backtesttrade.SymbolEQ(f.Symbol))
	}
	rows, err := q.Order(ent.Asc(backtesttrade.FieldSeq)).Limit(f.Limit).Offset(f.Offset).All(ctx)
	if err != nil {
		return nil, 0, err
	}
	out := make([]backtest.Trade, len(rows))
	for i, row := range rows {
		out[i] = backtest.Trade{
			Seq: row.Seq, Symbol: row.Symbol, Side: row.Side, Time: row.TradeTime,
			Price: row.Price, Qty: row.Quantity, Amount: row.Amount, Fee: row.Fee,
			Reason: row.Reason, PnL: row.Pnl, RoundID: row.RoundID,
		}
	}
	return out, total, nil
}

func (r *Repo) LatestSucceeded(ctx context.Context, strategy string, kinds ...string) (*biz.Task, error) {
	row, err := r.client.BacktestTask.Query().
		Where(backtesttask.StrategyEQ(strategy), backtesttask.KindIn(kinds...), backtesttask.StatusEQ(biz.StatusSucceeded)).
		Order(ent.Desc(backtesttask.FieldFinishedAt)).
		First(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return r.GetTask(ctx, row.ID)
}

func (r *Repo) TasksFinished(ctx context.Context, from, to time.Time) ([]*biz.Task, error) {
	rows, err := r.client.BacktestTask.Query().
		Where(backtesttask.FinishedAtGTE(from), backtesttask.FinishedAtLT(to)).
		Order(ent.Asc(backtesttask.FieldFinishedAt)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*biz.Task, len(rows))
	for i, row := range rows {
		out[i] = toTask(row, nil)
	}
	return out, nil
}

func (r *Repo) MaxShadowDecision(ctx context.Context) (int, error) {
	row, err := r.client.ShadowSignal.Query().Order(ent.Desc(shadowsignal.FieldDecisionID)).First(ctx)
	if ent.IsNotFound(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return row.DecisionID, nil
}

func (r *Repo) NewDecisions(ctx context.Context, afterID, limit int) ([]biz.Shadow, error) {
	rows, err := r.client.AgentDecision.Query().
		Where(agentdecision.IDGT(afterID), agentdecision.SymbolNEQ(""), agentdecision.Discarded(false)).
		Order(ent.Asc(agentdecision.FieldID)).
		Limit(limit).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]biz.Shadow, len(rows))
	for i, row := range rows {
		kind := ""
		if row.DecisionType != nil {
			kind = *row.DecisionType
		}
		out[i] = biz.Shadow{
			DecisionID: row.ID, Symbol: row.Symbol, Model: row.Model, PromptVersion: row.PromptVersion,
			DecisionType: kind, Score: row.Confidence, DecidedAt: row.CreatedAt, Status: "pending",
		}
	}
	return out, nil
}

func (r *Repo) InsertShadows(ctx context.Context, rows []biz.Shadow) error {
	for _, s := range rows {
		if err := r.client.ShadowSignal.Create().
			SetDecisionID(s.DecisionID).SetSymbol(s.Symbol).SetModel(s.Model).
			SetPromptVersion(s.PromptVersion).SetDecisionType(s.DecisionType).
			SetNillableScore(s.Score).SetDecidedAt(s.DecidedAt).SetStatus(s.Status).
			OnConflictColumns(shadowsignal.FieldDecisionID).DoNothing().
			Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repo) OpenShadows(ctx context.Context) ([]biz.Shadow, error) {
	rows, err := r.client.ShadowSignal.Query().
		Where(shadowsignal.StatusNEQ("done")).
		Order(ent.Asc(shadowsignal.FieldID)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	return toShadows(rows), nil
}

func (r *Repo) UpdateShadow(ctx context.Context, s biz.Shadow) error {
	return r.client.ShadowSignal.UpdateOneID(s.ID).
		SetNillableRefDate(s.RefDate).SetNillableRefPrice(s.RefPrice).
		SetNillableRetT1(s.RetT1).SetNillableRetT3(s.RetT3).SetNillableRetT5(s.RetT5).
		SetStatus(s.Status).
		Exec(ctx)
}

func (r *Repo) Shadows(ctx context.Context, f biz.ShadowFilter) ([]biz.Shadow, error) {
	q := r.client.ShadowSignal.Query()
	if f.Model != "" {
		q = q.Where(shadowsignal.ModelEQ(f.Model))
	}
	if f.PromptVersion != "" {
		q = q.Where(shadowsignal.PromptVersionEQ(f.PromptVersion))
	}
	if !f.From.IsZero() {
		q = q.Where(shadowsignal.DecidedAtGTE(f.From))
	}
	if !f.To.IsZero() {
		q = q.Where(shadowsignal.DecidedAtLT(f.To))
	}
	rows, err := q.Order(ent.Asc(shadowsignal.FieldDecidedAt)).All(ctx)
	if err != nil {
		return nil, err
	}
	return toShadows(rows), nil
}

func toShadows(rows []*ent.ShadowSignal) []biz.Shadow {
	out := make([]biz.Shadow, len(rows))
	for i, row := range rows {
		s := biz.Shadow{
			ID: row.ID, DecisionID: row.DecisionID, Symbol: row.Symbol, Model: row.Model,
			PromptVersion: row.PromptVersion, DecisionType: row.DecisionType, Score: row.Score,
			DecidedAt: row.DecidedAt, RefPrice: row.RefPrice, RetT1: row.RetT1, RetT3: row.RetT3, RetT5: row.RetT5,
			Status: row.Status,
		}
		if row.RefDate != nil {
			d := backtest.Day(*row.RefDate)
			s.RefDate = &d
		}
		out[i] = s
	}
	return out
}

func (r *Repo) DayBars(ctx context.Context, symbol string, from, to time.Time) ([]backtest.DayBar, error) {
	var rows []barRow
	if err := r.client.MarketData.Query().
		Where(marketdata.SymbolEQ(symbol), marketdata.FreqEQ(backtest.Freq1d),
			marketdata.BarTimeGTE(backtest.Day(from)), marketdata.BarTimeLT(backtest.Day(to).AddDate(0, 0, 1))).
		Order(ent.Asc(marketdata.FieldBarTime)).
		Select(barFields...).
		Scan(ctx, &rows); err != nil {
		return nil, err
	}
	out := make([]backtest.DayBar, 0, len(rows))
	for _, row := range rows {
		if !row.ok() {
			continue
		}
		out = append(out, backtest.DayBar{
			Day: backtest.Day(row.BarTime), Open: *row.Open, High: *row.High, Low: *row.Low, Close: *row.Close,
			Volume: val(row.Volume), Amount: val(row.Amount), Adj: row.Adj, PreClose: val(row.PreClose),
		})
	}
	return out, nil
}

func (r *Repo) Candidates(ctx context.Context, from, to time.Time) ([]biz.Candidate, error) {
	rows, err := r.client.StrategyCandidate.Query().
		Where(strategycandidate.TradeDateGTE(from), strategycandidate.TradeDateLTE(to)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	var ids []int
	for _, row := range rows {
		if row.SignalID != nil {
			ids = append(ids, *row.SignalID)
		}
	}
	sigs := map[int]*ent.TradeSignal{}
	if len(ids) > 0 {
		got, err := r.client.TradeSignal.Query().Where(tradesignal.IDIn(ids...)).All(ctx)
		if err != nil {
			return nil, err
		}
		for _, s := range got {
			sigs[s.ID] = s
		}
	}
	out := make([]biz.Candidate, len(rows))
	for i, row := range rows {
		c := biz.Candidate{
			TradeDate: backtest.Day(row.TradeDate), Strategy: row.Strategy, Symbol: row.StockCode,
			Stage: row.Stage, MissReason: row.MissReason, SignalID: row.SignalID,
			Ret: map[int]*float64{1: row.RetT1, 3: row.RetT3, 5: row.RetT5},
		}
		if row.SignalID != nil {
			if s := sigs[*row.SignalID]; s != nil {
				c.SignalStatus, c.ExitKind = s.Status, s.ExitKind
				c.NewsDim = s.Dims["news"]
			}
		}
		out[i] = c
	}
	return out, nil
}

func (r *Repo) PhaseOn(ctx context.Context, day time.Time) (string, error) {
	row, err := r.client.MarketSentiment.Query().
		Where(marketsentiment.TradeDateEQ(backtest.Day(day)), marketsentiment.Stale(false)).
		Order(ent.Desc(marketsentiment.FieldAsOf)).
		First(ctx)
	if ent.IsNotFound(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return row.Phase, nil
}

func (r *Repo) SaveFeedback(ctx context.Context, rows []biz.Feedback) error {
	if len(rows) == 0 {
		return nil
	}
	from, to := rows[0].TradeDate, rows[0].TradeDate
	for _, row := range rows {
		if row.TradeDate.Before(from) {
			from = row.TradeDate
		}
		if row.TradeDate.After(to) {
			to = row.TradeDate
		}
	}
	old, err := r.client.StrategyFeedback.Query().
		Where(strategyfeedback.TradeDateGTE(from), strategyfeedback.TradeDateLTE(to)).
		All(ctx)
	if err != nil {
		return err
	}
	have := map[string]*ent.StrategyFeedback{}
	for _, row := range old {
		have[feedbackKey(backtest.Day(row.TradeDate), row.StockCode, row.Strategy, row.SignalID)] = row
	}
	for _, row := range rows {
		key := feedbackKey(row.TradeDate, row.Symbol, row.Strategy, row.SignalID)
		if cur := have[key]; cur != nil {
			u := r.client.StrategyFeedback.UpdateOneID(cur.ID).
				SetActualReturn(row.Actual).SetFalseNegative(row.FalseNegative).
				SetAttribution(row.Attribution).SetStrategy(row.Strategy)
			if row.Correct != nil {
				u.SetSignalCorrect(*row.Correct)
			}
			if row.Analysis != "" {
				u.SetErrorAnalysis(row.Analysis)
			}
			if err := u.Exec(ctx); err != nil {
				return err
			}
			continue
		}
		c := r.client.StrategyFeedback.Create().
			SetTradeDate(row.TradeDate).SetStockCode(row.Symbol).SetStrategy(row.Strategy).
			SetNillableSignalID(row.SignalID).SetPredictedReturn(0).SetActualReturn(row.Actual).
			SetNillableSignalCorrect(row.Correct).SetFalseNegative(row.FalseNegative).
			SetAttribution(row.Attribution)
		if row.Analysis != "" {
			c.SetErrorAnalysis(row.Analysis)
		}
		if err := c.Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}

func feedbackKey(day time.Time, code, strategy string, signal *int) string {
	id := 0
	if signal != nil {
		id = *signal
	}
	return fmt.Sprintf("%s|%s|%s|%d", day.Format("2006-01-02"), code, strategy, id)
}

func (r *Repo) Equity(ctx context.Context, book string, from, to time.Time) ([]backtest.EquityPoint, error) {
	q := r.client.AccountSnapshot.Query().Where(accountsnapshot.BookEQ(book))
	if !from.IsZero() {
		q = q.Where(accountsnapshot.SnapshotTimeGTE(from))
	}
	if !to.IsZero() {
		q = q.Where(accountsnapshot.SnapshotTimeLT(to.AddDate(0, 0, 1)))
	}
	rows, err := q.Order(ent.Asc(accountsnapshot.FieldSnapshotTime)).All(ctx)
	if err != nil {
		return nil, err
	}
	var out []backtest.EquityPoint
	for _, row := range rows {
		d := backtest.Day(row.SnapshotTime)
		p := backtest.EquityPoint{Day: d, Equity: row.TotalEquity}
		if n := len(out); n > 0 && out[n-1].Day.Equal(d) {
			out[n-1] = p
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

func (r *Repo) SaveIntrospection(ctx context.Context, in *biz.Introspection) (*biz.Introspection, error) {
	if in.Attribution == nil {
		in.Attribution = map[string]int{}
	}
	row, err := r.client.Introspection.Query().
		Where(introspection.TradeDateEQ(in.TradeDate), introspection.BookEQ(in.Book)).
		Only(ctx)
	if ent.IsNotFound(err) {
		row, err = r.client.Introspection.Create().
			SetTradeDate(in.TradeDate).SetBook(in.Book).SetWindowDays(in.WindowDays).
			SetSampleCount(in.SampleCount).SetWinRate(in.WinRate).SetAvgReturn(in.AvgReturn).
			SetMaxDrawdown(in.MaxDrawdown).SetMissedCount(in.MissedCount).SetAttribution(in.Attribution).
			SetTriggered(in.Triggered).SetTriggerReason(in.TriggerReason).SetNillableTaskID(in.TaskID).SetNote(in.Note).
			Save(ctx)
	} else if err == nil {
		row, err = r.client.Introspection.UpdateOneID(row.ID).
			SetWindowDays(in.WindowDays).SetSampleCount(in.SampleCount).SetWinRate(in.WinRate).SetAvgReturn(in.AvgReturn).
			SetMaxDrawdown(in.MaxDrawdown).SetMissedCount(in.MissedCount).SetAttribution(in.Attribution).
			SetTriggered(in.Triggered).SetTriggerReason(in.TriggerReason).SetNillableTaskID(in.TaskID).SetNote(in.Note).
			Save(ctx)
	}
	if err != nil {
		return nil, err
	}
	return toIntro(row), nil
}

func (r *Repo) Introspections(ctx context.Context, book string, from, to time.Time, limit int) ([]*biz.Introspection, error) {
	q := r.client.Introspection.Query().Where(introspection.BookEQ(book))
	if !from.IsZero() {
		q = q.Where(introspection.TradeDateGTE(from))
	}
	if !to.IsZero() {
		q = q.Where(introspection.TradeDateLTE(to))
	}
	q = q.Order(ent.Desc(introspection.FieldTradeDate))
	if limit > 0 {
		q = q.Limit(limit)
	}
	rows, err := q.All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*biz.Introspection, len(rows))
	for i, row := range rows {
		out[i] = toIntro(row)
	}
	return out, nil
}

func toIntro(row *ent.Introspection) *biz.Introspection {
	return &biz.Introspection{
		ID: row.ID, TradeDate: backtest.Day(row.TradeDate), Book: row.Book, WindowDays: row.WindowDays,
		SampleCount: row.SampleCount, WinRate: row.WinRate, AvgReturn: row.AvgReturn, MaxDrawdown: row.MaxDrawdown,
		MissedCount: row.MissedCount, Attribution: row.Attribution, Triggered: row.Triggered,
		TriggerReason: row.TriggerReason, TaskID: row.TaskID, Note: row.Note, CreatedAt: row.CreatedAt,
	}
}

func (r *Repo) ConfigValues(ctx context.Context, keys []string) (map[string]string, error) {
	out := map[string]string{}
	if len(keys) == 0 {
		return out, nil
	}
	rows, err := r.client.StrategyConfig.Query().Where(strategyconfig.ConfigKeyIn(keys...)).All(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.ConfigKey] = row.ConfigValue
	}
	return out, nil
}

func (r *Repo) CreateProposal(ctx context.Context, p *biz.Proposal) (*biz.Proposal, error) {
	row, err := r.client.ParamProposal.Create().
		SetStrategy(p.Strategy).SetBaseParams(p.BaseParams).SetParams(p.Params).SetReason(p.Reason).
		SetNillableTaskID(p.TaskID).SetBaseMetrics(p.BaseMetrics).SetNewMetrics(p.NewMetrics).SetStatus(p.Status).
		Save(ctx)
	if err != nil {
		return nil, err
	}
	return toProposal(row), nil
}

func (r *Repo) GetProposal(ctx context.Context, id int) (*biz.Proposal, error) {
	row, err := r.client.ParamProposal.Get(ctx, id)
	if err != nil {
		return nil, missing(err)
	}
	return toProposal(row), nil
}

func (r *Repo) ListProposals(ctx context.Context, f biz.ProposalFilter) ([]*biz.Proposal, error) {
	q := r.client.ParamProposal.Query()
	if f.Status != "" {
		q = q.Where(paramproposal.StatusEQ(f.Status))
	}
	if f.Strategy != "" {
		q = q.Where(paramproposal.StrategyEQ(f.Strategy))
	}
	rows, err := q.Order(ent.Desc(paramproposal.FieldID)).Limit(f.Limit).All(ctx)
	if err != nil {
		return nil, err
	}
	return toProposals(rows), nil
}

func (r *Repo) ProposalsBetween(ctx context.Context, from, to time.Time) ([]*biz.Proposal, error) {
	rows, err := r.client.ParamProposal.Query().
		Where(paramproposal.Or(
			paramproposal.And(paramproposal.CreatedAtGTE(from), paramproposal.CreatedAtLT(to)),
			paramproposal.And(paramproposal.DecidedAtGTE(from), paramproposal.DecidedAtLT(to)),
		)).
		Order(ent.Asc(paramproposal.FieldID)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	return toProposals(rows), nil
}

func (r *Repo) HasPendingProposal(ctx context.Context, strategy string) (bool, error) {
	return r.client.ParamProposal.Query().
		Where(paramproposal.StrategyEQ(strategy), paramproposal.StatusEQ(biz.ProposalPending)).
		Exist(ctx)
}

func (r *Repo) ApproveProposal(ctx context.Context, id int, values map[string]string, user string) (*biz.Proposal, error) {
	newID, prevID, err := params.PutMany(ctx, r.client, values, "float", "M07 参数进化提案")
	if err != nil {
		return nil, err
	}
	n, err := r.client.ParamProposal.Update().
		Where(paramproposal.IDEQ(id), paramproposal.StatusEQ(biz.ProposalPending)).
		SetStatus(biz.ProposalApproved).SetVersionID(newID).SetPrevVersionID(prevID).
		SetDecidedBy(user).SetDecidedAt(time.Now()).
		Save(ctx)
	if err != nil || n == 0 {
		_ = params.Rollback(ctx, r.client, prevID)
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("%w: 提案 %d 已不是待处理", biz.ErrConflict, id)
	}
	return r.GetProposal(ctx, id)
}

func (r *Repo) RejectProposal(ctx context.Context, id int, user, note string) (*biz.Proposal, error) {
	u := r.client.ParamProposal.Update().
		Where(paramproposal.IDEQ(id), paramproposal.StatusEQ(biz.ProposalPending)).
		SetStatus(biz.ProposalRejected).SetDecidedBy(user).SetDecidedAt(time.Now())
	if note != "" {
		u.SetReason(note)
	}
	n, err := u.Save(ctx)
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, fmt.Errorf("%w: 提案 %d 已不是待处理", biz.ErrConflict, id)
	}
	return r.GetProposal(ctx, id)
}

func (r *Repo) RollbackProposal(ctx context.Context, strategy, user string) (*biz.Proposal, error) {
	row, err := r.client.ParamProposal.Query().
		Where(paramproposal.StrategyEQ(strategy), paramproposal.StatusEQ(biz.ProposalApproved)).
		Order(ent.Desc(paramproposal.FieldDecidedAt)).
		First(ctx)
	if ent.IsNotFound(err) {
		return nil, fmt.Errorf("%w: %s 没有可回滚的已批准提案", biz.ErrNotFound, strategy)
	}
	if err != nil {
		return nil, err
	}
	if row.PrevVersionID == nil {
		return nil, fmt.Errorf("%w: 提案 %d 没有上一版", biz.ErrConflict, row.ID)
	}
	if err := params.Rollback(ctx, r.client, *row.PrevVersionID); err != nil {
		return nil, err
	}
	if err := r.client.ParamProposal.UpdateOneID(row.ID).
		SetStatus(biz.ProposalRolledBack).SetDecidedBy(user).SetDecidedAt(time.Now()).
		Exec(ctx); err != nil {
		return nil, err
	}
	return r.GetProposal(ctx, row.ID)
}

func toProposals(rows []*ent.ParamProposal) []*biz.Proposal {
	out := make([]*biz.Proposal, len(rows))
	for i, row := range rows {
		out[i] = toProposal(row)
	}
	return out
}

func toProposal(row *ent.ParamProposal) *biz.Proposal {
	return &biz.Proposal{
		ID: row.ID, Strategy: row.Strategy, BaseParams: row.BaseParams, Params: row.Params, Reason: row.Reason,
		TaskID: row.TaskID, BaseMetrics: row.BaseMetrics, NewMetrics: row.NewMetrics, Status: row.Status,
		VersionID: row.VersionID, PrevVersionID: row.PrevVersionID, DecidedBy: row.DecidedBy,
		CreatedAt: row.CreatedAt, DecidedAt: row.DecidedAt,
	}
}
