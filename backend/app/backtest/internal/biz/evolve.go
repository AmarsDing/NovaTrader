package biz

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"server/pkg/backtest"
	"server/pkg/rules"
	"server/pkg/tradecal"
)

const (
	AttrIndicator = "indicator_failed"
	AttrNews      = "news_misread"
	AttrUnfilled  = "unfilled"
	AttrEmotion   = "emotion_mismatch"
	AttrMissed    = "missed"
	AttrUnknown   = "unknown"
)

// missReasons 是「排名靠前但没被选上」的原因。持仓、冷却、熔断等不算漏选。
var missReasons = map[string]bool{
	"not_in_ai_top": true, "below_threshold": true, "daily_cap": true, "ai_invalid": true,
}

// attribute 给一个亏损的入选样本归因。只用落库的事实，规则依次判断：
// 信号没成交 → unfilled；当日情绪为冰点或退潮 → emotion_mismatch；
// 利空卖出或消息面分 ≥ 70 却亏损 → news_misread；其余 → indicator_failed。
func attribute(c Candidate, phase string) string {
	switch {
	case c.SignalStatus == "expired" || c.SignalStatus == "rejected" || c.SignalStatus == "cancelled":
		return AttrUnfilled
	case phase == string(rules.StageIce) || phase == string(rules.StageFade):
		return AttrEmotion
	case c.ExitKind == rules.ExitBadNews || c.NewsDim >= 70:
		return AttrNews
	default:
		return AttrIndicator
	}
}

func (uc *Usecase) lastDays(ctx context.Context, end time.Time, n int) ([]time.Time, error) {
	days, err := uc.src.TradingDays(ctx, end.AddDate(0, 0, -(n*2+20)), end)
	if err != nil {
		return nil, err
	}
	if len(days) > n {
		days = days[len(days)-n:]
	}
	return days, nil
}

// LastClosed 是最近一个已收盘交易日。
func (uc *Usecase) LastClosed() (time.Time, error) {
	d, err := tradecal.Default.LastClosedDay(uc.now())
	return backtest.Day(d), err
}

// Introspect 统计截至 day 的近 window_days 个交易日，写 introspection 和 strategy_feedback；
// 连续 trigger_days 天胜率偏低时为在用策略建进化任务。同一天重复执行覆盖。
func (uc *Usecase) Introspect(ctx context.Context, day time.Time, book string) (*Introspection, error) {
	if book == "" {
		book = uc.cfg.Book
	}
	day = backtest.Day(day)
	days, err := uc.lastDays(ctx, day, uc.cfg.WindowDays)
	if err != nil {
		return nil, err
	}
	if len(days) == 0 {
		return nil, fmt.Errorf("%w: %s 之前没有交易日", ErrBadRequest, day.Format("2006-01-02"))
	}
	from := days[0]
	cands, err := uc.repo.Candidates(ctx, from, day)
	if err != nil {
		return nil, err
	}
	in := &Introspection{TradeDate: day, Book: book, WindowDays: len(days), Attribution: map[string]int{}}
	phases := map[string]string{}
	phaseOf := func(d time.Time) string {
		k := d.Format("2006-01-02")
		if p, ok := phases[k]; ok {
			return p
		}
		p, err := uc.repo.PhaseOn(ctx, d)
		if err != nil {
			uc.log.Warnf("phase %s: %v", k, err)
		}
		phases[k] = p
		return p
	}
	var feedback []Feedback
	sum, wins := 0.0, 0
	for _, c := range cands {
		ret := c.Ret[uc.cfg.Horizon]
		if ret == nil {
			continue
		}
		if c.Stage == "selected" {
			in.SampleCount++
			sum += *ret
			ok := *ret > 0
			fb := Feedback{TradeDate: c.TradeDate, Symbol: c.Symbol, Strategy: c.Strategy, SignalID: c.SignalID, Actual: *ret, Correct: &ok}
			if ok {
				wins++
			} else {
				fb.Attribution = attribute(c, phaseOf(c.TradeDate))
				in.Attribution[fb.Attribution]++
				fb.Analysis = fmt.Sprintf("T+%d 收益 %.2f%%", uc.cfg.Horizon, *ret*100)
			}
			feedback = append(feedback, fb)
			continue
		}
		if missReasons[c.MissReason] && *ret >= uc.cfg.MissedReturn {
			in.MissedCount++
			feedback = append(feedback, Feedback{
				TradeDate: c.TradeDate, Symbol: c.Symbol, Strategy: c.Strategy, Actual: *ret,
				FalseNegative: true, Attribution: AttrMissed,
				Analysis: fmt.Sprintf("未入选（%s），T+%d 收益 %.2f%%", c.MissReason, uc.cfg.Horizon, *ret*100),
			})
		}
	}
	if in.SampleCount > 0 {
		in.WinRate = float64(wins) / float64(in.SampleCount)
		in.AvgReturn = sum / float64(in.SampleCount)
	}
	if err := uc.repo.SaveFeedback(ctx, feedback); err != nil {
		return nil, err
	}
	eq, err := uc.repo.Equity(ctx, book, from, day)
	if err != nil {
		return nil, err
	}
	if len(eq) > 0 {
		in.MaxDrawdown = drawdown(eq)
	}
	var notes []string
	if in.SampleCount == 0 {
		notes = append(notes, fmt.Sprintf("窗口内没有带 T+%d 后验的入选样本", uc.cfg.Horizon))
	}
	saved, err := uc.repo.SaveIntrospection(ctx, in)
	if err != nil {
		return nil, err
	}
	triggered, reason, err := uc.triggered(ctx, book, day)
	if err != nil {
		return nil, err
	}
	saved.Triggered, saved.TriggerReason = triggered, reason
	if triggered {
		for _, name := range uc.cfg.EvolveStrategies {
			t, why := uc.startEvolve(ctx, name, day, saved.ID)
			if t != nil && saved.TaskID == nil {
				saved.TaskID = &t.ID
			}
			notes = append(notes, why)
		}
		if len(uc.cfg.EvolveStrategies) == 0 {
			notes = append(notes, "配置里没有 evolve_strategies，不进化")
		}
	}
	saved.Note = strings.Join(notes, "；")
	return uc.repo.SaveIntrospection(ctx, saved)
}

