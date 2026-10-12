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
	"server/ent/auditlog"
	"server/pkg/dbinit"
	"server/pkg/entclient"
	"server/pkg/params"

	"github.com/go-kratos/kratos/v2/log"
)

// AuditStore 把网关审计写进 audit_log。库连不上时 Enabled 为 false，admin 仍可启动。
// 同一份连接配置也供 Backup（backend 侧的 pg_dump 导出）使用。
type AuditStore struct {
	client *ent.Client
	db     *sql.DB
	pg     *conf.Postgres
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
	store := &AuditStore{client: conn.Client, db: conn.DB, pg: pg}
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

// List 按动作/时间倒序翻页读审计；action 为空 = 全部动作。
func (s *AuditStore) List(ctx context.Context, action string, limit int) ([]gateway.Row, error) {
	if !s.Enabled() {
		return nil, errors.New("audit disabled")
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := s.client.AuditLog.Query()
	if action != "" {
		q = q.Where(auditlog.ActionEQ(action))
	}
	rows, err := q.Order(ent.Desc(auditlog.FieldID)).Limit(limit).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("audit list: %w", err)
	}
	out := make([]gateway.Row, 0, len(rows))
	for _, r := range rows {
		out = append(out, gateway.Row{
			ID:        r.ID,
			Actor:     r.Actor,
			Role:      r.Role,
			Action:    r.Action,
			Target:    r.Target,
			Detail:    r.Detail,
			OK:        r.Ok,
			CreatedAt: r.CreatedAt,
		})
	}
	return out, nil
}

// LoadParams 读出 strategy_config 的当前值。配置页用它显示选股和行情阈值。
func (s *AuditStore) LoadParams(ctx context.Context) (map[string]string, error) {
	if !s.Enabled() {
		return nil, errors.New("config disabled")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	rows, err := s.client.StrategyConfig.Query().All(ctx)
	if err != nil {
		return nil, fmt.Errorf("config load: %w", err)
	}
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		out[row.ConfigKey] = row.ConfigValue
	}
	return out, nil
}

// SaveParams 把配置页提交的键写入 strategy_config，并生成一个新版本。
func (s *AuditStore) SaveParams(ctx context.Context, values map[string]string) error {
	if !s.Enabled() {
		return errors.New("config disabled")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, _, err := params.PutMany(ctx, s.client, values, "string", "配置页")
	return err
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
