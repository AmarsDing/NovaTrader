package biz

import (
	"context"
	"fmt"
	"sort"
	"time"

	"server/pkg/backtest"
	"server/pkg/rules"
)

// StrategyInfo 是一个可回测策略及其参数的当前值。
type StrategyInfo struct {
	Spec    rules.Spec
	Current map[string]float64
	Evolve  bool
}

// Strategies 排除测试用的 t_ 前缀策略。
func (uc *Usecase) Strategies(ctx context.Context) ([]StrategyInfo, error) {
	evolve := map[string]bool{}
	for _, n := range uc.cfg.EvolveStrategies {
		evolve[n] = true
	}
	var out []StrategyInfo
	for _, s := range rules.List() {
		if len(s.Name) > 2 && s.Name[:2] == "t_" {
			continue
		}
		cur, err := uc.CurrentParams(ctx, s)
		if err != nil {
			return nil, err
		}
		out = append(out, StrategyInfo{Spec: s, Current: cur, Evolve: evolve[s.Name]})
	}
	return out, nil
}

// PeriodRange 周以周一为始，月以自然月。
func PeriodRange(period string, day time.Time) (time.Time, time.Time, error) {
	day = backtest.Day(day)
	switch period {
	case "week", "":
		off := (int(day.Weekday()) + 6) % 7
		start := day.AddDate(0, 0, -off)
		return start, start.AddDate(0, 0, 6), nil
	case "month":
		start := time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, day.Location())
		return start, start.AddDate(0, 1, -1), nil
	}
	return time.Time{}, time.Time{}, fmt.Errorf("%w: period 只能是 week / month", ErrBadRequest)
}

type BookStat struct {
	Book        string  `json:"book"`
	Days        int     `json:"days"`
	Start       float64 `json:"start_equity"`
	End         float64 `json:"end_equity"`
	Return      float64 `json:"return"`
	MaxDrawdown float64 `json:"max_drawdown"`
}

type TaskBrief struct {
	ID          int     `json:"id"`
	Kind        string  `json:"kind"`
	Name        string  `json:"name"`
	Strategy    string  `json:"strategy"`
	Status      string  `json:"status"`
	FinishedAt  string  `json:"finished_at"`
	TotalReturn float64 `json:"total_return"`
	MaxDrawdown float64 `json:"max_drawdown"`
	Sharpe      float64 `json:"sharpe"`
}

type IntroBrief struct {
	Day         string  `json:"day"`
	SampleCount int     `json:"sample_count"`
	WinRate     float64 `json:"win_rate"`
	AvgReturn   float64 `json:"avg_return"`
	MissedCount int     `json:"missed_count"`
	Triggered   bool    `json:"triggered"`
}

type ProposalBrief struct {
	ID        int    `json:"id"`
	Strategy  string `json:"strategy"`
	Status    string `json:"status"`
	Reason    string `json:"reason"`
	CreatedAt string `json:"created_at"`
	DecidedBy string `json:"decided_by"`
}

// Period 是周报、月报，按需生成，不落表。
type Period struct {
	Introspections []IntroBrief    `json:"introspections"`
	Books          []BookStat      `json:"books"`
	Shadow         *ShadowStats    `json:"shadow"`
	Tasks          []TaskBrief     `json:"tasks"`
	Proposals      []ProposalBrief `json:"proposals"`
}

