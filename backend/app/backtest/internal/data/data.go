// Package data 是 backtest 的数据访问层：ent 访问 PostgreSQL，NATS 发进度。
package data

import (
	"context"
	"fmt"

	"server/app/backtest/internal/biz"
	"server/conf"
	"server/ent"
	"server/pkg/backtest"
	"server/pkg/dbinit"
	"server/pkg/entclient"
	"server/pkg/events"
	"server/pkg/scheduler"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
)

var ProviderSet = wire.NewSet(
	NewEntClient, scheduler.NewEntStore, NewBus, NewRepo, NewSource, NewPublisher,
	wire.Bind(new(biz.Repo), new(*Repo)),
	wire.Bind(new(biz.Publisher), new(*Publisher)),
	wire.Bind(new(backtest.Source), new(*Source)),
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
