package data

import (
	"context"
	"time"

	"server/app/intel/internal/biz"
	"server/conf"
	"server/ent"
	"server/pkg/events"
	"server/pkg/llm"
)

// gatewayModel 把情报打分接到 M05 的模型网关。调用记录进 llm_call_log，排队和熔断由网关负责。
type gatewayModel struct {
	gw *llm.Gateway
}

// NewModel 在 llm.enabled 为假时返回 nil，业务层只用规则打分。
// base_url 为空时网关会返回不可用，流水线退回规则分并标 degraded。
func NewModel(c *conf.Intel, client *ent.Client) biz.Model {
	l := c.GetLlm()
	if !l.GetEnabled() {
		return nil
	}
	timeout := 20 * time.Second
	if d := l.GetTimeout(); d != nil && d.AsDuration() > 0 {
		timeout = d.AsDuration()
	}
	small := llm.ModelConfig{
		BaseURL: l.GetBaseUrl(), Model: l.GetSmallModel(), APIKey: l.GetApiKey(),
		Timeout: timeout, Temperature: 0, MaxTokens: 300, MaxConcurrency: 2, MaxQueue: 128,
	}
	large := llm.ModelConfig{Timeout: timeout, Temperature: 0, MaxTokens: 300, MaxConcurrency: 1, MaxQueue: 32}
	if l.GetLargeModel() != "" {
		large.BaseURL = l.GetBaseUrl()
		large.Model = l.GetLargeModel()
		large.APIKey = l.GetApiKey()
	}
	return &gatewayModel{gw: llm.New(map[llm.Tier]llm.ModelConfig{llm.Small: small, llm.Large: large}, llm.EntRecorder{Client: client})}
}

const promptVersion = "intel-score-v1"

func (m *gatewayModel) Complete(ctx context.Context, tier, system, user string) (string, error) {
	resp, err := m.gw.Chat(ctx, llm.Request{
		Task:          "intel.score",
		Tier:          llm.Tier(tier),
		Priority:      llm.NewsBatch,
		Messages:      []llm.Message{{Role: "system", Content: system}, {Role: "user", Content: user}},
		TraceID:       events.TraceID(ctx),
		PromptVersion: promptVersion,
	})
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}
