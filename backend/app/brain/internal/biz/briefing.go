package biz

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"server/pkg/llm"
	"server/pkg/tradecal"
)

const (
	briefingMinImportance = 3
	briefingMaxNews       = 30
	briefingTimeout       = 5 * time.Minute
)

func dayOf(t time.Time) time.Time {
	t = t.In(tradecal.Shanghai())
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, tradecal.Shanghai())
}

// prevTradingDay 往前找最近的交易日。日历缺年份时退回前一个自然日。
func (uc *Usecase) prevTradingDay(day time.Time) time.Time {
	for i := 1; i <= 30; i++ {
		d := day.AddDate(0, 0, -i)
		open, err := uc.cal.Open(d)
		if err != nil {
			return day.AddDate(0, 0, -1)
		}
		if open {
			return d
		}
	}
	return day.AddDate(0, 0, -1)
}

func holdingFacts(hs []Holding) []Fact {
	var out []Fact
	for _, h := range hs {
		if h.Quantity <= 0 {
			continue
		}
		p := "F:pos." + h.Book + "." + h.Symbol + "."
		name := h.Symbol + "(" + h.Book + ")"
		out = append(out, Fact{ID: p + "qty", Label: name + " 持仓", Value: float64(h.Quantity), Unit: "股", Dim: Capital})
		if h.AvgCost != nil {
			out = append(out, Fact{ID: p + "avg_cost", Label: name + " 成本价", Value: round(*h.AvgCost, 3), Unit: "元", Dim: Capital})
		}
		if h.Price != nil {
			out = append(out, Fact{ID: p + "price", Label: name + " 现价", Value: round(*h.Price, 2), Unit: "元", Dim: Capital})
		}
		if h.PnL != nil {
			out = append(out, Fact{ID: p + "pnl", Label: name + " 浮动盈亏", Value: round(*h.PnL, 2), Unit: "元", Dim: Capital})
		}
	}
	return out
}

// GenerateBriefing 生成某日盘前晨报（FR-05-06），5 分钟内必有结果：
// 模型不可用或两次不合格时写降级晨报，只列情报标题和持仓。同一天再生成会覆盖。
func (uc *Usecase) GenerateBriefing(ctx context.Context, day time.Time) (*Briefing, error) {
	ctx, cancel := context.WithTimeout(ctx, briefingTimeout)
	defer cancel()
	day = dayOf(day)
	prev := uc.prevTradingDay(day)
	since := time.Date(prev.Year(), prev.Month(), prev.Day(), 15, 0, 0, 0, tradecal.Shanghai())
	until := time.Date(day.Year(), day.Month(), day.Day(), 9, 15, 0, 0, tradecal.Shanghai())
	if now := uc.now(); now.Before(until) {
		until = now
	}
	if !until.After(since) {
		return nil, badRequest("%s 的晨报时间窗为空", day.Format("2006-01-02"))
	}
	trace := newTrace("")
	id, err := uc.decisions.Begin(ctx, "briefing", "", trace)
	if err != nil {
		return nil, fmt.Errorf("brain: begin decision: %w", err)
	}
	start := uc.now()
	news, err := uc.market.MarketNews(ctx, since, until, briefingMinImportance, briefingMaxNews)
	if err != nil {
		return nil, fmt.Errorf("brain: briefing news: %w", err)
	}
	holdings, err := uc.market.Holdings(ctx)
	if err != nil {
		return nil, fmt.Errorf("brain: briefing holdings: %w", err)
	}
	pack := FactPack{AsOf: until, Facts: holdingFacts(holdings), Intel: news}
	b := &Briefing{Date: day, DecisionID: id, News: newsLines(news)}

	known, held := map[string]bool{}, map[string]bool{}
	for _, n := range news {
		if n.Symbol != "" {
			known[n.Symbol] = true
		}
	}
	for _, h := range holdings {
		if h.Quantity > 0 {
			known[h.Symbol], held[h.Symbol] = true, true
		}
	}

	var model string
	if len(pack.Facts) == 0 && len(pack.Intel) == 0 {
		uc.degradeBriefing(b, holdings, "上一交易日收盘以来没有重要情报，也没有持仓")
	} else {
		user := renderPack(&pack)
		ids := pack.ids()
		nums := newNumberSet(pack.Facts, briefingPrompt.System, user)
		out, resp, err := callJSON(ctx, uc, callSpec{
			task: "briefing", prompt: briefingPrompt, prio: llm.Briefing, trace: trace, decision: id, user: user,
		}, func(o *briefOut) error { return o.check(ids, nums, known, held) })
		model = resp.Model
		switch {
		case err == nil:
			b.Headline, b.MarketView, b.Risks, b.Watchlist, b.Positions = out.Headline, out.MarketView, out.Risks, out.Watchlist, out.Positions
		case errors.Is(err, llm.ErrInvalid):
			uc.degradeBriefing(b, holdings, "模型输出两次不合格："+invalidReason(err))
		default:
			uc.degradeBriefing(b, holdings, "模型不可用："+err.Error())
		}
	}
	b.CreatedAt = uc.now()

	pctx, pcancel := persistCtx(ctx)
	defer pcancel()
	if err := uc.briefings.Save(pctx, b); err != nil {
		return nil, fmt.Errorf("brain: save briefing: %w", err)
	}
	if err := uc.decisions.Finish(pctx, id, DecisionRecord{
		Kind: "briefing", TraceID: trace,
		Content:       toMap(map[string]any{"pack": pack, "result": b}),
		PromptVersion: briefingPrompt.Tag(), Model: model,
		Degraded: b.Degraded, LatencyMS: int(uc.now().Sub(start).Milliseconds()),
	}); err != nil {
		return nil, fmt.Errorf("brain: finish decision: %w", err)
	}
	return b, nil
}

