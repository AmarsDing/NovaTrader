package gateway

import (
	"context"
	"database/sql"
	"net/http"
	"time"
)

// P2 运维只读口（监控驾驶舱业务监控方案 §7）：
// GET /api/ops/db   —— PostgreSQL 连接池 / 库体积 / 慢查询（pg_stat_statements 未装就如实写缺口）
// GET /api/ops/nats —— NATS 连接计数 / JetStream 流与各 durable 消费者 lag

// opsDB 聚合 PG 连接池（database/sql.Stats）与库内省（pg_catalog 只读查询）。
func (g *Gate) opsDB(w http.ResponseWriter, r *http.Request) {
	if _, ok := g.principal(r); !ok {
		writeErr(w, http.StatusUnauthorized, "未登录")
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 3*time.Second)
	defer cancel()
	out := map[string]any{}
	if g.PoolStats != nil {
		if stats, ok := g.PoolStats(); ok {
			out["pool"] = map[string]any{
				"max_open":             stats.MaxOpenConnections,
				"open":                 stats.OpenConnections,
				"in_use":               stats.InUse,
				"idle":                 stats.Idle,
				"wait_count":           stats.WaitCount,
				"wait_ms":              stats.WaitDuration.Milliseconds(),
				"max_idle_closed":      stats.MaxIdleClosed,
				"max_idle_time_closed": stats.MaxIdleTimeClosed,
				"max_lifetime_closed":  stats.MaxLifetimeClosed,
			}
			if stats.MaxOpenConnections > 0 {
				out["pool_used_pct"] = float64(stats.InUse) / float64(stats.MaxOpenConnections) * 100
			}
		}
	}
	if g.DBStats != nil {
		if extra, err := g.DBStats(ctx); err == nil {
			for k, v := range extra {
				out[k] = v
			}
		} else {
			out["db_note"] = err.Error()
		}
	}
	if _, ok := out["pool"]; !ok && len(out) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{
			"available": false,
			"note":      "admin 的 PG 连接不在本进程（审计未启用或 postgres 未配置），无法读取连接池",
		})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// opsNATS 返回消息总线口径。断连时 connected=false，不编数。
func (g *Gate) opsNATS(w http.ResponseWriter, r *http.Request) {
	if _, ok := g.principal(r); !ok {
		writeErr(w, http.StatusUnauthorized, "未登录")
		return
	}
	if g.NATSStats == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"connected": false,
			"note":      "NATS 统计未接线（hub 未注入）",
		})
		return
	}
	writeJSON(w, http.StatusOK, g.NATSStats())
}

// PoolStatsFunc 取连接池统计；DBStatsFunc 取库内省。由 data.AuditStore 提供。
type PoolStatsFunc func() (sql.DBStats, bool)
type DBStatsFunc func(context.Context) (map[string]any, error)
type NATSStatsFunc func() map[string]any
