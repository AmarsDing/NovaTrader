package data

import (
	"context"
	"sort"
	"time"

	"server/app/brain/internal/biz"
	"server/ent"
	"server/ent/llmcalllog"
)

type llmCallRepo struct {
	db *ent.Client
}

// NewLlmCallRepo 提供 llm_call_log 的只读聚合（监控驾驶舱 P1）。
func NewLlmCallRepo(db *ent.Client) biz.LlmCallRepo {
	return &llmCallRepo{db: db}
}

func (r *llmCallRepo) Usage(ctx context.Context, since time.Time) ([]biz.TierUsage, error) {
	rows, err := r.db.LlmCallLog.Query().
		Where(llmcalllog.CreatedAtGTE(since)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	byTier := map[string]*biz.TierUsage{}
	for _, row := range rows {
		key := row.Tier
		cur, ok := byTier[key]
		if !ok {
			cur = &biz.TierUsage{Tier: row.Tier, Model: row.Model}
			byTier[key] = cur
		}
		if cur.Model == "" {
			cur.Model = row.Model
		}
		cur.PromptTokens += int64(row.PromptTokens)
		cur.CompletionTokens += int64(row.CompletionTokens)
		cur.Calls++
		if row.Status != "ok" {
			cur.Failures++
		}
	}
	out := make([]biz.TierUsage, 0, len(byTier))
	for _, cur := range byTier {
		cur.TotalTokens = cur.PromptTokens + cur.CompletionTokens
		out = append(out, *cur)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Tier < out[j].Tier })
	return out, nil
}

func (r *llmCallRepo) SignalTrace(ctx context.Context, signalID int) (string, error) {
	row, err := r.db.TradeSignal.Get(ctx, signalID)
	if ent.IsNotFound(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return row.TraceID, nil
}

func (r *llmCallRepo) ReasoningByTrace(ctx context.Context, traceID string) (*biz.ReasoningExcerpt, error) {
	rows, err := r.db.LlmCallLog.Query().
		Where(llmcalllog.TraceID(traceID), llmcalllog.Status("ok")).
		Order(ent.Desc(llmcalllog.FieldCreatedAt)).
		Limit(1).
		All(ctx)
	if err != nil {
		return nil, err
	}
	return toExcerpt(rows), nil
}

func (r *llmCallRepo) ReasoningByDecision(ctx context.Context, decisionID int) (*biz.ReasoningExcerpt, error) {
	rows, err := r.db.LlmCallLog.Query().
		Where(llmcalllog.DecisionID(decisionID), llmcalllog.Status("ok")).
		Order(ent.Desc(llmcalllog.FieldCreatedAt)).
		Limit(1).
		All(ctx)
	if err != nil {
		return nil, err
	}
	return toExcerpt(rows), nil
}

func toExcerpt(rows []*ent.LlmCallLog) *biz.ReasoningExcerpt {
	if len(rows) == 0 {
		return &biz.ReasoningExcerpt{Found: false}
	}
	row := rows[0]
	ex := &biz.ReasoningExcerpt{
		Found:     true,
		TraceID:   row.TraceID,
		Excerpt:   firstParagraph(row.Output, 200),
		LatencyMS: row.LatencyMs,
		Tier:      row.Tier,
		Model:     row.Model,
		Task:      row.Task,
		CreatedAt: row.CreatedAt,
	}
	if row.DecisionID != nil {
		ex.DecisionID = *row.DecisionID
	}
	return ex
}

// firstParagraph 取正文首段（空行截断），最多 n 个字符。
func firstParagraph(s string, n int) string {
	for i := 0; i+1 < len(s); i++ {
		if s[i] == '\n' && s[i+1] == '\n' {
			s = s[:i]
			break
		}
	}
	r := []rune(s)
	if len(r) > n {
		r = r[:n]
	}
	return string(r)
}

func (r *llmCallRepo) LatencyHistogram(ctx context.Context, since time.Time) (*biz.LatencyHistogram, error) {
	rows, err := r.db.LlmCallLog.Query().
		Where(llmcalllog.CreatedAtGTE(since), llmcalllog.Status("ok")).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := &biz.LatencyHistogram{}
	if len(rows) == 0 {
		return out, nil
	}
	lat := make([]float64, 0, len(rows))
	for _, row := range rows {
		lat = append(lat, float64(row.LatencyMs))
	}
	sort.Float64s(lat)
	out.Total = int64(len(lat))
	out.P50Ms = percentile(lat, 0.50)
	out.P95Ms = percentile(lat, 0.95)
	out.Buckets = buckets(lat, 8)
	return out, nil
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)-1) * p)
	return sorted[idx]
}

// buckets 按 P95 自适应桶宽切成 n 桶，最后一桶上沿为样本最大值。
func buckets(sorted []float64, n int) []biz.LatencyBucket {
	out := make([]biz.LatencyBucket, 0, n)
	if len(sorted) == 0 {
		return out
	}
	hi := percentile(sorted, 0.95)
	max := sorted[len(sorted)-1]
	if hi <= 0 {
		hi = max
	}
	if hi <= 0 {
		hi = 1
	}
	width := hi / float64(n-1)
	for i := 0; i < n; i++ {
		le := hi
		if i < n-1 {
			le = width * float64(i+1)
		} else {
			le = max
		}
		out = append(out, biz.LatencyBucket{LeMs: le})
	}
	for _, v := range sorted {
		placed := false
		for i := range out {
			if v <= out[i].LeMs {
				out[i].Count++
				placed = true
				break
			}
		}
		if !placed {
			out[len(out)-1].Count++
		}
	}
	return out
}
