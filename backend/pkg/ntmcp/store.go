package ntmcp

import (
	"context"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-redis/redis/v8"

	"server/ent"
	"server/ent/backtestreport"
	"server/ent/backtesttask"
	"server/ent/position"
	"server/ent/tradecash"
	"server/ent/tradesignal"
	"server/pkg/market"
)

// ============================================================================
// nt-mcp 只读存储面（ent 直连 + Redis 快照读）。
// 纪律：只读——本包只出现 Query 链，绝不出现 Create/Update/Delete/Exec 写链；
// 迁移/建库不归本进程（entclient.Open 带 migrate，nt-mcp 刻意绕开——
// cmd/ntmcp 用 dbinit.BusinessDSN + sql.Open 直连，schema 归属各业务服务）。
// ============================================================================

// Store ent 只读查询面（Positions / Signals / Reports 三接口实现）。
type Store struct {
	client *ent.Client
}

// NewStore 包装既有 ent.Client（连接生命周期归调用方）。
func NewStore(client *ent.Client) *Store { return &Store{client: client} }

// ListPositions 按 book 列持仓（M09 positions 表；symbol 升序稳定输出）。
func (s *Store) ListPositions(ctx context.Context, book string) ([]*ent.Position, error) {
	return s.client.Position.Query().
		Where(position.BookEQ(book)).
		Order(ent.Asc(position.FieldSymbol)).
		All(ctx)
}

// GetCash 按 book 取现金行（单账本一行；缺行=false——如实区分「零现金」与「无账」）。
func (s *Store) GetCash(ctx context.Context, book string) (*ent.TradeCash, bool, error) {
	row, err := s.client.TradeCash.Query().
		Where(tradecash.BookEQ(book)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return row, true, nil
}

// ListSignals 信号列表（M06 trade_signals；id 倒序 = 最新在前，limit 由调用方钳制）。
func (s *Store) ListSignals(ctx context.Context, f SignalFilter) ([]*ent.TradeSignal, error) {
	q := s.client.TradeSignal.Query().
		Where(tradesignal.BookEQ(f.Book)).
		Order(ent.Desc(tradesignal.FieldID))
	if f.Status != "" {
		q = q.Where(tradesignal.StatusEQ(f.Status))
	}
	if f.Side != "" {
		q = q.Where(tradesignal.SignalTypeEQ(tradesignal.SignalType(f.Side)))
	}
	if f.Day != nil {
		q = q.Where(tradesignal.TradeDateEQ(*f.Day))
	}
	if f.Limit > 0 {
		q = q.Limit(f.Limit)
	}
	return q.All(ctx)
}

// GetTask 回测任务行（缺行 → NotFound 错误，调用方如实报）。
func (s *Store) GetTask(ctx context.Context, id int) (*ent.BacktestTask, error) {
	return s.client.BacktestTask.Query().
		Where(backtesttask.IDEQ(id)).
		Only(ctx)
}

// GetReport 回测报告行（task_id 唯一；缺行 → NotFound 错误，调用方如实报）。
func (s *Store) GetReport(ctx context.Context, taskID int) (*ent.BacktestReport, error) {
	return s.client.BacktestReport.Query().
		Where(backtestreport.TaskIDEQ(taskID)).
		Only(ctx)
}

// RedisSnapshots 最新行情快照读面（datahub 写 Redis snap:latest Hash，
// 与 app/market Snapshots.All 同一读取路径——坏行跳过记日志，不拖垮整轮）。
type RedisSnapshots struct {
	rdb *redis.Client
	log *log.Helper
}

// NewRedisSnapshots 包装既有 redis.Client（连接生命周期归调用方）。
func NewRedisSnapshots(rdb *redis.Client, logger log.Logger) *RedisSnapshots {
	return &RedisSnapshots{rdb: rdb, log: log.NewHelper(log.With(logger, "module", "ntmcp/snapshots"))}
}

// All 读全部快照。
func (s *RedisSnapshots) All(ctx context.Context) ([]market.Snapshot, error) {
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
