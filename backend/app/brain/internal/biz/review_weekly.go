package biz

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"server/pkg/llm"
	"server/pkg/observe"
)

// review_weekly.go：AI 周报（P1-7b）。
// 窗口 = day 所在 ISO 周（周一至周日）内不晚于 day 的全部交易日（至多 5 天）；
// 行落在该 ISO 周最后一个交易日（common 情形下即周五，遇节假日提前到最后一个开市日），
// 本周内任意一天手动重跑都写同一行，同日同 kind 覆盖。读出口也按同一规则对齐，
// 前端选本周任意一天都看到同一份周报。
// 读数 = 各日收盘定稿 + 情绪截面；逐日一行 + 周聚合（均值/极值/20 日均比/题材归并）组成事实包，
// 单次模型会话出总评+教训，数字过三档核对（P1-5）；任何取数缺口进 missing，不落 0。
// 落库 daily_reviews(kind='ai_week') 与 outbox 事件 strategy.review 同一事务，衔接 M10 通知与桌面推送。
// 「只在本周最后交易日自动生成」的判定在 core 调度（IsWeekLastOpenDay），手动重跑不受限。

// reviewWeekKind 是 daily_reviews 中 AI 周报的 kind。
const reviewWeekKind = "ai_week"

// weekLastOpenDay 返回 day 所在 ISO 周的最后一个交易日。
// day 不是交易日时先回退到上一交易日（选周末日期也落在本周的周报上）。
func (uc *Usecase) weekLastOpenDay(day time.Time) (time.Time, error) {
	day = shDate(day)
	open, err := uc.cal.Open(day)
	if err != nil {
		return time.Time{}, err
	}
	if !open {
		if day, err = uc.cal.PrevOpen(day); err != nil {
			return time.Time{}, err
		}
	}
	dy, dw := day.ISOWeek()
	d := day
	for i := 0; i < 6; i++ {
		next, err := uc.cal.NextOpen(d)
		if err != nil {
			return time.Time{}, err
		}
		ny, nw := next.ISOWeek()
		if ny != dy || nw != dw {
			return d, nil
		}
		d = next
	}
	return time.Time{}, fmt.Errorf("brain: %s 所在 ISO 周交易日超过 6 天", day.Format("2006-01-02"))
}

// IsWeekLastOpenDay 判断 day 是否本周最后一个交易日；core 调度用它决定要不要生成周报。
func (uc *Usecase) IsWeekLastOpenDay(day time.Time) (bool, error) {
	end, err := uc.weekLastOpenDay(day)
	if err != nil {
		return false, err
	}
	return end.Equal(shDate(day)), nil
}

// weekOpenDays 返回 day 所在 ISO 周内、不晚于 day 的交易日（升序，至多 5 天）。day 须是交易日。
func (uc *Usecase) weekOpenDays(day time.Time) ([]time.Time, error) {
	dy, dw := day.ISOWeek()
	out := []time.Time{day}
	d := day
	for len(out) < 5 {
		prev, err := uc.cal.PrevOpen(d)
		if err != nil {
			return nil, err
		}
		py, pw := prev.ISOWeek()
		if py != dy || pw != dw {
			break
		}
		out = append([]time.Time{prev}, out...)
		d = prev
	}
	return out, nil
}

// weekDayFacts 一个交易日喂给周报的原料：定稿与情绪截面都可缺（缺了记 missing，不补 0）。
type weekDayFacts struct {
	day    time.Time
	frozen *Frozen
	senti  *SentimentRow
}

// brief 输出该日一行读数；两块都没取到时返回空串（该日已由调用方计入 missing）。
func (r weekDayFacts) brief() string {
	var parts []string
	if f := r.frozen; f != nil {
		var em []string
		if f.UpCount != nil {
			em = append(em, fmt.Sprintf("涨停 %d 家", *f.UpCount))
		}
		if f.DownCount != nil {
			em = append(em, fmt.Sprintf("跌停 %d 家", *f.DownCount))
		}
		if f.SealRate != nil {
			em = append(em, fmt.Sprintf("封板率 %s%%", formatValue(*f.SealRate)))
		}
		if f.MaxHeight != nil {
			em = append(em, fmt.Sprintf("最高 %d 板", *f.MaxHeight))
		}
		if f.Promo12 != nil {
			em = append(em, fmt.Sprintf("1进2晋级率 %s%%", formatValue(*f.Promo12)))
		}
		if len(em) > 0 {
			parts = append(parts, strings.Join(em, "、"))
		}
	}
	if s := r.senti; s != nil {
		parts = append(parts, fmt.Sprintf("成交 %s 亿、涨 %d 家/跌 %d 家", formatValue(r2(s.Amount/1e8)), s.Advance, s.Decline))
	}
	return strings.Join(parts, "；")
}

