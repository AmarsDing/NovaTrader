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

// PoolStats 返回连接池口径（database/sql.Stats 直通，供 /api/ops/db）。
func (s *AuditStore) PoolStats() (sql.DBStats, bool) {
	if s == nil || s.db == nil {
		return sql.DBStats{}, false
	}
	return s.db.Stats(), true
}

// DBStats 返回库体积、活动连接、慢查询（pg_stat_statements 未装就如实标 available=false）。
// 全部是 pg_catalog 只读查询，不碰业务表。
func (s *AuditStore) DBStats(ctx context.Context) (map[string]any, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("postgres off")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out := map[string]any{}

	var sizeBytes int64
	if err := s.db.QueryRowContext(ctx, `SELECT pg_database_size(current_database())`).Scan(&sizeBytes); err == nil {
		out["size_bytes"] = sizeBytes
		out["size_pretty"] = prettyBytes(sizeBytes)
	}

	var total, active, idle, idleInTx int
	row := s.db.QueryRowContext(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE state = 'active'),
		       count(*) FILTER (WHERE state = 'idle'),
		       count(*) FILTER (WHERE state = 'idle in transaction')
		FROM pg_stat_activity WHERE datname = current_database()`)
	if err := row.Scan(&total, &active, &idle, &idleInTx); err == nil {
		out["connections"] = map[string]int{"total": total, "active": active, "idle": idle, "idle_in_tx": idleInTx}
	}

	var maxConns int
	if err := s.db.QueryRowContext(ctx, `SHOW max_connections`).Scan(&maxConns); err == nil {
		out["max_connections"] = maxConns
	}

	// 慢查询：依赖 pg_stat_statements 扩展。没有就写缺口，不编。
	var hasExt int
	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM pg_extension WHERE extname = 'pg_stat_statements'`).Scan(&hasExt); err != nil || hasExt == 0 {
		out["slow_queries"] = map[string]any{
			"available": false,
			"note":      "pg_stat_statements 扩展未安装：库体积/连接数可用，慢查询需先在 postgresql.conf 启用 shared_preload_libraries 并 CREATE EXTENSION",
		}
	} else {
		out["slow_queries"] = s.slowQueries(ctx)
	}
	return out, nil
}

// slowQueries 取平均耗时前 5 的语句。列名 PG13+ 用 mean_exec_time，旧版 mean_time，先新后旧。
func (s *AuditStore) slowQueries(ctx context.Context) map[string]any {
	type slow struct {
		Query   string  `json:"query"`
		Calls   int64   `json:"calls"`
		MeanMs  float64 `json:"mean_ms"`
		TotalMs float64 `json:"total_ms"`
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT query, calls, mean_exec_time, total_exec_time
		FROM pg_stat_statements WHERE dbid = (SELECT oid FROM pg_database WHERE datname = current_database())
		ORDER BY mean_exec_time DESC LIMIT 5`)
	if err != nil {
		rows, err = s.db.QueryContext(ctx, `
			SELECT query, calls, mean_time, total_time
			FROM pg_stat_statements WHERE dbid = (SELECT oid FROM pg_database WHERE datname = current_database())
			ORDER BY mean_time DESC LIMIT 5`)
		if err != nil {
			return map[string]any{"available": false, "note": "pg_stat_statements 已装但查询失败：" + err.Error()}
		}
	}
	defer rows.Close()
	list := []slow{}
	for rows.Next() {
		var it slow
		if err := rows.Scan(&it.Query, &it.Calls, &it.MeanMs, &it.TotalMs); err == nil {
			list = append(list, it)
		}
	}
	return map[string]any{"available": true, "top": list}
}

// prettyBytes 把字节数转成人读单位（监控牌面直接显示）。
func prettyBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
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
