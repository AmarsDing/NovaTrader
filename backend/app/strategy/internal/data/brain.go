package data

import (
	"context"
	"time"

	brainv1 "server/api/brain/v1"
	"server/app/strategy/internal/biz"
	"server/conf"

	"github.com/go-kratos/kratos/v2/log"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
)

type brainAnalyzer struct {
	client brainv1.BrainHTTPClient
}

// NewAnalyzer 连接 M05 brain。brain_addr 为空时返回 nil，biz 把所有候选按模型不可用处理。
func NewAnalyzer(c *conf.Strategy, logger log.Logger) (biz.Analyzer, func(), error) {
	addr := c.GetBrainAddr()
	if addr == "" {
		log.NewHelper(logger).Warn("strategy: brain_addr is empty, all signals will be ai_degraded")
		return nil, func() {}, nil
	}
	timeout := c.GetAiTimeout().AsDuration()
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	conn, err := khttp.NewClient(context.Background(), khttp.WithEndpoint(addr), khttp.WithTimeout(timeout))
	if err != nil {
		return nil, nil, err
	}
	return &brainAnalyzer{client: brainv1.NewBrainHTTPClient(conn)}, func() { _ = conn.Close() }, nil
}

func (b *brainAnalyzer) Analyze(ctx context.Context, in biz.AnalyzeInput) (biz.AnalyzeOutput, error) {
	req := &brainv1.AnalyzeRequest{
		Symbol: in.Symbol, Name: in.Name, TraceId: in.TraceID, Priority: brainv1.Priority_INTRADAY,
		RuleScore: in.RuleScore, Phase: string(in.Stage),
	}
	if !in.AsOf.IsZero() {
		req.AsOf = in.AsOf.Format(time.RFC3339)
	}
	for _, f := range in.Facts {
		req.Facts = append(req.Facts, &brainv1.Fact{Id: f.ID, Label: f.Label, Value: f.Value, Unit: f.Unit, Text: f.Text, Dim: f.Dim})
	}
	res, err := b.client.Analyze(ctx, req)
	if err != nil {
		return biz.AnalyzeOutput{}, err
	}
	return fromAnalysis(res), nil
}

func fromAnalysis(res *brainv1.Analysis) biz.AnalyzeOutput {
	out := biz.AnalyzeOutput{
		DecisionID: res.GetDecisionId(), Score: res.GetCompositeScore(), Summary: res.GetSummary(),
		Degraded: res.GetAiDegraded(), Discarded: res.GetDiscarded(), Reason: res.GetReason(),
		Dims: map[string]float64{},
	}
	seen := map[string]bool{}
	add := func(ids []string) {
		for _, id := range ids {
			if id != "" && !seen[id] {
				seen[id] = true
				out.Evidence = append(out.Evidence, id)
			}
		}
	}
	add(res.GetSummaryEvidence())
	for _, d := range res.GetDimensions() {
		out.Dims[d.GetDim()] = float64(d.GetScore())
		for _, r := range d.GetReasons() {
			add(r.GetEvidence())
		}
	}
	return out
}
