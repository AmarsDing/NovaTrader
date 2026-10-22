// Package service 把 brain.v1 协议转成 biz 调用，只做参数转换和错误映射。
package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	v1 "server/api/brain/v1"
	"server/app/brain/internal/biz"
	"server/pkg/llm"

	kerrors "github.com/go-kratos/kratos/v2/errors"
	"github.com/google/wire"
)

var ProviderSet = wire.NewSet(NewBrainService)

type BrainService struct {
	v1.UnimplementedBrainServer
	uc *biz.Usecase
}

func NewBrainService(uc *biz.Usecase) *BrainService {
	return &BrainService{uc: uc}
}

func mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, biz.ErrBadRequest):
		return kerrors.BadRequest("BAD_REQUEST", err.Error())
	case errors.Is(err, biz.ErrNotFound):
		return kerrors.NotFound("NOT_FOUND", err.Error())
	}
	return kerrors.InternalServer("INTERNAL", err.Error())
}

var priorities = map[v1.Priority]llm.Priority{
	v1.Priority_PRIORITY_UNSPECIFIED: llm.Intraday,
	v1.Priority_POSITION_RISK:        llm.PositionRisk,
	v1.Priority_INTRADAY:             llm.Intraday,
	v1.Priority_BRIEFING:             llm.Briefing,
	v1.Priority_NEWS_BATCH:           llm.NewsBatch,
	v1.Priority_REVIEW:               llm.Review,
}

func toFacts(in []*v1.Fact) []biz.Fact {
	out := make([]biz.Fact, 0, len(in))
	for _, f := range in {
		out = append(out, biz.Fact{ID: f.GetId(), Label: f.GetLabel(), Value: f.GetValue(), Unit: f.GetUnit(), Text: f.GetText(), Dim: biz.Dim(f.GetDim())})
	}
	return out
}

func toAnalyzeInput(r *v1.AnalyzeRequest) (biz.AnalyzeInput, error) {
	in := biz.AnalyzeInput{
		Symbol: r.GetSymbol(), Name: r.GetName(), TraceID: r.GetTraceId(),
		Priority: priorities[r.GetPriority()], RuleScore: r.GetRuleScore(), Phase: r.GetPhase(),
		Facts: toFacts(r.GetFacts()),
	}
	if s := r.GetAsOf(); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return in, kerrors.BadRequest("BAD_REQUEST", "as_of 应为 RFC3339 时间")
		}
		in.AsOf = t
	}
	for _, n := range r.GetIntel() {
		item := biz.Intel{ID: n.GetId(), Title: n.GetTitle(), Body: n.GetBody(), Source: n.GetSource(), Sentiment: n.GetSentiment()}
		if s := n.GetTime(); s != "" {
			t, err := time.Parse(time.RFC3339, s)
			if err != nil {
				return in, kerrors.BadRequest("BAD_REQUEST", "intel.time 应为 RFC3339 时间")
			}
			item.Time = t
		}
		in.Intel = append(in.Intel, item)
	}
	return in, nil
}

func toReply(a *biz.Analysis) *v1.Analysis {
	out := &v1.Analysis{
		DecisionId: int64(a.DecisionID), TraceId: a.TraceID, Symbol: a.Symbol,
		CompositeScore: a.Composite, WeightedScore: a.Weighted, Phase: a.Phase, PhaseCoef: a.PhaseCoef,
		Summary: a.Summary, SummaryEvidence: a.SummaryEvidence,
		AiDegraded: a.Degraded, Discarded: a.Discarded, Reason: a.Reason,
		LatencyMs: a.LatencyMS, RuleScore: a.RuleScore,
	}
	for _, d := range a.Dims {
		ds := &v1.DimensionScore{Dim: string(d.Dim), Score: int32(d.Score), Risks: d.Risks, Source: d.Source, Model: d.Model}
		for _, r := range d.Reasons {
			ds.Reasons = append(ds.Reasons, &v1.Reason{Text: r.Text, Evidence: r.Evidence})
		}
		out.Dimensions = append(out.Dimensions, ds)
	}
	return out
}

func (s *BrainService) Analyze(ctx context.Context, req *v1.AnalyzeRequest) (*v1.Analysis, error) {
	in, err := toAnalyzeInput(req)
	if err != nil {
		return nil, err
	}
	res, err := s.uc.Analyze(ctx, in)
	if err != nil {
		return nil, mapErr(err)
	}
	return toReply(res), nil
}

