// Package data 是 risk 的数据访问层：ent 访问 PostgreSQL，Redis 读快照，NATS 发事件。
package data

import (
	"context"
	"fmt"

	"server/app/risk/internal/biz"
	"server/conf"
	"server/ent"
	"server/pkg/dbinit"
	"server/pkg/entclient"
	"server/pkg/events"
	"server/pkg/outbox"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-redis/redis/v8"
	"github.com/google/wire"
)

var ProviderSet = wire.NewSet(
	NewEntClient, outbox.NewDispatcher, NewBus, NewRedis, NewRepo, NewAuditor, NewQuotes, NewPublisher,
	wire.Bind(new(biz.Repo), new(*Repo)),
	wire.Bind(new(biz.Auditor), new(*Auditor)),
	wire.Bind(new(biz.QuoteSource), new(*Quotes)),
	wire.Bind(new(biz.Publisher), new(*Publisher)),
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

// NewBus 连接 NATS 并确保 JetStream 流包含 risk.>、sys.>。
func NewBus(c *conf.Nats) (*events.Bus, func(), error) {
	bus, err := events.Dial(c.GetUrl())
	if err != nil {
		return nil, nil, err
	}
	return bus, bus.Close, nil
}

// NewRedis 连接快照所在的 Redis。风控只按代码 HMGET，用通用超时即可。
func NewRedis(c *conf.Redis) (*redis.Client, func(), error) {
	if c == nil || c.GetAddr() == "" {
		return nil, nil, fmt.Errorf("data: redis config is missing")
	}
	opt := &redis.Options{Addr: c.GetAddr()}
	if d := c.GetReadTimeout(); d != nil {
		opt.ReadTimeout = d.AsDuration()
	}
	if d := c.GetWriteTimeout(); d != nil {
		opt.WriteTimeout = d.AsDuration()
	}
	rdb := redis.NewClient(opt)
	return rdb, func() { _ = rdb.Close() }, nil
}