func (uc *Usecase) PeriodReport(ctx context.Context, period string, day time.Time) (time.Time, time.Time, *Period, error) {
	start, end, err := PeriodRange(period, day)
	if err != nil {
		return start, end, nil, err
	}
	out := &Period{}
	intros, err := uc.repo.Introspections(ctx, uc.cfg.Book, start, end, 0)
	if err != nil {
		return start, end, nil, err
	}
	for i := len(intros) - 1; i >= 0; i-- {
		r := intros[i]
		out.Introspections = append(out.Introspections, IntroBrief{
			Day: r.TradeDate.Format("2006-01-02"), SampleCount: r.SampleCount, WinRate: r.WinRate,
			AvgReturn: r.AvgReturn, MissedCount: r.MissedCount, Triggered: r.Triggered,
		})
	}
	for _, book := range []string{"paper", "live"} {
		eq, err := uc.repo.Equity(ctx, book, start, end)
		if err != nil {
			return start, end, nil, err
		}
		if len(eq) == 0 {
			continue
		}
		bs := BookStat{Book: book, Days: len(eq), Start: eq[0].Equity, End: eq[len(eq)-1].Equity, MaxDrawdown: drawdown(eq)}
		if bs.Start > 0 {
			bs.Return = bs.End/bs.Start - 1
		}
		out.Books = append(out.Books, bs)
	}
	shadows, err := uc.repo.Shadows(ctx, ShadowFilter{From: start, To: end.AddDate(0, 0, 1)})
	if err != nil {
		return start, end, nil, err
	}
	out.Shadow = shadowStats(shadows)
	tasks, err := uc.repo.TasksFinished(ctx, start, end.AddDate(0, 0, 1))
	if err != nil {
		return start, end, nil, err
	}
	for _, t := range tasks {
		tb := TaskBrief{ID: t.ID, Kind: t.Kind, Name: t.Name, Strategy: t.Strategy, Status: t.Status}
		if t.FinishedAt != nil {
			tb.FinishedAt = t.FinishedAt.Format(time.RFC3339)
		}
		if t.Summary != nil {
			tb.TotalReturn, tb.MaxDrawdown, tb.Sharpe = t.Summary.TotalReturn, t.Summary.MaxDrawdown, t.Summary.Sharpe
		}
		out.Tasks = append(out.Tasks, tb)
	}
	props, err := uc.repo.ProposalsBetween(ctx, start, end.AddDate(0, 0, 1))
	if err != nil {
		return start, end, nil, err
	}
	for _, p := range props {
		out.Proposals = append(out.Proposals, ProposalBrief{
			ID: p.ID, Strategy: p.Strategy, Status: p.Status, Reason: p.Reason,
			CreatedAt: p.CreatedAt.Format(time.RFC3339), DecidedBy: p.DecidedBy,
		})
	}
	return start, end, out, nil
}

type AdmissionItem struct {
	Key, Title, Status, Detail string
}

const (
	Pass    = "pass"
	Fail    = "fail"
	Unknown = "unknown"
)