func (s *BrainService) AnalyzeBatch(ctx context.Context, req *v1.AnalyzeBatchRequest) (*v1.AnalyzeBatchReply, error) {
	items := make([]biz.AnalyzeInput, 0, len(req.GetItems()))
	for _, r := range req.GetItems() {
		in, err := toAnalyzeInput(r)
		if err != nil {
			return nil, err
		}
		items = append(items, in)
	}
	res, err := s.uc.AnalyzeBatch(ctx, items)
	if err != nil {
		return nil, mapErr(err)
	}
	out := &v1.AnalyzeBatchReply{}
	for _, a := range res {
		out.Items = append(out.Items, toReply(a))
	}
	return out, nil
}

func toBriefing(b *biz.Briefing) *v1.Briefing {
	out := &v1.Briefing{
		Date: b.Date.Format("2006-01-02"), Degraded: b.Degraded, Headline: b.Headline, MarketView: b.MarketView,
		Risks: b.Risks, DecisionId: int64(b.DecisionID), Reason: b.Reason,
	}
	if !b.CreatedAt.IsZero() {
		out.CreatedAt = b.CreatedAt.Format(time.RFC3339)
	}
	if b.OmittedCount != nil {
		omitted := int32(*b.OmittedCount)
		out.OmittedCount = &omitted
	}
	for _, e := range b.Evidence {
		out.Evidence = append(out.Evidence, &v1.EvidenceItem{Field: e.Field, Span: e.Span, Verdict: e.Verdict, Match: e.Match})
	}
	for _, w := range b.Watchlist {
		out.Watchlist = append(out.Watchlist, &v1.WatchItem{Symbol: w.Symbol, Reason: w.Reason, Evidence: w.Evidence})
	}
	for _, p := range b.Positions {
		out.Positions = append(out.Positions, &v1.PositionView{Symbol: p.Symbol, View: p.View, Evidence: p.Evidence})
	}
	for _, n := range b.News {
		out.News = append(out.News, &v1.NewsLine{Id: n.ID, Title: n.Title, Importance: int32(n.Importance), Symbol: n.Symbol})
	}
	return out
}

func (s *BrainService) GenerateBriefing(ctx context.Context, req *v1.GenerateBriefingRequest) (*v1.Briefing, error) {
	day, err := s.uc.ParseDay(req.GetDate())
	if err != nil {
		return nil, mapErr(err)
	}
	b, err := s.uc.GenerateBriefing(ctx, day)
	if err != nil {
		return nil, mapErr(err)
	}
	return toBriefing(b), nil
}

func (s *BrainService) GetBriefing(ctx context.Context, req *v1.GetBriefingRequest) (*v1.Briefing, error) {
	day, err := s.uc.ParseDay(req.GetDate())
	if err != nil {
		return nil, mapErr(err)
	}
	b, err := s.uc.GetBriefing(ctx, day)
	if err != nil {
		return nil, mapErr(err)
	}
	return toBriefing(b), nil
}

func (s *BrainService) Explain(ctx context.Context, req *v1.ExplainRequest) (*v1.Explanation, error) {
	res, err := s.uc.Explain(ctx, biz.ExplainInput{
		Symbol: req.GetSymbol(), Event: req.GetEvent(), Holding: req.GetHolding(),
		TraceID: req.GetTraceId(), Facts: toFacts(req.GetFacts()),
	})
	if err != nil {
		return nil, mapErr(err)
	}
	return &v1.Explanation{
		DecisionId: int64(res.DecisionID), Summary: res.Summary, Impact: res.Impact, Risks: res.Risks,
		Evidence: res.Evidence, AiDegraded: res.Degraded, Discarded: res.Discarded, Reason: res.Reason,
	}, nil
}

func (s *BrainService) Ask(ctx context.Context, req *v1.AskRequest) (*v1.AskReply, error) {
	res, err := s.uc.Ask(ctx, int(req.GetDecisionId()), req.GetQuestion(), req.GetPane())
	if err != nil {
		return nil, mapErr(err)
	}
	return &v1.AskReply{
		DecisionId: int64(res.DecisionID), Answer: res.Answer, Evidence: res.Evidence,
		AiDegraded: res.Degraded, Discarded: res.Discarded, Reason: res.Reason,
	}, nil
}