func drawdown(eq []backtest.EquityPoint) float64 {
	peak, dd := 0.0, 0.0
	for _, p := range eq {
		if p.Equity > peak {
			peak = p.Equity
		}
		if peak > 0 && (peak-p.Equity)/peak > dd {
			dd = (peak - p.Equity) / peak
		}
	}
	return dd
}

// triggered 判断截至 day 的最近 trigger_days 行是否都样本充足且胜率低于阈值。
func (uc *Usecase) triggered(ctx context.Context, book string, day time.Time) (bool, string, error) {
	rows, err := uc.repo.Introspections(ctx, book, time.Time{}, day, uc.cfg.TriggerDays)
	if err != nil {
		return false, "", err
	}
	if len(rows) < uc.cfg.TriggerDays {
		return false, "", nil
	}
	for _, r := range rows {
		if r.SampleCount < uc.cfg.MinSamples || r.WinRate >= uc.cfg.WinRateBelow {
			return false, "", nil
		}
	}
	return true, fmt.Sprintf("连续 %d 天胜率低于 %.0f%%（每天样本 ≥ %d）", uc.cfg.TriggerDays, uc.cfg.WinRateBelow*100, uc.cfg.MinSamples), nil
}

// CurrentParams 读策略的当前生效参数：参数表里有就用它（吸附到格点），否则用默认值。
func (uc *Usecase) CurrentParams(ctx context.Context, spec rules.Spec) (map[string]float64, error) {
	keys := make([]string, len(spec.Params))
	for i, p := range spec.Params {
		keys[i] = spec.ParamKey(p.Name)
	}
	vals, err := uc.repo.ConfigValues(ctx, keys)
	if err != nil {
		return nil, err
	}
	out := make(map[string]float64, len(spec.Params))
	for _, p := range spec.Params {
		out[p.Name] = p.Default
		if s, ok := vals[spec.ParamKey(p.Name)]; ok {
			if v, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
				out[p.Name] = p.Snap(v)
			}
		}
	}
	return out, nil
}

