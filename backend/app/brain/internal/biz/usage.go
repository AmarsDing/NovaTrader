package biz

import (
	"context"
	"time"
)

// TierUsage 是一档模型在窗口内的令牌用量与调用计数。
type TierUsage struct {
	Tier             string
	Model            string
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
	Calls            int64
	Failures         int64
}

// UsageSummary 是 /v1/brain/usage 的聚合结果。
type UsageSummary struct {
	Window string
	Since  time.Time
	Tiers  []TierUsage
	Total  TierUsage
}

// ReasoningExcerpt 是某次研判的模型输出摘录。
type ReasoningExcerpt struct {
	Found      bool
	DecisionID int
	TraceID    string
	Excerpt    string
	LatencyMS  int
	Tier       string
	Model      string
	Task       string
	CreatedAt  time.Time
}

// LatencyBucket 是延迟直方图的一桶。
type LatencyBucket struct {
	LeMs  float64
	Count int64
}

// LatencyHistogram 是 /v1/brain/latency-histogram 的聚合结果。
type LatencyHistogram struct {
	WindowMinutes int
	Total         int64
	P50Ms         float64
	P95Ms         float64
	Buckets       []LatencyBucket
}

// LlmCallRepo 只读聚合 llm_call_log（监控驾驶舱 P1）。
type LlmCallRepo interface {
	// Usage 聚合 since 之后的令牌用量，按档分组。
	Usage(ctx context.Context, since time.Time) ([]TierUsage, error)
	// ReasoningByTrace 取该 trace 最近一次 status=ok 的输出摘录；查不到返回 Found=false。
	ReasoningByTrace(ctx context.Context, traceID string) (*ReasoningExcerpt, error)
	// ReasoningByDecision 同上，按 decision_id。
	ReasoningByDecision(ctx context.Context, decisionID int) (*ReasoningExcerpt, error)
	// LatencyHistogram 聚合 since 之后 status=ok 调用的延迟分位与 8 桶直方图。
	LatencyHistogram(ctx context.Context, since time.Time) (*LatencyHistogram, error)
	// SignalTrace 取 trade_signals.trace_id；查不到返回空串不报错。
	SignalTrace(ctx context.Context, signalID int) (string, error)
}