func (s *BrainService) Stats(ctx context.Context, _ *v1.StatsRequest) (*v1.StatsReply, error) {
	out := &v1.StatsReply{}
	for _, t := range s.uc.Stats(ctx) {
		out.Tiers = append(out.Tiers, &v1.TierStats{
			Tier: string(t.Tier), Model: t.Model, Configured: t.Configured, Calls: t.Calls, Failures: t.Failures,
			P50Ms: t.P50MS, P95Ms: t.P95MS, InFlight: int32(t.InFlight), Queued: int32(t.Queued),
			CircuitOpen: t.CircuitOpen, KvCacheUsage: t.KVCacheUsage, Running: t.Running, Waiting: t.Waiting,
		})
	}
	return out, nil
}

func (s *BrainService) Usage(ctx context.Context, req *v1.UsageRequest) (*v1.UsageReply, error) {
	sum, err := s.uc.Usage(ctx, req.GetWindow())
	if err != nil {
		return nil, mapErr(err)
	}
	out := &v1.UsageReply{Window: sum.Window, Since: sum.Since.Format(time.RFC3339)}
	for _, t := range sum.Tiers {
		out.Tiers = append(out.Tiers, &v1.TierUsage{
			Tier: t.Tier, Model: t.Model, PromptTokens: t.PromptTokens,
			CompletionTokens: t.CompletionTokens, TotalTokens: t.TotalTokens,
			Calls: t.Calls, Failures: t.Failures,
		})
	}
	out.Total = &v1.TierUsage{
		Tier: sum.Total.Tier, PromptTokens: sum.Total.PromptTokens,
		CompletionTokens: sum.Total.CompletionTokens, TotalTokens: sum.Total.TotalTokens,
		Calls: sum.Total.Calls, Failures: sum.Total.Failures,
	}
	return out, nil
}

func (s *BrainService) Reasoning(ctx context.Context, req *v1.ReasoningRequest) (*v1.ReasoningReply, error) {
	ex, err := s.uc.Reasoning(ctx, int(req.GetSignalId()), int(req.GetDecisionId()))
	if err != nil {
		return nil, mapErr(err)
	}
	out := &v1.ReasoningReply{Found: ex.Found}
	if !ex.Found {
		return out, nil
	}
	out.DecisionId = int64(ex.DecisionID)
	out.TraceId = ex.TraceID
	out.Excerpt = ex.Excerpt
	out.LatencyMs = int64(ex.LatencyMS)
	out.Tier = ex.Tier
	out.Model = ex.Model
	out.Task = ex.Task
	if !ex.CreatedAt.IsZero() {
		out.CreatedAt = ex.CreatedAt.Format(time.RFC3339)
	}
	return out, nil
}

func (s *BrainService) LatencyHistogram(ctx context.Context, req *v1.LatencyHistogramRequest) (*v1.LatencyHistogramReply, error) {
	h, err := s.uc.LatencyHistogram(ctx, int(req.GetWindowMinutes()))
	if err != nil {
		return nil, mapErr(err)
	}
	out := &v1.LatencyHistogramReply{
		WindowMinutes: int32(h.WindowMinutes), Total: h.Total,
		P50Ms: h.P50Ms, P95Ms: h.P95Ms,
	}
	for _, b := range h.Buckets {
		out.Buckets = append(out.Buckets, &v1.LatencyBucket{LeMs: b.LeMs, Count: b.Count})
	}
	return out, nil
}

/* ---------- 复盘体系 ---------- */

func (s *BrainService) GetProgramReview(ctx context.Context, req *v1.GetProgramReviewRequest) (*v1.ProgramReview, error) {
	day, err := s.uc.ParseDay(req.GetDate())
	if err != nil {
		return nil, mapErr(err)
	}
	kind := strings.TrimSpace(req.GetKind())
	if kind == "" {
		kind = "program"
	}
	content, stats, lessons, model, effDay, err := s.uc.Review(ctx, day, kind)
	if err != nil {
		return nil, mapErr(err)
	}
	raw, err := json.Marshal(stats)
	if err != nil {
		return nil, mapErr(err)
	}
	return &v1.ProgramReview{
		Date: effDay.Format("2006-01-02"), Content: content, StatsJson: string(raw),
		Lessons: lessons, Model: model,
	}, nil
}

// GenerateAIReview 手动生成或重新生成 AI 复盘：kind 空或 ai = 分段式日复盘；ai_week = 周报（行落在本周最后交易日）。
func (s *BrainService) GenerateAIReview(ctx context.Context, req *v1.GenerateAIReviewRequest) (*v1.ProgramReview, error) {
	day, err := s.uc.ParseDay(req.GetDate())
	if err != nil {
		return nil, mapErr(err)
	}
	kind := strings.TrimSpace(req.GetKind())
	if err := s.uc.GenerateAIReviewOf(ctx, day, kind); err != nil {
		return nil, mapErr(err)
	}
	if kind == "" {
		kind = "ai"
	}
	return s.GetProgramReview(ctx, &v1.GetProgramReviewRequest{Date: day.Format("2006-01-02"), Kind: kind})
}

