package data

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"server/app/admin/internal/gateway"
	"server/conf"
	"server/ent"
	"server/pkg/dbinit"
	"server/pkg/entclient"

	"github.com/go-kratos/kratos/v2/log"
)

// AuditStore 把网关审计写进 audit_log。库连不上时 Enabled 为 false，admin 仍可启动。
type AuditStore struct {
	client *ent.Client
	db     *sql.DB
}

func NewAuditStore(pg *conf.Postgres, logger log.Logger) (*AuditStore, func(), error) {
	helper := log.NewHelper(log.With(logger, "module", "admin/audit"))
	noop := func() {}
	store, cleanup, err := connectAudit(context.Background(), pg)
	if err != nil {
		helper.Warnf("审计关闭：%v", err)
		return &AuditStore{}, noop, nil
	}
	return store, cleanup, nil
}

func connectAudit(ctx context.Context, pg *conf.Postgres) (*AuditStore, func(), error) {
	if pg == nil || (pg.GetDsn() == "" && pg.GetHost() == "") {
		return nil, nil, errors.New("postgres 未配置")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cfg := dbinit.Config{
		DSN: pg.GetDsn(), Host: pg.GetHost(), Port: pg.GetPort(), User: pg.GetUser(),
		Password: pg.GetPassword(), Database: pg.GetDatabase(), SSLMode: pg.GetSslMode(),
		Timeout: pg.GetDialTimeout().AsDuration(),
	}
	if cfg.Database == "" && cfg.DSN == "" {
		cfg.Database = "novatrader"
	}
	conn, err := entclient.Open(ctx, entclient.Config{
		DB:           cfg,
		MaxOpenConns: int(pg.GetMaxOpenConns()),
		MaxIdleConns: int(pg.GetMaxIdleConns()),
	})
	if err != nil {
		return nil, nil, err
	}
	store := &AuditStore{client: conn.Client, db: conn.DB}
	return store, func() { _ = conn.Close() }, nil
}

func (s *AuditStore) Enabled() bool {
	return s != nil && s.client != nil
}

func (s *AuditStore) Ping(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("postgres off")
	}
	return s.db.PingContext(ctx)
}

func (s *AuditStore) Write(ctx context.Context, e gateway.Entry) error {
	if !s.Enabled() {
		return errors.New("audit disabled")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_, err := s.client.AuditLog.Create().
		SetActor(clip(e.Actor, 64)).
		SetRole(clip(e.Role, 16)).
		SetAction(clip(e.Action, 64)).
		SetTarget(clip(e.Target, 256)).
		SetDetail(e.Detail).
		SetOk(e.OK).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("audit: %w", err)
	}
	return nil
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
