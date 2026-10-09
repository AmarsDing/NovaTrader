package biz

import (
	"context"
	"errors"
	"time"

	"server/pkg/events"
)

// ErrDuplicate 表示内容指纹已经在库里。
var ErrDuplicate = errors.New("intel: duplicate content")

// ErrNotFound 表示条目不存在。
var ErrNotFound = errors.New("intel: not found")

// Scored 是一次评分要在同一事务里写下的全部内容。
type Scored struct {
	Item   *Item
	Score  Score
	Links  []Link
	Facts  []Fact
	Stock  string           // 置信度最高的个股
	Event  *events.Envelope // intel.news.scored，只有簇首条有
	Alert  *events.Envelope // intel.alert；簇已告警过时由仓储丢弃
}

// ItemView 是查询接口返回的条目。
type ItemView struct {
	ID              int
	ClusterID       int
	Kind            string
	Source          string
	SourceID        string
	URL             string
	Title           string
	Content         string
	PublishTime     time.Time
	EventType       string
	Sentiment       float64
	Importance      int
	HalfLifeMinutes int
	Scorer          string
	Degraded        bool
	Status          string
	Reason          string
	ClusterSize     int
	Links           []Link
	Facts           []Fact
	Effective       float64
}

type AliasInput struct {
	Alias      string
	StockCode  string
	Kind       string
	Confidence float64
	Enabled    bool
}

// Repo 是 intel 的存储端口，实现只用 ent。
type Repo interface {
	// Find 按来源编号或内容指纹找已有条目。都没有时返回 nil。
	Find(ctx context.Context, source, sourceID, hash string) (*Item, string, error)
	// CreateItem 写入 pending 或 filtered 条目。pending 且 ClusterID 为 0 时新建一簇并把本条设为簇首；
	// ClusterID 非 0 时并入该簇。内容指纹重复返回 ErrDuplicate。成功后回填 ID、ClusterID、Head。
	CreateItem(ctx context.Context, it *Item, status, reason string) error
	// SaveScored 在一个事务里写评分、关联、抽取字段和 outbox。
	// 只有从 pending 转成 scored 时写事件；已经评分过的再次调用直接返回，避免重试发出第二条事件。
	SaveScored(ctx context.Context, s *Scored) error
	Fingerprints(ctx context.Context, since time.Time) ([]Fingerprint, error)
	Pending(ctx context.Context, limit int) ([]*Item, error)
	// SentimentInputs 返回某只股票 since 之后已评分的簇首条。
	SentimentInputs(ctx context.Context, code string, since time.Time) ([]SentimentInput, error)
	Dictionary(ctx context.Context) ([]StockEntry, []AliasEntry, error)
	UpsertAlias(ctx context.Context, a AliasInput) (int, error)
	GetItem(ctx context.Context, id int) (*ItemView, error)
	// Timeline 按发布时间倒序返回关联到 code 的簇首条，不带正文。
	Timeline(ctx context.Context, code string, since, until time.Time, limit int) ([]*ItemView, error)
	// HotCounts 统计 [from, to) 内每个个股、概念、事件类型涉及的簇数。键为 {类型, 对象}。
	HotCounts(ctx context.Context, from, to time.Time) (map[[2]string]int, error)
}