func (s *BrainService) GetFrozenEmotion(ctx context.Context, req *v1.GetFrozenEmotionRequest) (*v1.FrozenEmotion, error) {
	day, err := s.uc.ParseDay(req.GetDate())
	if err != nil {
		return nil, mapErr(err)
	}
	f, err := s.uc.FrozenEmotionData(ctx, day)
	if err != nil {
		return nil, mapErr(err)
	}
	out := &v1.FrozenEmotion{
		Date: day.Format("2006-01-02"), UpCount: opti32(f.UpCount), DownCount: opti32(f.DownCount),
		BrokenCount: opti32(f.BrokenCount), MaxHeight: opti32(f.MaxHeight),
		SealRate: f.SealRate, Promo_12: f.Promo12, Promo_23: f.Promo23, PromoHigh: f.PromoHigh,
		TierAvg: f.TierAvg, TierMedian: f.TierMedian, TierRedRate: f.TierRedRate, TierRelimit: f.TierRelimit,
		Partial: f.Partial, Missing: f.Missing, Source: f.Source,
	}
	for _, g := range f.StructureGaps {
		out.StructureGaps = append(out.StructureGaps, int32(g))
	}
	if !f.CapturedAt.IsZero() {
		out.CapturedAt = f.CapturedAt.Format(time.RFC3339)
	}
	if out.LadderJson, err = jsonStr(f.Ladder); err != nil {
		return nil, mapErr(err)
	}
	if out.FirstBoardJson, err = jsonStr(f.FirstBoard); err != nil {
		return nil, mapErr(err)
	}
	if out.ThemeTreeJson, err = jsonStr(f.ThemeTree); err != nil {
		return nil, mapErr(err)
	}
	if out.PrevTierJson, err = jsonStr(f.PrevTier); err != nil {
		return nil, mapErr(err)
	}
	if out.SealQualityJson, err = jsonStr(f.SealQ); err != nil {
		return nil, mapErr(err)
	}
	return out, nil
}

func (s *BrainService) ListPredictionChecks(ctx context.Context, req *v1.ListChecksRequest) (*v1.PredictionCheckList, error) {
	items, err := s.uc.ListPredictionChecks(ctx, int(req.GetLimit()))
	if err != nil {
		return nil, mapErr(err)
	}
	out := &v1.PredictionCheckList{}
	for _, c := range items {
		out.Items = append(out.Items, toCheck(c))
	}
	return out, nil
}

func (s *BrainService) RunPredictionCheck(ctx context.Context, req *v1.RunCheckRequest) (*v1.PredictionCheck, error) {
	day, err := s.uc.ParseDay(req.GetDate())
	if err != nil {
		return nil, mapErr(err)
	}
	c, err := s.uc.RunPredictionCheck(ctx, day)
	if err != nil {
		return nil, mapErr(err)
	}
	return toCheck(c), nil
}

func toCheck(c *biz.Check) *v1.PredictionCheck {
	out := &v1.PredictionCheck{
		SignalDate: c.SignalDate.Format("2006-01-02"), CheckDate: c.CheckDate.Format("2006-01-02"),
		Source: c.Source, CandidateCount: opti32(c.CandidateCount), HitCount: opti32(c.HitCount),
		RelimitCount: opti32(c.RelimitCount), HitRate: c.HitRate, AvgPct: c.AvgPct, MaxPct: c.MaxPct,
		Partial: c.Partial, Missing: c.Missing,
	}
	for _, cd := range c.Candidates {
		out.Candidates = append(out.Candidates, &v1.CheckCandidateRow{Symbol: cd.Symbol, Name: cd.Name, Reason: cd.Reason, Text: cd.Text})
	}
	for _, r := range c.Results {
		out.Results = append(out.Results, &v1.CheckResultRow{Symbol: r.Symbol, Name: r.Name, Pct: r.Pct, Relimit: r.Relimit, Hit: r.Hit})
	}
	return out
}

func opti32(p *int) *int32 {
	if p == nil {
		return nil
	}
	v := int32(*p)
	return &v
}

func jsonStr(v any) (string, error) {
	if v == nil {
		return "", nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
