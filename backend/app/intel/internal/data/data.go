// Package data 是 intel 的数据访问层，只通过 ent 访问 PostgreSQL。
package data

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"server/conf"
	"server/ent"
	"server/pkg/dbinit"
	"server/pkg/events"
	"server/pkg/migrate"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"

	_ "github.com/jackc/pgx/v5/stdlib"
)

var ProviderSet = wire.NewSet(NewEntClient, NewBus, NewRepo, NewModel)

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

// NewBus 连接 NATS 并确保 JetStream 流包含 intel.>。
func NewBus(c *conf.Nats) (*events.Bus, func(), error) {
	bus, err := events.Dial(c.GetUrl())
	if err != nil {
		return nil, nil, err
	}
	return bus, bus.Close, nil
}