func (uc *Usecase) startEvolve(ctx context.Context, name string, day time.Time, introID int) (*Task, string) {
	spec, ok := rules.Find(name)
	if !ok {
		return nil, name + "：未注册"
	}
	if busy, err := uc.repo.ActiveEvolve(ctx, name); err != nil || busy {
		return nil, name + "：已有进化任务在排队或运行"
	}
	if pending, err := uc.repo.HasPendingProposal(ctx, name); err != nil || pending {
		return nil, name + "：已有待处理提案"
	}
	cur, err := uc.CurrentParams(ctx, spec)
	if err != nil {
		return nil, name + "：读参数失败 " + err.Error()
	}
	n := uc.cfg.EvolveISDays + uc.cfg.EvolveOOSDays
	days, err := uc.lastDays(ctx, day, n)
	if err != nil || len(days) < n {
		return nil, fmt.Sprintf("%s：交易日不足 %d 天", name, n)
	}
	seed, _ := strconv.ParseInt(day.Format("20060102"), 10, 64)
	cfg := backtest.DefaultConfig()
	cfg.Strategy, cfg.Params, cfg.Freq, cfg.Seed = name, cur, uc.cfg.EvolveFreq, seed
	cfg.Start, cfg.End = days[0].Format("2006-01-02"), day.Format("2006-01-02")
	ts := Spec{
		Config: cfg,
		Search: &backtest.SearchConfig{Workers: uc.cfg.SearchWorkers},
		Evolve: &EvolveSpec{
			Candidates: backtest.Neighbors(spec, cur, uc.cfg.EvolveCands, seed),
			ISDays:     uc.cfg.EvolveISDays, OOSDays: uc.cfg.EvolveOOSDays, IntrospectionID: introID,
		},
	}
	t, err := uc.enqueue(ctx, KindEvolve, fmt.Sprintf("%s 进化 %s", name, cfg.End), ts, "introspect", nil)
	if err != nil {
		return nil, name + "：建任务失败 " + err.Error()
	}
	return t, fmt.Sprintf("%s：已建进化任务 #%d（%d 组候选）", name, t.ID, len(ts.Evolve.Candidates))
}

// propose 在进化任务结束时调用：样本外更好才写提案。
func (uc *Usecase) propose(ctx context.Context, t *Task, ev *backtest.EvolveResult) error {
	if ev == nil || !ev.Improved {
		return nil
	}
	base, _ := json.Marshal(ev.BaseOOS)
	cand, _ := json.Marshal(ev.BestOOS)
	_, err := uc.repo.CreateProposal(ctx, &Proposal{
		Strategy: t.Strategy, BaseParams: ev.Base, Params: ev.Best,
		Reason: ev.Reason + "；" + changed(ev.Base, ev.Best), TaskID: &t.ID,
		BaseMetrics: base, NewMetrics: cand, Status: ProposalPending,
	})
	return err
}

func changed(a, b map[string]float64) string {
	var keys []string
	for k := range b {
		if a[k] != b[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s %v→%v", k, a[k], b[k])
	}
	return "调整 " + strings.Join(parts, "，")
}

func (uc *Usecase) Introspections(ctx context.Context, book string, limit int) ([]*Introspection, error) {
	if book == "" {
		book = uc.cfg.Book
	}
	if limit <= 0 || limit > 365 {
		limit = 60
	}
	return uc.repo.Introspections(ctx, book, time.Time{}, time.Time{}, limit)
}

func (uc *Usecase) Proposals(ctx context.Context, f ProposalFilter) ([]*Proposal, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	return uc.repo.ListProposals(ctx, f)
}

// Approve 把提案参数一次写入参数表并生成新版本。
func (uc *Usecase) Approve(ctx context.Context, id int, user string) (*Proposal, error) {
	p, err := uc.repo.GetProposal(ctx, id)
	if err != nil {
		return nil, err
	}
	if p.Status != ProposalPending {
		return nil, fmt.Errorf("%w: 提案已%s", ErrConflict, p.Status)
	}
	spec, ok := rules.Find(p.Strategy)
	if !ok {
		return nil, fmt.Errorf("%w: 策略 %s 未注册", ErrBadRequest, p.Strategy)
	}
	if _, err := spec.Resolve(p.Params); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	values := make(map[string]string, len(p.Params))
	for k, v := range p.Params {
		values[spec.ParamKey(k)] = strconv.FormatFloat(v, 'f', -1, 64)
	}
	return uc.repo.ApproveProposal(ctx, id, values, userOr(user))
}

func (uc *Usecase) Reject(ctx context.Context, id int, user, note string) (*Proposal, error) {
	return uc.repo.RejectProposal(ctx, id, userOr(user), note)
}

// Rollback 回到该策略最近一次批准前的参数版本。
func (uc *Usecase) Rollback(ctx context.Context, strategy, user string) (*Proposal, error) {
	if _, ok := rules.Find(strategy); !ok {
		return nil, fmt.Errorf("%w: 策略 %s 未注册", ErrBadRequest, strategy)
	}
	return uc.repo.RollbackProposal(ctx, strategy, userOr(user))
}

func userOr(u string) string {
	if strings.TrimSpace(u) == "" {
		return "local"
	}
	return strings.TrimSpace(u)
}