func (uc *Usecase) degradeBriefing(b *Briefing, holdings []Holding, reason string) {
	b.Degraded = true
	b.Reason = reason
	b.Headline = "模型未参与，以下为原始情报与持仓"
	b.MarketView = ""
	b.Risks, b.Watchlist = nil, nil
	b.Positions = nil
	for _, h := range holdings {
		if h.Quantity <= 0 {
			continue
		}
		p := "F:pos." + h.Book + "." + h.Symbol + "."
		view := fmt.Sprintf("%s 持仓 %d 股", h.Book, h.Quantity)
		ev := []string{p + "qty"}
		if h.PnL != nil {
			view += "，浮动盈亏 " + formatValue(round(*h.PnL, 2)) + " 元"
			ev = append(ev, p+"pnl")
		}
		b.Positions = append(b.Positions, PositionView{Symbol: h.Symbol, View: view, Evidence: ev})
	}
	uc.log.Warnf("briefing %s degraded: %s", b.Date.Format("2006-01-02"), reason)
}

func newsLines(news []Intel) []NewsLine {
	out := make([]NewsLine, 0, len(news))
	for _, n := range news {
		out = append(out, NewsLine{ID: n.ID, Title: n.Title, Importance: n.Importance, Symbol: n.Symbol})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Importance > out[j].Importance })
	return out
}

func (uc *Usecase) GetBriefing(ctx context.Context, day time.Time) (*Briefing, error) {
	b, err := uc.briefings.Get(ctx, dayOf(day))
	if err != nil {
		return nil, err
	}
	if b == nil {
		return nil, fmt.Errorf("%w: %s 没有晨报", ErrNotFound, dayOf(day).Format("2006-01-02"))
	}
	return b, nil
}

// ParseDay 解析 YYYY-MM-DD，空串为今天（上海时间）。
func (uc *Usecase) ParseDay(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return dayOf(uc.now()), nil
	}
	d, err := time.ParseInLocation("2006-01-02", s, tradecal.Shanghai())
	if err != nil {
		return time.Time{}, badRequest("日期 %q 应为 YYYY-MM-DD", s)
	}
	return d, nil
}
