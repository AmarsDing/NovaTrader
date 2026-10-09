package biz

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"server/pkg/llm"
	"server/pkg/symbol"
)

const (
	stageFacts     = "facts"
	stageDimension = "dimension"
	stageSynthesis = "synthesis"
	stageDone      = "done"
	stageDegraded  = "degraded"
	stageDiscarded = "discarded"

	neutralScore = 50
	batchWorkers = 8
)

func normalizeSymbol(raw string) (string, error) {
	s, err := symbol.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", badRequest("证券代码 %q 无法识别", raw)
	}
	return s.Tongdaxin(), nil
}

// loadPack 从库里取数据并与调用方的因子合并。库出错时返回空包和错误，调用方降级。
func (uc *Usecase) loadPack(ctx context.Context, sym string, asOf time.Time, in AnalyzeInput) (FactPack, error) {
	if strings.TrimSpace(in.Phase) == "" {
		phase, err := uc.market.Phase(ctx, asOf)
		if err != nil {
			return buildPack(sym, asOf, nil, nil, nil, nil, in), fmt.Errorf("market_sentiment: %w", err)
		}
		in.Phase = phase
	}
	factors, err := uc.market.Factors(ctx, sym, asOf)
	if err != nil {
		return buildPack(sym, asOf, nil, nil, nil, nil, in), fmt.Errorf("stock_factor: %w", err)
	}
	info, err := uc.market.Stock(ctx, sym)
	if err != nil {
		return buildPack(sym, asOf, nil, nil, factors, nil, in), fmt.Errorf("stock_basic: %w", err)
	}
	n := barsForPack
	if len(factors) > 0 {
		n = 1
	}
	bars, err := uc.market.DailyBars(ctx, sym, asOf, n)
	if err != nil {
		return buildPack(sym, asOf, info, nil, factors, nil, in), fmt.Errorf("market_data: %w", err)
	}
	news, err := uc.market.StockNews(ctx, sym, asOf.Add(-newsWindow), asOf, newsForPack)
	if err != nil {
		return buildPack(sym, asOf, info, bars, factors, nil, in), fmt.Errorf("news_sentiment: %w", err)
	}
	return buildPack(sym, asOf, info, bars, factors, news, in), nil
}

// Analyze 对单只候选做四维研判（FR-05-02～05）。
// 模型不可用：综合分取 rule_score，标 ai_degraded。输出两次不合格：标 discarded。
func (uc *Usecase) Analyze(ctx context.Context, in AnalyzeInput) (*Analysis, error) {
	sym, err := normalizeSymbol(in.Symbol)
	if err != nil {
		return nil, err
	}
	if in.RuleScore < 0 || in.RuleScore > 100 || math.IsNaN(in.RuleScore) {
		return nil, badRequest("rule_score 应在 0 到 100 之间")
	}
	callerFacts, err := checkCallerFacts(in.Facts, in.Intel)
	if err != nil {
		return nil, badRequest("%v", err)
	}
	in.Facts = callerFacts
	asOf := in.AsOf
	if asOf.IsZero() {
		asOf = uc.now()
	}
	start := uc.now()
	trace := newTrace(in.TraceID)
	ctx, cancel := context.WithTimeout(ctx, uc.cfg.AnalyzeTimeout)
	defer cancel()

	id, err := uc.decisions.Begin(ctx, "analyze", sym, trace)
	if err != nil {
		return nil, fmt.Errorf("brain: begin decision: %w", err)
	}
	res := &Analysis{
		DecisionID: id, TraceID: trace, Symbol: sym, RuleScore: in.RuleScore,
		Phase: strings.ToUpper(strings.TrimSpace(in.Phase)),
	}
	pack, perr := uc.loadPack(ctx, sym, asOf, in)
	if res.Phase == "" {
		res.Phase = pack.Phase
	}
	res.PhaseCoef = uc.cfg.coef(res.Phase)
	uc.emit(ctx, ProgressEvent{TraceID: trace, Symbol: sym, Stage: stageFacts, OK: perr == nil && len(pack.Facts) > 0})
	var prompts []string
	var models []string
	switch {
	case perr != nil:
		uc.log.Warnf("analyze %s: load facts: %v", sym, perr)
		uc.degrade(res, "读取事实包失败："+perr.Error())
	case len(pack.Facts) == 0:
		uc.degrade(res, "事实包里没有因子，未调用模型")
	default:
		prompts, models = uc.scoreDims(ctx, &pack, res, in.Priority)
	}
	if !res.Degraded && !res.Discarded {
		p, m := uc.synthesize(ctx, &pack, res, in.Priority)
		prompts, models = append(prompts, p), appendModel(models, m)
	}

	stage := stageDone
	switch {
	case res.Discarded:
		stage = stageDiscarded
	case res.Degraded:
		stage = stageDegraded
	}
	res.LatencyMS = uc.now().Sub(start).Milliseconds()
	uc.emit(ctx, ProgressEvent{TraceID: trace, Symbol: sym, Stage: stage, OK: stage == stageDone})

	pctx, pcancel := persistCtx(ctx)
	defer pcancel()
	confidence := res.Composite
	rec := DecisionRecord{
		Kind: "analyze", Symbol: sym, TraceID: trace,
		Content:       toMap(map[string]any{"pack": pack, "result": res}),
		Confidence:    &confidence,
		PromptVersion: strings.Join(prompts, ","), Model: strings.Join(models, ","),
		Degraded: res.Degraded, Discarded: res.Discarded, LatencyMS: int(res.LatencyMS),
	}
	if err := uc.decisions.Finish(pctx, id, rec); err != nil {
		return nil, fmt.Errorf("brain: finish decision: %w", err)
	}
	return res, nil
}

