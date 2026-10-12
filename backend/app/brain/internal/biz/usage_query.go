package biz

import (
	"context"
	"strings"
	"time"
)

// Usage 聚合 llm_call_log 的令牌用量。window：today（默认）/ 1h / 24h。
func (uc *Usecase) Usage(ctx context.Context, window string) (*UsageSummary, error) {
	since, name := usageWindow(uc.now(), window)
	tiers, err := uc.llmCalls.Usage(ctx, since)
	if err != nil {
		return nil, err
	}
	out := &UsageSummary{Window: name, Since: since, Tiers: tiers}
	for _, t := range tiers {
		out.Total.PromptTokens += t.PromptTokens
		out.Total.CompletionTokens += t.CompletionTokens
		out.Total.Calls += t.Calls
		out.Total.Failures += t.Failures
	}
	out.Total.Tier = "total"
	out.Total.TotalTokens = out.Total.PromptTokens + out.Total.CompletionTokens
	return out, nil
}

func usageWindow(now time.Time, window string) (time.Time, string) {
	switch strings.ToLower(strings.TrimSpace(window)) {
	case "1h":
		return now.Add(-time.Hour), "1h"
	case "24h":
		return now.Add(-24 * time.Hour), "24h"
	default:
		// 自然日 00:00（本地时区）
		day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		return day, "today"
	}
}

// Reasoning 按 signal_id 或 decision_id 找最近一次成功调用的输出摘录。
func (uc *Usecase) Reasoning(ctx context.Context, signalID, decisionID int) (*ReasoningExcerpt, error) {
	if signalID > 0 {
		trace, err := uc.llmCalls.SignalTrace(ctx, signalID)
		if err != nil {
			return nil, err
		}
		if trace == "" {
			return &ReasoningExcerpt{Found: false}, nil
		}
		return uc.llmCalls.ReasoningByTrace(ctx, trace)
	}
	if decisionID > 0 {
		return uc.llmCalls.ReasoningByDecision(ctx, decisionID)
	}
	return nil, badRequest("signal_id 或 decision_id 至少给一个")
}

// LatencyHistogram 聚合近 windowMinutes 分钟成功调用的延迟直方图。
func (uc *Usecase) LatencyHistogram(ctx context.Context, windowMinutes int) (*LatencyHistogram, error) {
	if windowMinutes <= 0 {
		windowMinutes = 60
	}
	if windowMinutes > 24*60 {
		windowMinutes = 24 * 60
	}
	out, err := uc.llmCalls.LatencyHistogram(ctx, uc.now().Add(-time.Duration(windowMinutes)*time.Minute))
	if err != nil {
		return nil, err
	}
	out.WindowMinutes = windowMinutes
	return out, nil
}
