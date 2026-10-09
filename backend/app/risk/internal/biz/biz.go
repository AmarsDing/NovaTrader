// Package biz 是 risk 服务的业务编排：内存账本、规则链调用、盯盘、Kill Switch、熔断锁存。
// 规则本身在 pkg/risk，这里只负责拼输入和维护状态。
package biz

import (
	"context"
	"time"

	"server/pkg/risk"

	"github.com/google/wire"
)

var ProviderSet = wire.NewSet(NewEngine)

// Repo 是 data 层提供的持久化。写事件的方法要在同一事务里写 outbox。
type Repo interface {
	LoadParams(ctx context.Context) (map[string]string, error)
	// SaveParam 走 pkg/params.Put 生成版本快照，并写一条 kind=param 的 risk_event。
	SaveParam(ctx context.Context, key, value, old, operator string) error
	LastKill(ctx context.Context) (risk.KillState, error)
	LastMode(ctx context.Context) (risk.Mode, bool, error)
	// SaveKill 写 kind=killswitch 事件，并经 outbox 发 sys.killswitch 和 risk.alert。
	SaveKill(ctx context.Context, k risk.KillState, mode risk.Mode) error
	SaveMode(ctx context.Context, mode risk.Mode, operator, reason string) error
	// SaveBreaker 写 kind=breaker 事件，并经 outbox 发 risk.alert。account 为空表示市场类熔断。
	SaveBreaker(ctx context.Context, account risk.AccountType, active bool, reason string) error
	// SaveAlert 写 kind=reject_rate 事件，并经 outbox 发 risk.alert。
	SaveAlert(ctx context.Context, message string) error
	Report(ctx context.Context, from, to time.Time) (*Report, error)
	// RestoreDay 读 from 以来已锁存的账户熔断和已放行的买单数，重启后当日状态不丢。
	RestoreDay(ctx context.Context, from time.Time) (DayState, error)
	// RefData 读 stock_basic 和 day 那天的日线成交额。
	RefData(ctx context.Context, day time.Time) (map[string]RefInfo, error)
}

// DayState 是需要跨重启保留的当日状态。
type DayState struct {
	Breakers map[risk.AccountType]string
	Buys     map[risk.AccountType]int
}

// RefInfo 是盘中不变的个股信息。Industry 在信号和持仓没带板块时补上。
type RefInfo struct {
	Industry   string
	ST         bool
	Suspended  bool
	ListDate   time.Time
	PrevAmount float64
}

// Snapshot 是 Redis snap:latest 里的一条快照（M01 写入），只取风控用到的字段。
type Snapshot struct {
	Symbol   string
	Time     time.Time
	AsOf     time.Time
	Stale    bool
	PreClose float64
	Last     float64
	Amount   float64
	Bid1     float64
	Ask1     float64
}

// QuoteSource 读最新快照。
type QuoteSource interface {
	Quotes(ctx context.Context, symbols []string) (map[string]Snapshot, error)
}

// AuditRecord 是一次校验的审计记录。
type AuditRecord struct {
	Decision risk.Decision
	Input    risk.Input
	Latency  time.Duration
}

// Auditor 异步落审计。Submit 不阻塞，队列满时返回 false。
type Auditor interface {
	Submit(rec AuditRecord) bool
}

// Publisher 直接发事件（不经 outbox），用于 risk.exit。
type Publisher interface {
	Publish(ctx context.Context, subject string, payload any) error
}

// Report 是日度风险报告。
type Report struct {
	Checks, Approved, Rejected, Adjusted int
	Rejects                              map[string]int
	P99Latency                           time.Duration
	Events                               []Event
}

// Event 是一条 risk_event。
type Event struct {
	Kind, Mode, Source, Reason, Operator string
	Active                               bool
	At                                   time.Time
}
