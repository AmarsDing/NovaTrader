// Package service 把 brain.v1 协议转成 biz 调用，只做参数转换和错误映射。
package service

import (
	"context"
	"errors"
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
	res, err := s.uc.Ask(ctx, int(req.GetDecisionId()), req.GetQuestion())
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
