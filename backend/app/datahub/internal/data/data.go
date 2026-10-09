package data

import (
	"context"
	"fmt"
	"time"

	"server/app/datahub/internal/biz"
	"server/conf"
	"server/ent"
	"server/pkg/dbinit"
	"server/pkg/entclient"
	"server/pkg/events"
	"server/pkg/registry"
	"server/pkg/scheduler"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-redis/redis/v8"
	"github.com/google/wire"
)

var ProviderSet = wire.NewSet(
	NewEntClient, scheduler.NewEntStore, registry.NewBeater, NewRedis, NewBus, NewRepo, NewSnapshotStore, NewCollector,
	wire.Bind(new(biz.Repo), new(*Repo)),
	wire.Bind(new(biz.SnapshotStore), new(*SnapshotStore)),
	wire.Bind(new(biz.Publisher), new(*events.Bus)),
)

func dbConfig(c *conf.Postgres) dbinit.Config {
	cfg := dbinit.Config{
		DSN: c.GetDsn(), Host: c.GetHost(), Port: c.GetPort(), User: c.GetUser(),
		Password: c.GetPassword(), Database: c.GetDatabase(), SSLMode: c.GetSslMode(),
	}
	if d := c.GetDialTimeout(); d != nil {
		cfg.Timeout = d.AsDuration()
	}
	return cfg
}

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

func NewRedis(c *conf.Redis) (*redis.Client, func(), error) {
	if c == nil || c.GetAddr() == "" {
		return nil, nil, fmt.Errorf("data: redis config is missing")
	}
	opt := &redis.Options{Addr: c.GetAddr()}
	if d := c.GetWriteTimeout(); d != nil {
		opt.WriteTimeout = maxDuration(d.AsDuration(), 2*time.Second)
	}
	if d := c.GetReadTimeout(); d != nil {
		opt.ReadTimeout = maxDuration(d.AsDuration(), 2*time.Second)
	}
	rdb := redis.NewClient(opt)
	return rdb, func() { _ = rdb.Close() }, nil
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

func NewBus(c *conf.Nats) (*events.Bus, func(), error) {
	bus, err := events.Dial(c.GetUrl())
	if err != nil {
		return nil, nil, err
	}
	return bus, bus.Close, nil
}
