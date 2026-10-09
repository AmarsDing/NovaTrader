// Package data 是 notify 的数据访问层：ent 写通知记录，NATS 推桌面，HTTP 调飞书。
package data

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"server/app/notify/internal/biz"
	"server/conf"
	"server/pkg/dbinit"
	"server/pkg/events"
	"server/pkg/migrate"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"

	"server/ent"

	_ "github.com/jackc/pgx/v5/stdlib"
)

var ProviderSet = wire.NewSet(
	NewEntClient, NewBus, NewRepo, NewDesktop, NewFeishu, NewPipeline,
	wire.Bind(new(biz.Store), new(*Repo)),
	wire.Bind(new(biz.Desktop), new(*Desktop)),
	wire.Bind(new(biz.Feishu), new(*Feishu)),
)

func NewPipeline(c *conf.Notify, store *Repo, desk *Desktop, fei *Feishu) *biz.Pipeline {
	cfg := biz.Config{Templates: map[string]string{}}
	if c != nil {
		if c.GetMergeWindowSec() > 0 {
			cfg.MergeWindow = time.Duration(c.GetMergeWindowSec()) * time.Second
		}
		if c.GetRetryMax() > 0 {
			cfg.RetryMax = int(c.GetRetryMax())
		}
		cfg.Templates = c.GetTemplates()
	}
	return biz.NewPipeline(cfg, store, desk, fei)
}

func dbConfig(c *conf.Postgres) dbinit.Config {
	cfg := dbinit.Config{
		DSN: c.GetDsn(), Host: c.GetHost(), Port: c.GetPort(),
		User: c.GetUser(), Password: c.GetPassword(), Database: c.GetDatabase(), SSLMode: c.GetSslMode(),
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
	cfg := dbConfig(c)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := dbinit.EnsureDatabase(ctx, cfg); err != nil {
		return nil, nil, err
	}
	if err := dbinit.EnsureExtension(ctx, cfg, "vector"); err != nil {
		return nil, nil, err
	}
	if err := migrate.Schema(ctx, cfg); err != nil {
		return nil, nil, err
	}
	dsn, err := dbinit.BusinessDSN(cfg)
	if err != nil {
		return nil, nil, err
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("data: open: %w", err)
	}
	if n := c.GetMaxOpenConns(); n > 0 {
		db.SetMaxOpenConns(int(n))
	}
	if n := c.GetMaxIdleConns(); n > 0 {
		db.SetMaxIdleConns(int(n))
	}
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	cleanup := func() {
		if err := client.Close(); err != nil {
			log.NewHelper(logger).Errorf("close ent: %v", err)
		}
	}
	return client, cleanup, nil
}

func NewBus(c *conf.Nats) (*events.Bus, func(), error) {
	bus, err := events.Dial(c.GetUrl())
	if err != nil {
		return nil, nil, err
	}
	return bus, bus.Close, nil
}
