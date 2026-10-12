package data

import (
	"context"
	"fmt"
	"strings"
	"time"

	"server/app/intel/internal/biz"
	"server/conf"
	"server/ent"
	"server/pkg/appcfg"
	"server/pkg/events"
	"server/pkg/llm"
)

// gatewayModel 把情报打分接到 M05 的模型网关。调用记录进 llm_call_log，排队和熔断由网关负责。
type gatewayModel struct {
	gw     *llm.Gateway
	yamlOn bool
}

// NewModel 在配置页或 yaml 打开模型时接入网关。两边都没打开且没有数据库时返回 nil。
func NewModel(c *conf.Intel, client *ent.Client) biz.Model {
	if c == nil {
		return nil
	}
	l := c.GetLlm()
	if l == nil {
		l = &conf.IntelLLM{}
	}
	if !l.GetEnabled() && client == nil {
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
	gw := llm.New(map[llm.Tier]llm.ModelConfig{llm.Small: small, llm.Large: large}, llm.EntRecorder{Client: client})
	gw.SetLive(func(t llm.Tier) llm.Overlay {
		model := appcfg.Get(context.Background(), "app.intel.small")
		if t == llm.Large {
			model = appcfg.Get(context.Background(), "app.intel.large")
		}
		return llm.Overlay{
			BaseURL: appcfg.Get(context.Background(), "app.intel.url"),
			Model:   model,
			APIKey:  appcfg.Get(context.Background(), "app.intel.key"),
		}
	})
	return &gatewayModel{gw: gw, yamlOn: l.GetEnabled()}
}

const promptVersion = "intel-score-v1"

func (m *gatewayModel) on() bool {
	switch strings.TrimSpace(appcfg.Get(context.Background(), "app.intel.enabled")) {
	case "true":
		return true
	case "false":
		return false
	}
	if m.yamlOn {
		return true
	}
	return m.gw != nil && (m.gw.Configured(llm.Small) || m.gw.Configured(llm.Large))
}

func (m *gatewayModel) Complete(ctx context.Context, tier, system, user string) (string, error) {
	if m == nil || m.gw == nil || !m.on() {
		return "", fmt.Errorf("llm: disabled")
	}
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
