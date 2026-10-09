// Package data 是 market 的数据访问层：PostgreSQL 只走 ent，快照读 Redis，事件发普通 NATS。
package data

import (
	"context"
	"fmt"
	"time"

	"server/app/market/internal/biz"
	"server/conf"
	"server/ent"
	"server/pkg/dbinit"
	"server/pkg/entclient"
	"server/pkg/events"
	"server/pkg/market"
	"server/pkg/scheduler"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-redis/redis/v8"
	"github.com/google/wire"
)

var ProviderSet = wire.NewSet(
	NewEntClient, scheduler.NewEntStore, NewRedis, NewBus, NewRepo, NewSnapshots, NewPublisher,
	wire.Bind(new(biz.Repo), new(*Repo)),
	wire.Bind(new(biz.SnapshotSource), new(*Snapshots)),
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

// NewRedis 连接快照所在的 Redis。
func NewRedis(c *conf.Redis) (*redis.Client, func(), error) {
	if c == nil || c.GetAddr() == "" {
		return nil, nil, fmt.Errorf("data: redis config is missing")
	}
	opt := &redis.Options{Addr: c.GetAddr()}
	if d := c.GetReadTimeout(); d != nil {
		// 全市场 HGETALL 约 2MB，0.2s 的通用读超时不够。
		opt.ReadTimeout = maxDuration(d.AsDuration(), 2*time.Second)
	}
	if d := c.GetWriteTimeout(); d != nil {
		opt.WriteTimeout = d.AsDuration()
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

// NewBus 连接 NATS。market.* 只走普通 NATS。
func NewBus(c *conf.Nats) (*events.Bus, func(), error) {
	bus, err := events.Dial(c.GetUrl())
	if err != nil {
		return nil, nil, err
	}
	return bus, bus.Close, nil
}

// Snapshots 读 Redis snap:latest。
type Snapshots struct {
	rdb *redis.Client
	log *log.Helper
}

func NewSnapshots(rdb *redis.Client, logger log.Logger) *Snapshots {
	return &Snapshots{rdb: rdb, log: log.NewHelper(logger)}
}

// All 读全部快照。解析失败的单条跳过并记日志，不让一条坏数据拖垮整轮。
func (s *Snapshots) All(ctx context.Context) ([]market.Snapshot, error) {
	m, err := s.rdb.HGetAll(ctx, market.SnapshotKey).Result()
	if err != nil {
		return nil, err
	}
	out := make([]market.Snapshot, 0, len(m))
	bad := 0
	for _, raw := range m {
		snap, err := market.ParseSnapshot([]byte(raw))
		if err != nil {
			bad++
			continue
		}
		out = append(out, snap)
	}
	if bad > 0 {
		s.log.Warnf("snap:latest: %d malformed entries skipped", bad)
	}
	return out, nil
}

// Publisher 把载荷装进信封发到 NATS。
type Publisher struct {
	bus *events.Bus
}

func NewPublisher(bus *events.Bus) *Publisher { return &Publisher{bus: bus} }

func (p *Publisher) Publish(ctx context.Context, subject string, payload any) error {
	env, err := events.New("market", subject, events.TraceID(ctx), payload)
	if err != nil {
		return err
	}
	return p.bus.Publish(ctx, env)
}