func (uc *Usecase) degrade(res *Analysis, reason string) {
	res.Degraded = true
	res.Reason = reason
	res.Composite = res.RuleScore
	res.Weighted = res.RuleScore
}

// scoreDims 并行跑四个维度。任一维度两次不合格则丢弃候选；否则任一维度不可用则降级。
// 两种情况同时出现时按丢弃处理：输出不合格说明这只票的研判本身有问题，宁可不出信号。
func (uc *Usecase) scoreDims(ctx context.Context, pack *FactPack, res *Analysis, prio llm.Priority) (prompts, models []string) {
	user := renderPack(pack)
	ids := pack.ids()
	scores := make([]DimScore, len(Dims))
	errs := make([]error, len(Dims))
	var wg sync.WaitGroup
	for i, d := range Dims {
		if !hasDimData(pack, d) {
			scores[i] = DimScore{Dim: d, Score: neutralScore, Source: "rule",
				Reasons: []Reason{{Text: "缺少" + dimNames[d] + "数据，按中性处理"}}}
			uc.emit(ctx, ProgressEvent{TraceID: res.TraceID, Symbol: res.Symbol, Stage: stageDimension, Dim: d, OK: true})
			continue
		}
		scores[i] = DimScore{Dim: d, Source: "failed"}
		wg.Add(1)
		go func(i int, d Dim) {
			defer wg.Done()
			p := dimPrompts[d]
			nums := newNumberSet(pack.Facts, p.System, user)
			out, resp, err := callJSON(ctx, uc, callSpec{
				task: string(d), prompt: p, prio: prio, trace: res.TraceID, decision: res.DecisionID, user: user,
			}, func(o *dimOut) error { return o.check(ids, nums) })
			errs[i] = err
			if err == nil {
				scores[i] = DimScore{Dim: d, Score: out.score(), Reasons: out.Reasons, Risks: out.Risks, Source: "model", Model: resp.Model}
			}
			uc.emit(ctx, ProgressEvent{TraceID: res.TraceID, Symbol: res.Symbol, Stage: stageDimension, Dim: d, OK: err == nil})
		}(i, d)
	}
	wg.Wait()

	for i, d := range Dims {
		if scores[i].Source == "model" || errs[i] != nil {
			prompts = append(prompts, dimPrompts[d].Tag())
		}
		models = appendModel(models, scores[i].Model)
	}
	var invalid, unavailable []string
	for i, err := range errs {
		switch {
		case err == nil:
		case errors.Is(err, llm.ErrInvalid):
			invalid = append(invalid, dimNames[Dims[i]]+"："+invalidReason(err))
		default:
			unavailable = append(unavailable, dimNames[Dims[i]]+"："+err.Error())
		}
	}
	res.Dims = scores
	switch {
	case len(invalid) > 0:
		res.Discarded = true
		res.Reason = "模型输出两次不合格，丢弃候选。" + strings.Join(invalid, "；")
		return prompts, models
	case len(unavailable) > 0:
		uc.degrade(res, "模型不可用，退回规则分。"+strings.Join(unavailable, "；"))
		return prompts, models
	}
	byDim := map[Dim]int{}
	for _, s := range scores {
		byDim[s.Dim] = s.Score
	}
	res.Weighted, res.Composite = composite(byDim, uc.cfg.Weights, res.PhaseCoef)
	return prompts, models
}

