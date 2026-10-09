package biz

import (
	"context"
	"time"

	"server/pkg/events"
	"server/pkg/tradecal"
)

// Ready 是数据集完成事件 md.<domain>.ready 的载荷。仓储在写数据的同一事务里写进 outbox。
type Ready struct {
	Domain    string `json:"domain"`
	TradeDate string `json:"trade_date"`
	Count     int    `json:"count"`
	Source    string `json:"source"`
	Stale     bool   `json:"stale"`
}

// Repo 是 datahub 的持久化端口，由 data 层用 ent 实现。全部写入为 upsert，可重复执行。
type Repo interface {
	UpsertSecurities(ctx context.Context, items []Security, source string, ready Ready) (int, error)
	UpsertBars(ctx context.Context, bars []Bar, source string, ready Ready) (int, error)
	UpdateAdjFactors(ctx context.Context, items []AdjFactor, ready Ready) (int, error)
	UpsertLimitPool(ctx context.Context, day time.Time, items []LimitEntry, source string, ready Ready) (int, error)
	UpsertMoneyFlows(ctx context.Context, day time.Time, items []MoneyFlow, source string, ready Ready) (int, error)
	UpsertLhbSeats(ctx context.Context, day time.Time, items []LhbSeat, ready Ready) (int, error)
	UpsertMargins(ctx context.Context, day time.Time, items []Margin, source string, ready Ready) (int, error)
	UpsertHsgtTop10(ctx context.Context, day time.Time, items []HsgtTop, source string, ready Ready) (int, error)
	SyncSectors(ctx context.Context, day time.Time, items []Sector, source string, ready Ready) (int, error)
	UpsertFinance(ctx context.Context, items []FinanceItem, source string, ready Ready) (int, error)
	UpsertOverseas(ctx context.Context, items []OverseasQuote, source string, ready Ready) (int, error)
	UpsertMacro(ctx context.Context, items []MacroPoint, source string, ready Ready) (int, error)
	InsertHotRank(ctx context.Context, at time.Time, items []HotItem, source string) (int, error)

	// ActiveSymbols 返回 day 当天已上市、未退市、未停牌的 A 股，完整率的分母。
	ActiveSymbols(ctx context.Context, day time.Time) ([]string, error)
	// DailyBars 读已入库的日线，对账用。
	DailyBars(ctx context.Context, day time.Time, symbols []string) (map[string]Bar, map[string]string, error)
	// BarSymbols 返回某日某周期已入库的代码，完整率用。
	BarSymbols(ctx context.Context, day time.Time, freq string) ([]string, error)

	SaveIssues(ctx context.Context, issues []Issue) error
	ListIssues(ctx context.Context, day *time.Time, domain string, limit int) ([]StoredIssue, error)
	SaveHealth(ctx context.Context, rows []Health) error
	LoadHealth(ctx context.Context) ([]Health, error)

	Cursor(ctx context.Context, name string) (string, error)
	SetCursor(ctx context.Context, name, value string) error

	CreateBackfill(ctx context.Context, job BackfillJob) (BackfillJob, error)
	UpdateBackfill(ctx context.Context, job BackfillJob) error
	GetBackfill(ctx context.Context, id int) (BackfillJob, error)
	PendingBackfills(ctx context.Context) ([]BackfillJob, error)

	SaveRaw(ctx context.Context, raw RawRecord) error
	PurgeRaw(ctx context.Context, before time.Time) (int, error)

	// DispatchOutbox 投递 outbox 里未发出的事件。
	DispatchOutbox(ctx context.Context, pub Publisher, limit int) (int, error)
}

// StoredIssue 是已入库的质量问题。
type StoredIssue struct {
	ID int
	Issue
	Hits     int
	LastSeen time.Time
}

// BackfillJob 是补数任务。
type BackfillJob struct {
	ID          int
	Domain      string
	Symbols     []string
	Start       time.Time
	End         time.Time
	Source      string
	Status      string
	Total       int
	Done        int
	Rows        int64
	Error       string
	RequestedBy string
	CreatedAt   time.Time
	StartedAt   *time.Time
	FinishedAt  *time.Time
}

// 补数状态。
const (
	JobPending = "pending"
	JobRunning = "running"
	JobSuccess = "success"
	JobFailed  = "failed"
)

// RawRecord 是一条原始响应。
type RawRecord struct {
	Source  string
	Domain  string
	Request string
	Status  int
	Body    []byte
	At      time.Time
}

// SnapshotMeta 是一轮快照的批次信息，写 Redis snap:meta 并随 market.snapshot 发出。
type SnapshotMeta struct {
	AsOf      time.Time `json:"as_of"`
	Source    string    `json:"source"`
	Count     int       `json:"count"`
	LatencyMs int64     `json:"latency_ms"`
	Seq       int64     `json:"seq"`
	Stale     bool      `json:"stale"`
}

// SnapshotStore 是 Redis 上的实时数据。
type SnapshotStore interface {
	WriteSnapshots(ctx context.Context, items []Snapshot, meta SnapshotMeta) error
	MarkStale(ctx context.Context, meta SnapshotMeta) error
	Meta(ctx context.Context) (SnapshotMeta, error)
	WriteSectorQuotes(ctx context.Context, items []SectorQuote) error
	WriteIntradayFlows(ctx context.Context, items []IntradayFlow) error
}

// Publisher 发 NATS 事件。*events.Bus 满足该接口。
type Publisher interface {
	Publish(ctx context.Context, env events.Envelope) error
}

func shanghai() *time.Location { return tradecal.Shanghai() }
