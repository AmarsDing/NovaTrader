// Package data 是 strategy 服务的数据访问：ent 仓储、行情快照（PostgreSQL 日线 + Redis 最新快照）、brain 客户端。
package data

import (
	"context"
	"time"

	"server/conf"
	"server/ent"
	"server/pkg/dbinit"
	"server/pkg/entclient"
	"server/pkg/outbox"
	"server/pkg/scheduler"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-redis/redis/v8"
	"github.com/google/wire"
)

var ProviderSet = wire.NewSet(
	NewData, NewParamRepo, NewSignalRepo, NewCandidateRepo, NewBlacklistRepo, NewPositionRepo,
	NewMarketSource, NewAnalyzer,
)

type Data struct {
	client *ent.Client
	Redis  *redis.Client
}

// TaskStore 把 task_run 交给调度器，core 不直接拿 ent 客户端。
func (d *Data) TaskStore() scheduler.Store {
	if d == nil {
		return scheduler.EntStore{}
	}
	return scheduler.EntStore{Client: d.client}
}

// DispatchOutbox 投递尚未发出的事件。
func (d *Data) DispatchOutbox(ctx context.Context, pub outbox.Publisher, limit int) (int, error) {
	if d == nil || d.client == nil || pub == nil {
		return 0, nil
	}
	return outbox.Dispatch(ctx, d.client, pub, limit)
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
	conn, err := entclient.Open(ctx, entclient.Config{
		DB:           cfg,
		MaxOpenConns: int(pg.GetMaxOpenConns()),
		MaxIdleConns: int(pg.GetMaxIdleConns()),
	})
	if err != nil {
		return nil, nil, err
	}
	client := conn.Client
	d := &Data{client: client}
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
		if err := conn.Close(); err != nil {
			helper.Errorf("close ent: %v", err)
		}
	}
	return d, cleanup, nil
}