// composite：综合分 = Σ(维度分 × 权重) × 情绪系数，截断到 0～100，保留两位小数。
func composite(scores map[Dim]int, weights map[Dim]float64, coef float64) (weighted, final float64) {
	for _, d := range Dims {
		weighted += float64(scores[d]) * weights[d]
	}
	final = math.Max(0, math.Min(100, weighted*coef))
	return round(weighted, 2), round(final, 2)
}

// synthesize 让大模型写综合结论。两次不合格或不可用时用各维第一条理由拼接，不影响分数。
func (uc *Usecase) synthesize(ctx context.Context, pack *FactPack, res *Analysis, prio llm.Priority) (prompt, model string) {
	var b strings.Builder
	b.WriteString(renderPack(pack))
	b.WriteString("[四维结论]\n")
	for _, s := range res.Dims {
		fmt.Fprintf(&b, "%s %d 分：", dimNames[s.Dim], s.Score)
		for _, r := range s.Reasons {
			b.WriteString(r.Text)
			b.WriteString("；")
		}
		if len(s.Risks) > 0 {
			b.WriteString("风险：" + strings.Join(s.Risks, "；"))
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "综合分 %s，情绪系数 %s\n", formatValue(res.Composite), formatValue(res.PhaseCoef))
	user := b.String()
	ids := pack.ids()
	nums := newNumberSet(pack.Facts, synthesisPrompt.System, user)
	out, resp, err := callJSON(ctx, uc, callSpec{
		task: "synthesis", prompt: synthesisPrompt, prio: prio, trace: res.TraceID, decision: res.DecisionID, user: user,
	}, func(o *summaryOut) error { return o.check(ids, nums) })
	uc.emit(ctx, ProgressEvent{TraceID: res.TraceID, Symbol: res.Symbol, Stage: stageSynthesis, OK: err == nil})
	if err == nil {
		res.Summary, res.SummaryEvidence = out.Summary, out.Evidence
		return synthesisPrompt.Tag(), resp.Model
	}
	uc.log.Warnf("analyze %s: synthesis fallback: %v", res.Symbol, err)
	res.Summary, res.SummaryEvidence = fallbackSummary(res.Dims)
	return synthesisPrompt.Tag(), resp.Model
}

func fallbackSummary(dims []DimScore) (string, []string) {
	var parts []string
	seen := map[string]bool{}
	var ev []string
	for _, d := range dims {
		if len(d.Reasons) == 0 {
			continue
		}
		parts = append(parts, dimNames[d.Dim]+"："+d.Reasons[0].Text)
		for _, id := range d.Reasons[0].Evidence {
			if !seen[id] {
				seen[id] = true
				ev = append(ev, id)
			}
		}
	}
	return strings.Join(parts, "；"), ev
}

func appendModel(models []string, m string) []string {
	if m == "" {
		return models
	}
	for _, x := range models {
		if x == m {
			return models
		}
	}
	models = append(models, m)
	sort.Strings(models)
	return models
}

// AnalyzeBatch 一次最多 MaxBatch 只（验收：全市场扫描只送前 30 名）。单只失败不影响其他。
func (uc *Usecase) AnalyzeBatch(ctx context.Context, items []AnalyzeInput) ([]*Analysis, error) {
	if len(items) == 0 {
		return nil, badRequest("items 为空")
	}
	if len(items) > uc.cfg.MaxBatch {
		return nil, badRequest("一次最多 %d 只，收到 %d 只", uc.cfg.MaxBatch, len(items))
	}
	out := make([]*Analysis, len(items))
	sem := make(chan struct{}, batchWorkers)
	var wg sync.WaitGroup
	for i := range items {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			res, err := uc.Analyze(ctx, items[i])
			if err != nil {
				res = &Analysis{Symbol: items[i].Symbol, TraceID: items[i].TraceID, RuleScore: items[i].RuleScore}
				uc.degrade(res, err.Error())
			}
			out[i] = res
		}(i)
	}
	wg.Wait()
	return out, nil
}