// Admission 是 FR-07-11 的准入检查表。全部 pass 才算可以进入半自动实盘。
func (uc *Usecase) Admission(ctx context.Context, strategy string) ([]AdmissionItem, bool, error) {
	if _, ok := rules.Find(strategy); !ok {
		return nil, false, fmt.Errorf("%w: 策略 %q 未注册", ErrBadRequest, strategy)
	}
	c := uc.cfg
	var items []AdmissionItem
	single, err := uc.repo.LatestSucceeded(ctx, strategy, KindSingle)
	if err != nil {
		return nil, false, err
	}
	bt := AdmissionItem{Key: "backtest", Title: "回测达标"}
	la := AdmissionItem{Key: "lookahead", Title: "未来函数自检"}
	if single == nil || single.Summary == nil {
		bt.Status, bt.Detail = Fail, "没有成功的单次回测"
		la.Status, la.Detail = Fail, "没有成功的单次回测"
	} else {
		s := single.Summary
		var bad []string
		if s.Rounds < c.MinTrades {
			bad = append(bad, fmt.Sprintf("回合数 %d < %d", s.Rounds, c.MinTrades))
		}
		if s.MaxDrawdown > c.MaxDrawdown {
			bad = append(bad, fmt.Sprintf("最大回撤 %.1f%% > %.0f%%", s.MaxDrawdown*100, c.MaxDrawdown*100))
		}
		if s.AnnualReturn <= c.MinAnnual {
			bad = append(bad, fmt.Sprintf("年化 %.1f%% ≤ %.1f%%", s.AnnualReturn*100, c.MinAnnual*100))
		}
		if s.ProfitFactor < c.MinProfitFactor {
			bad = append(bad, fmt.Sprintf("盈亏比 %.2f < %.2f", s.ProfitFactor, c.MinProfitFactor))
		}
		if s.Sharpe < c.MinSharpe {
			bad = append(bad, fmt.Sprintf("夏普 %.2f < %.2f", s.Sharpe, c.MinSharpe))
		}
		ref := fmt.Sprintf("任务 #%d（%s~%s）", single.ID, single.Start.Format("2006-01-02"), single.End.Format("2006-01-02"))
		if len(bad) == 0 {
			bt.Status, bt.Detail = Pass, fmt.Sprintf("%s：回合 %d、回撤 %.1f%%、年化 %.1f%%、盈亏比 %.2f、夏普 %.2f",
				ref, s.Rounds, s.MaxDrawdown*100, s.AnnualReturn*100, s.ProfitFactor, s.Sharpe)
		} else {
			bt.Status, bt.Detail = Fail, ref+"："+join(bad)
		}
		switch {
		case s.LookaheadPass == nil:
			la.Status, la.Detail = Fail, ref+" 没做截断自检"
		case *s.LookaheadPass:
			la.Status, la.Detail = Pass, ref+" 通过"
		default:
			la.Status, la.Detail = Fail, ref+" 未通过"
		}
	}
	items = append(items, bt, la)

	oos := AdmissionItem{Key: "oos", Title: "样本外"}
	opt, err := uc.repo.LatestSucceeded(ctx, strategy, KindOptimize, KindWalkForward)
	if err != nil {
		return nil, false, err
	}
	switch {
	case opt == nil || opt.Summary == nil:
		oos.Status, oos.Detail = Fail, "没有成功的参数搜索或滚动前推任务"
	case opt.Summary.OOSDecay:
		oos.Status, oos.Detail = Fail, fmt.Sprintf("任务 #%d 标了样本外衰减", opt.ID)
	default:
		oos.Status, oos.Detail = Pass, fmt.Sprintf("任务 #%d 没有样本外衰减", opt.ID)
	}
	items = append(items, oos)

	eq, err := uc.repo.Equity(ctx, "paper", time.Time{}, uc.now())
	if err != nil {
		return nil, false, err
	}
	pd := AdmissionItem{Key: "paper_days", Title: fmt.Sprintf("模拟盘满 %d 个交易日", c.PaperDays)}
	pdd := AdmissionItem{Key: "paper_drawdown", Title: "模拟盘回撤"}
	if len(eq) >= c.PaperDays {
		pd.Status = Pass
	} else {
		pd.Status = Fail
	}
	pd.Detail = fmt.Sprintf("已有 %d 个交易日的账户快照", len(eq))
	if len(eq) == 0 {
		pdd.Status, pdd.Detail = Fail, "没有模拟盘账户快照"
	} else if dd := drawdown(eq); dd <= c.PaperMaxDrawdown {
		pdd.Status, pdd.Detail = Pass, fmt.Sprintf("最大回撤 %.1f%% ≤ %.0f%%", dd*100, c.PaperMaxDrawdown*100)
	} else {
		pdd.Status, pdd.Detail = Fail, fmt.Sprintf("最大回撤 %.1f%% > %.0f%%", dd*100, c.PaperMaxDrawdown*100)
	}
	items = append(items, pd, pdd,
		AdmissionItem{Key: "risk_drill", Title: "风控演练", Status: Unknown, Detail: "熔断、Kill Switch、行情中断、终端扫单停止：M08 尚无演练记录，需人工确认"},
		AdmissionItem{Key: "broker", Title: "东财文件单联调", Status: Unknown, Detail: "仿真账户买入、撤单、卖出各一笔：M09 尚无记录，需人工确认"},
	)
	ready := true
	for _, it := range items {
		if it.Status != Pass {
			ready = false
		}
	}
	return items, ready, nil
}

func join(s []string) string {
	sort.Strings(s)
	out := ""
	for i, x := range s {
		if i > 0 {
			out += "；"
		}
		out += x
	}
	return out
}