// weekAggregateFacts 周内聚合读数：均值只对有数据的日集合求，凑不齐的聚合计 missing，不落 0。
func (uc *Usecase) weekAggregateFacts(ctx context.Context, rows []weekDayFacts, day time.Time, missingSet map[string]bool) []Fact {
	facts := []Fact{{ID: "F:week.days", Label: "本周已复盘交易日数", Value: float64(len(rows)), Unit: "天", Dim: External}}
	var ups, seals, promos, amounts []float64
	height, hasHeight := 0, false
	for _, r := range rows {
		if r.frozen != nil {
			if r.frozen.UpCount != nil {
				ups = append(ups, float64(*r.frozen.UpCount))
			}
			if r.frozen.SealRate != nil {
				seals = append(seals, *r.frozen.SealRate)
			}
			if r.frozen.Promo12 != nil {
				promos = append(promos, *r.frozen.Promo12)
			}
			if r.frozen.MaxHeight != nil {
				hasHeight = true
				height = max(height, *r.frozen.MaxHeight)
			}
		}
		if r.senti != nil {
			amounts = append(amounts, r.senti.Amount)
		}
	}
	if len(ups) > 0 {
		facts = append(facts, Fact{ID: "F:week.up.avg", Label: "日均涨停家数", Value: r2(meanOf(ups)), Unit: "家", Dim: External})
	} else {
		missingSet["周涨停均值"] = true
	}
	if len(seals) > 0 {
		facts = append(facts, Fact{ID: "F:week.seal.avg", Label: "平均封板率", Value: r2(meanOf(seals)), Unit: "%", Dim: External})
	} else {
		missingSet["周封板率均值"] = true
	}
	if len(promos) > 0 {
		facts = append(facts, Fact{ID: "F:week.promo12.avg", Label: "日均1进2晋级率", Value: r2(meanOf(promos)), Unit: "%", Dim: External})
	} else {
		missingSet["周晋级率均值"] = true
	}
	if hasHeight {
		facts = append(facts, Fact{ID: "F:week.height.max", Label: "周内最高连板", Value: float64(height), Unit: "板", Dim: External})
	} else {
		missingSet["周内最高连板"] = true
	}
	if len(amounts) > 0 {
		wavg := meanOf(amounts)
		facts = append(facts, Fact{ID: "F:week.amount.avg", Label: "周日均成交额", Value: r2(wavg / 1e8), Unit: "亿", Dim: Capital})
		if hist, err := uc.reviews.SentimentAmounts(ctx, day, 25); err == nil && len(hist) >= 20 {
			if a20 := meanOf(hist[len(hist)-20:]); a20 > 0 {
				facts = append(facts, Fact{ID: "F:week.amount.r20", Label: "周日均成交/近20日均值", Value: r2(wavg / a20), Dim: Capital})
			} else {
				missingSet["amount_avg20"] = true
			}
		} else {
			missingSet["amount_avg20"] = true
		}
	} else {
		missingSet["week_amount"] = true
	}
	return facts
}

// weekThemeFacts 周内题材归并：同一题材跨天合并（上榜天数、累计涨停数、周内最高板），取前三。
func weekThemeFacts(rows []weekDayFacts) ([]Fact, bool) {
	type agg struct {
		days, total, height int
	}
	aggs := map[string]*agg{}
	for _, r := range rows {
		if r.frozen == nil {
			continue
		}
		seen := map[string]bool{}
		for _, t := range r.frozen.ThemeTree {
			a := aggs[t.Reason]
			if a == nil {
				a = &agg{}
				aggs[t.Reason] = a
			}
			if !seen[t.Reason] {
				a.days++
				seen[t.Reason] = true
			}
			a.total += t.Count
			a.height = max(a.height, t.MaxHeight)
		}
	}
	if len(aggs) == 0 {
		return nil, false
	}
	reasons := make([]string, 0, len(aggs))
	for reason := range aggs {
		reasons = append(reasons, reason)
	}
	sort.Slice(reasons, func(i, j int) bool {
		if aggs[reasons[i]].total != aggs[reasons[j]].total {
			return aggs[reasons[i]].total > aggs[reasons[j]].total
		}
		return reasons[i] < reasons[j]
	})
	var facts []Fact
	for i, reason := range reasons[:min(len(reasons), 3)] {
		a := aggs[reason]
		facts = append(facts, Fact{
			ID: fmt.Sprintf("F:week.theme.%d", i+1), Label: fmt.Sprintf("周内题材%d %s", i+1, reason),
			Text: fmt.Sprintf("周内 %d 天上榜，累计 %d 家涨停，最高 %d 板", a.days, a.total, a.height),
			Dim:  External,
		})
	}
	return facts, true
}

