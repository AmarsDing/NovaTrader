// Package data 是 strategy 服务的数据访问：ent 仓储、行情快照（PostgreSQL 日线 + Redis 最新快照）、brain 客户端。
package data

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"server/conf"
	"server/ent"
	"server/pkg/dbinit"
	"server/pkg/migrate"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-redis/redis/v8"
	"github.com/google/wire"
	_ "github.com/jackc/pgx/v5/stdlib"
)

var ProviderSet = wire.NewSet(
	NewData, NewParamRepo, NewSignalRepo, NewCandidateRepo, NewBlacklistRepo, NewPositionRepo,
	NewMarketSource, NewAnalyzer,
)

type Data struct {
	Client *ent.Client
	Redis  *redis.Client
}

func dbConfig(c *conf.Postgres) dbinit.Config {
	cfg := dbinit.Config{
		DSN: c.GetDsn(), Host: c.GetHost(), Port: c.GetPort(), User: c.GetUser(),
		Password: c.GetPassword(), Database: c.GetDatabase(), SSLMode: c.GetSslMode(),
		Timeout: c.GetDialTimeout().AsDuration(),
	}
	if cfg.Database == "" && cfg.DSN == "" {
		cfg.Database = "novatrader"
	}
	return cfg
}

// NewData 启动时自动建库、迁移，再打开 ent 客户端。Redis 不可用时盘中快照为空，只影响盘中扫描。
func NewData(pg *conf.Postgres, rc *conf.Redis, logger log.Logger) (*Data, func(), error) {
	helper := log.NewHelper(log.With(logger, "module", "strategy/data"))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cfg := dbConfig(pg)
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
		return nil, nil, fmt.Errorf("strategy: open postgres: %w", err)
	}
	if n := pg.GetMaxOpenConns(); n > 0 {
		db.SetMaxOpenConns(int(n))
	}
	if n := pg.GetMaxIdleConns(); n > 0 {
		db.SetMaxIdleConns(int(n))
	}
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	d := &Data{Client: client}
	if addr := rc.GetAddr(); addr != "" {
		d.Redis = redis.NewClient(&redis.Options{
			Addr:         addr,
			ReadTimeout:  rc.GetReadTimeout().AsDuration(),
			WriteTimeout: rc.GetWriteTimeout().AsDuration(),
		})
	}
	cleanup := func() {
		if d.Redis != nil {
			_ = d.Redis.Close()
		}
		if err := client.Close(); err != nil {
			helper.Errorf("close ent: %v", err)
		}
	}
	return d, cleanup, nil
}
