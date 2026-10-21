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
		DisableJSONMode: l.GetDisableJsonMode(),
	}
	large := llm.ModelConfig{Timeout: timeout, Temperature: 0, MaxTokens: 300, MaxConcurrency: 1, MaxQueue: 32,
		DisableJSONMode: l.GetDisableJsonMode()}
	if l.GetLargeModel() != "" {
		large.BaseURL = l.GetBaseUrl()
		large.Model = l.GetLargeModel()
		large.APIKey = l.GetApiKey()
	}
	gw := llm.New(map[llm.Tier]llm.ModelConfig{llm.Small: small, llm.Large: large}, llm.EntRecorder{Client: client})
	gw.SetLive(liveOverlay(appcfg.Get))
	return &gatewayModel{gw: gw, yamlOn: l.GetEnabled()}
}

// liveOverlay 每次调用前读配置页的地址、模型名、密钥与 JSON 模式开关（M13-T06b 接线）。
// appcfg 值非空即盖 conf（冲突 appcfg 优先，与既有 url/small/large/key 同惯例）；
// app.intel.disable_json_mode 键只认 "true"/"false"，其余取值（含空串=清除）保持启动配置。
// get 注入便于钉测试（生产传 appcfg.Get）。
func liveOverlay(get func(context.Context, string) string) func(llm.Tier) llm.Overlay {
	return func(t llm.Tier) llm.Overlay {
		model := get(context.Background(), "app.intel.small")
		if t == llm.Large {
			model = get(context.Background(), "app.intel.large")
		}
		over := llm.Overlay{
			BaseURL: get(context.Background(), "app.intel.url"),
			Model:   model,
			APIKey:  get(context.Background(), "app.intel.key"),
		}
		switch strings.TrimSpace(get(context.Background(), "app.intel.disable_json_mode")) {
		case "true":
			on := true
			over.DisableJSONMode = &on
		case "false":
			off := false
			over.DisableJSONMode = &off
		}
		return over
	}
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