func missingList(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// fallbackLessons 周报降级时从逐日读数拼教训（前两行，各行截到 80 字）。
func fallbackLessons(briefs []string) []string {
	out := make([]string, 0, 2)
	for _, b := range briefs {
		if len(out) >= 2 {
			break
		}
		out = append(out, clipRunes(b, 80))
	}
	if len(out) == 0 {
		out = []string{"本周无可用的复盘要点"}
	}
	return out
}

// GenerateWeeklyReview 生成 day 所在周的 AI 周报：逐日读数+周聚合一次会话，三档核对，
// 落 daily_reviews(kind='ai_week')（行对齐到本周最后交易日）并同事务发 strategy.review 事件。
// 整周完全没取到数据时不调模型、不发事件，只留一行降级周报（统计口径可追）。
func (uc *Usecase) GenerateWeeklyReview(ctx context.Context, day time.Time) (retErr error) {
	finish := observe.ForService("brain").Op("review_ai_week").Begin()
	observedSaved, observedDegraded := false, false
	defer func() { finishObservation(finish, retErr, observedSaved, observedDegraded, false) }()
	day = shDate(day)
	open, err := uc.cal.Open(day)
	if err != nil {
		return fmt.Errorf("brain: calendar: %w", err)
	}
	if !open {
		if day, err = uc.cal.PrevOpen(day); err != nil {
			return fmt.Errorf("brain: prev open: %w", err)
		}
	}
	weekEnd, err := uc.weekLastOpenDay(day)
	if err != nil {
		return fmt.Errorf("brain: week end: %w", err)
	}
	days, err := uc.weekOpenDays(day)
	if err != nil {
		return fmt.Errorf("brain: week days: %w", err)
	}
	fullWeek, err := uc.weekOpenDays(weekEnd)
	if err != nil {
		return fmt.Errorf("brain: full week days: %w", err)
	}

	trace := newTrace("")
	decisionID, err := uc.decisions.Begin(ctx, "review_ai_week", "", trace)
	if err != nil {
		return fmt.Errorf("brain: begin decision: %w", err)
	}
	start := uc.now()
	pctx, pcancel := persistCtx(ctx)
	defer pcancel()

	missingSet := map[string]bool{}
	rows := make([]weekDayFacts, 0, len(days))
	for _, d := range days {
		frozen, err := uc.reviews.FrozenOn(ctx, d)
		if err != nil {
			return fmt.Errorf("brain: frozen %s: %w", d.Format("2006-01-02"), err)
		}
		senti, err := uc.reviews.SentimentOn(ctx, d)
		if err != nil {
			return fmt.Errorf("brain: sentiment %s: %w", d.Format("2006-01-02"), err)
		}
		switch {
		case frozen == nil && senti == nil:
			missingSet[d.Format("01-02")+" 定稿与情绪"] = true
		default:
			if frozen == nil {
				missingSet[d.Format("01-02")+" 定稿"] = true
			} else {
				for _, m := range frozen.Missing {
					missingSet[d.Format("01-02")+" "+m] = true
				}
			}
			if senti == nil {
				missingSet[d.Format("01-02")+" 情绪截面"] = true
			}
		}
		rows = append(rows, weekDayFacts{day: d, frozen: frozen, senti: senti})
	}

	var facts []Fact
	var briefs []string
	usable := 0
	for i, r := range rows {
		brief := r.brief()
		if brief == "" {
			continue
		}
		usable++
		briefs = append(briefs, r.day.Format("01-02")+" "+brief)
		facts = append(facts, Fact{
			ID: fmt.Sprintf("F:week.day%d", i+1), Label: r.day.Format("01-02") + " 读数",
			Text: brief, Dim: External,
		})
	}
	facts = append(facts, uc.weekAggregateFacts(ctx, rows, day, missingSet)...)
	if themeFacts, ok := weekThemeFacts(rows); ok {
		facts = append(facts, themeFacts...)
	} else {
		missingSet["week_theme"] = true
	}
	seal := sealOf(day) // P1-6：数据截到生成日 15:00 封盘
	stats := map[string]any{
		"window_start":  days[0].Format("2006-01-02"),
		"window_end":    days[len(days)-1].Format("2006-01-02"),
		"day_count":     len(days),
		"expected_days": len(fullWeek),
		"usable_days":   usable,
		"days":          briefs,
		"sealed_at":     seal.Format("2006-01-02 15:04"),
		"missing":       missingList(missingSet),
	}
	title := fmt.Sprintf("AI 周复盘 %s~%s", days[0].Format("01.02"), days[len(days)-1].Format("01.02"))

	// 整周没有任何可用读数：不调模型、不发通知，只留降级行。
	if usable == 0 {
		content := fmt.Sprintf("本周（%s 至 %s）没取到任何定稿与情绪数据，未生成周报。", days[0].Format("01-02"), days[len(days)-1].Format("01-02"))
		stats["evidence"] = []EvidenceItem{}
		stats["omitted_count"] = 0
		stats["partial"] = true
		stats["degraded"] = true
		if err := uc.reviews.SaveReview(pctx, weekEnd, reviewWeekKind, content, stats, nil, ""); err != nil {
			return fmt.Errorf("brain: save empty weekly review: %w", err)
		}
		if err := uc.decisions.Finish(pctx, decisionID, DecisionRecord{
			Kind: "review_ai_week", TraceID: trace, Degraded: true,
			Content:   toMap(map[string]any{"summary": content, "usable_days": 0}),
			LatencyMS: int(uc.now().Sub(start).Milliseconds()),
		}); err != nil {
			return fmt.Errorf("brain: finish decision: %w", err)
		}
		uc.log.Warnf("weekly review %s: no usable day data, saved degraded row without event", weekEnd.Format("2006-01-02"))
		observedSaved, observedDegraded = true, true
		return nil
	}

	pack := FactPack{AsOf: seal, Facts: facts}
	user := renderPack(&pack)
	nums := newNumberSet(facts, reviewWeeklyPrompt.System, user)
	out, resp, err := callJSON(ctx, uc, callSpec{
		task: "review.weekly", prompt: reviewWeeklyPrompt, prio: llm.Review, trace: trace, decision: decisionID, user: user,
	}, func(o *reviewSynthesisOut) error { return o.check() })

	var summary string
	var lessons []string
	var trail []EvidenceItem
	var models, tags []string
	omitted := 0
	degraded := false
	if err != nil {
		degraded = true
		reason := "模型不可用：" + err.Error()
		if errors.Is(err, llm.ErrInvalid) {
			reason = "模型输出两次不合格：" + invalidReason(err)
		}
		uc.log.Warnf("weekly review %s degraded: %s", weekEnd.Format("2006-01-02"), reason)
		summary = "本周报模型未参与（" + reason + "）。周内逐日读数：" + strings.Join(briefs, "；")
		lessons = fallbackLessons(briefs)
	} else {
		models, tags = append(models, resp.Model), append(tags, reviewWeeklyPrompt.Tag())
		items, n := scrubBriefingText(nums, "review.weekly", &out.Summary, out.Lessons)
		omitted += n
		trail = append(trail, items...)
		summary, lessons = out.Summary, out.Lessons
	}
	stats["evidence"] = trail
	stats["omitted_count"] = omitted
	stats["partial"] = degraded || len(missingSet) > 0
	stats["degraded"] = degraded
	observedDegraded = degraded

	model := strings.Join(models, ",")
	if err := uc.reviews.SaveReviewWithEvent(pctx, weekEnd, reviewWeekKind, summary, stats, lessons, model, trace, title); err != nil {
		return fmt.Errorf("brain: save weekly review: %w", err)
	}
	if err := uc.decisions.Finish(pctx, decisionID, DecisionRecord{
		Kind: "review_ai_week", TraceID: trace,
		Content:       toMap(map[string]any{"summary": summary, "lessons": lessons, "days": briefs}),
		PromptVersion: strings.Join(tags, ","), Model: model,
		Degraded: degraded, LatencyMS: int(uc.now().Sub(start).Milliseconds()),
	}); err != nil {
		return fmt.Errorf("brain: finish decision: %w", err)
	}
	observedSaved = true
	return nil
}

// GenerateAIReviewOf 按 kind 分派 AI 复盘生成：ai=分段式日复盘（默认）、ai_week=周报。
func (uc *Usecase) GenerateAIReviewOf(ctx context.Context, day time.Time, kind string) error {
	switch strings.TrimSpace(kind) {
	case "", reviewAIKind:
		return uc.GenerateAIReview(ctx, day)
	case reviewWeekKind:
		return uc.GenerateWeeklyReview(ctx, day)
	default:
		return badRequest("kind 只能是 ai 或 ai_week")
	}
}
