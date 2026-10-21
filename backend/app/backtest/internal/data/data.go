// Package data 是 backtest 的数据访问层：ent 访问 PostgreSQL，NATS 发进度。
package data

import (
	"context"
	"fmt"

	"server/app/backtest/internal/biz"
	"server/conf"
	"server/ent"
	"server/pkg/appcfg"
	"server/pkg/backtest"
	"server/pkg/dbinit"
	"server/pkg/entclient"
	"server/pkg/events"
	"server/pkg/llm"
	"server/pkg/registry"
	"server/pkg/scheduler"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
)

var ProviderSet = wire.NewSet(
	NewEntClient, scheduler.NewEntStore, registry.NewBeater, NewBus, NewRepo, NewSource, NewPublisher, NewGateway,
	wire.Bind(new(biz.Repo), new(*Repo)),
	wire.Bind(new(biz.Publisher), new(*Publisher)),
	wire.Bind(new(backtest.Source), new(*Source)),
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

// NewGateway 是 M13-T14 small 档 LLM 网关。conf.Backtest 暂未挂 models 字段；
// 启动配置留空，靠 SetLive 在每次调用前从 appcfg 读 app.backtest.small.{url,model,key}。
// 未配置或读不到时 Chat 走 pkg/llm 现行的 ErrUnavailable，调用方按合同留空叙述字段。
func NewGateway(client *ent.Client) *llm.Gateway {
	gw := llm.New(map[llm.Tier]llm.ModelConfig{llm.Small: {}}, llm.EntRecorder{Client: client})
	gw.SetLive(func(t llm.Tier) llm.Overlay {
		if t != llm.Small {
			return llm.Overlay{}
		}
		return llm.Overlay{
			BaseURL: appcfg.Get(context.Background(), "app.backtest.small.url"),
			Model:   appcfg.Get(context.Background(), "app.backtest.small.model"),
			APIKey:  appcfg.Get(context.Background(), "app.backtest.small.key"),
		}
	})
	return gw
}

// NewBus 连接 NATS。连不上时返回 nil：进度只写库，客户端靠轮询。
func NewBus(c *conf.Nats, logger log.Logger) (*events.Bus, func()) {
	bus, err := events.Dial(c.GetUrl())
	if err != nil {
		log.NewHelper(logger).Warnf("backtest: nats unavailable, progress only in db: %v", err)
		return nil, func() {}
	}
	return bus, bus.Close
}

// Publisher 往普通 NATS 主题发进度，不进 JetStream。
type Publisher struct{ bus *events.Bus }

func NewPublisher(bus *events.Bus) *Publisher { return &Publisher{bus: bus} }

func (p *Publisher) Progress(ctx context.Context, payload biz.ProgressPayload) {
	if p.bus == nil {
		return
	}
	env, err := events.New("backtest", biz.SubjectProgress, events.TraceID(ctx), payload)
	if err != nil {
		return
	}
	b, err := env.Marshal()
	if err != nil {
		return
	}
	_ = p.bus.Conn().Publish(biz.SubjectProgress, b)
}
