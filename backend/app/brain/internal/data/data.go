// Package data 是 brain 的数据访问层：只通过 ent 访问 PostgreSQL，模型走 pkg/llm 网关。
package data

import (
	"context"
	"fmt"
	"strings"

	"server/app/brain/internal/biz"
	"server/conf"
	"server/ent"
	"server/pkg/appcfg"
	"server/pkg/dbinit"
	"server/pkg/entclient"
	"server/pkg/events"
	"server/pkg/llm"
	"server/pkg/outbox"
	"server/pkg/scheduler"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
)

var ProviderSet = wire.NewSet(
	NewEntClient, scheduler.NewEntStore, outbox.NewDispatcher, NewBus, NewGateway,
	NewMarketRepo, NewDecisionRepo, NewBriefingRepo, NewProgress, NewLlmCallRepo, NewReviewRepo, NewKnowledgeClient,
	wire.Bind(new(biz.LLM), new(*llm.Gateway)),
)

func dbConfig(c *conf.Postgres) dbinit.Config {
	cfg := dbinit.Config{
		DSN:      c.GetDsn(),
		Host:     c.GetHost(),
		Port:     c.GetPort(),
		User:     c.GetUser(),
		Password: c.GetPassword(),
		Database: c.GetDatabase(),
		SSLMode:  c.GetSslMode(),
	}
	if d := c.GetDialTimeout(); d != nil {
		cfg.Timeout = d.AsDuration()
	}
	return cfg
}

// NewEntClient 按 M00 的启动顺序自动建库、启用 pgvector、迁移，再打开业务连接。
func NewEntClient(c *conf.Postgres, logger log.Logger) (*ent.Client, func(), error) {
	if c == nil {
		return nil, nil, fmt.Errorf("data: postgres config is missing")
	}
	conn, err := entclient.Open(context.Background(), entclient.Config{
		DB:           dbConfig(c),
		MaxOpenConns: int(c.GetMaxOpenConns()),
		MaxIdleConns: int(c.GetMaxIdleConns()),
	})
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() {
		if err := conn.Close(); err != nil {
			log.NewHelper(logger).Errorf("close ent: %v", err)
		}
	}
	return conn.Client, cleanup, nil
}

func NewBus(c *conf.Nats) (*events.Bus, func(), error) {
	bus, err := events.Dial(c.GetUrl())
	if err != nil {
		return nil, nil, err
	}
	return bus, bus.Close, nil
}

// NewGateway 按配置建立 large / small 两档模型，调用记录写 llm_call_log。
func NewGateway(c *conf.Brain, client *ent.Client) *llm.Gateway {
	models := map[llm.Tier]llm.ModelConfig{llm.Large: {}, llm.Small: {}}
	for name, m := range c.GetModels() {
		cfg := llm.ModelConfig{
			BaseURL:         m.GetBaseUrl(),
			Model:           m.GetModel(),
			APIKey:          m.GetApiKey(),
			MaxConcurrency:  int(m.GetMaxConcurrency()),
			MaxQueue:        int(m.GetMaxQueue()),
			Temperature:     m.GetTemperature(),
			MaxTokens:       int(m.GetMaxTokens()),
			DisableJSONMode: m.GetDisableJsonMode(),
		}
		if d := m.GetTimeout(); d != nil {
			cfg.Timeout = d.AsDuration()
		}
		models[llm.Tier(name)] = cfg
	}
	gw := llm.New(models, llm.EntRecorder{Client: client})
	gw.SetLive(liveOverlay(appcfg.Get))
	return gw
}

// liveOverlay 每次调用前读配置页里的地址、模型名、密钥与 JSON 模式开关（M13-T06b 接线）。
// appcfg 值非空即盖 conf（冲突 appcfg 优先，与既有 url/model/key 同惯例）；
// *_disable_json_mode 键只认 "true"/"false"，其余取值（含空串=清除）保持启动配置。
// get 注入便于钉测试（生产传 appcfg.Get）。
func liveOverlay(get func(context.Context, string) string) func(llm.Tier) llm.Overlay {
	return func(t llm.Tier) llm.Overlay {
		prefix := "app.brain.small."
		if t == llm.Large {
			prefix = "app.brain.large."
		}
		over := llm.Overlay{
			BaseURL: get(context.Background(), prefix+"url"),
			Model:   get(context.Background(), prefix+"model"),
			APIKey:  get(context.Background(), prefix+"key"),
		}
		switch strings.TrimSpace(get(context.Background(), prefix+"disable_json_mode")) {
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
