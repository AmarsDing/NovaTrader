// Package kb 是 M04 知识库的业务层：入库、混合检索、信号案例。设计见 doc/开发文档/M04-知识库/设计文档.md。
package kb

import (
	"context"
	"time"

	"server/pkg/pgext"
)

// 取值与 ent Schema 的枚举一致。
var (
	Categories  = []string{"case", "rule", "compliance", "review"}
	SourceTypes = []string{"file", "url", "signal", "manual"}
	// TerminalStatuses 是信号走完的状态，只有这些会生成案例。
	TerminalStatuses = []string{"closed", "expired", "cancelled"}
	Attributions     = []string{"indicator_failed", "news_misread", "unfilled", "emotion_mismatch"}
)

const CategoryCase = "case"

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// Document 是一份文档的一个版本。
type Document struct {
	ID          int
	Category    string
	Title       string
	Source      string
	SourceType  string
	Author      string
	Format      string
	FileName    string
	SizeBytes   int64
	ContentHash string
	Status      string
	Version     int
	StockCodes  []string
	Tags        []string
	ExpiresAt   *time.Time
	ChunkCount  int
	EmbedStatus string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// NewDocument 是待写入的文档。状态、版本由仓储决定。
type NewDocument struct {
	Category    string
	Title       string
	Source      string
	SourceType  string
	Author      string
	Format      string
	FileName    string
	SizeBytes   int64
	ContentHash string
	StockCodes  []string
	Tags        []string
	ExpiresAt   *time.Time
}

// ChunkRow 是待写入的分块。
type ChunkRow struct {
	Seq     int
	Heading string
	Content string
	Tokens  int
	TSV     pgext.TSVector
}

// StoredChunk 是已入库的分块。
type StoredChunk struct {
	ID       int
	Seq      int
	Heading  string
	Content  string
	Tokens   int
	Embedded bool
}

// ChunkText 是待嵌入的分块文本。
type ChunkText struct {
	ID   int
	Text string
}

// SearchFilter 限定检索范围。只查在用、未过期的文档。
type SearchFilter struct {
	Categories []string
	StockCode  string
	DocIDs     []int
	Now        time.Time
}

// Hit 是一条检索结果，带出处。名次为 0 表示该路没有命中。
type Hit struct {
	DocID       int
	ChunkID     int
	Title       string
	Heading     string
	Content     string
	Source      string
	SourceType  string
	Category    string
	Score       float64
	KeywordRank int
	VectorRank  int
}

// CaseRecord 是案例的结构化字段。
type CaseRecord struct {
	Book         string
	SignalID     int64
	StockCode    string
	StockName    string
	Strategy     string
	Pattern      string
	EmotionPhase string
	SignalType   string
	FinalStatus  string
	SignalTime   time.Time
	ClosedAt     time.Time
	EntryPrice   *float64
	ExitPrice    *float64
	PnlPct       *float64
	HoldingDays  *int
	Outcome      string
	Attribution  string
}

// Case 是已入库的案例。
type Case struct {
	ID int
	CaseRecord
	DocID     *int
	CreatedAt time.Time
}

// CaseFilter 只返回 closed_at 早于 AsOf 的案例，避免研判用到未来结果。
type CaseFilter struct {
	Pattern      string
	EmotionPhase string
	StockCode    string
	Strategy     string
	Book         string
	AsOf         time.Time
}

// ListFilter 是文档列表的筛选与分页。
type ListFilter struct {
	Category string
	Status   string
	Offset   int
	Limit    int
}

// KnowledgeRepo 由 data 层用 ent 实现。
type KnowledgeRepo interface {
	FindDocumentByHash(ctx context.Context, hash string) (*Document, error)
	// CreateDocument 在一个事务里写文档和分块，并把同 source 的在用旧版本标成 superseded。
	CreateDocument(ctx context.Context, d NewDocument, chunks []ChunkRow) (*Document, []int, error)
	// ReactivateDocument 重新启用旧版本，同 source 的其他在用版本改为 superseded。
	ReactivateDocument(ctx context.Context, id int) (*Document, error)
	GetDocument(ctx context.Context, id int) (*Document, error)
	DocumentChunks(ctx context.Context, docID int) ([]StoredChunk, error)
	ListDocuments(ctx context.Context, f ListFilter) ([]Document, int, error)
	DeleteDocument(ctx context.Context, id int) error

	SaveEmbeddings(ctx context.Context, model string, vecs map[int][]float32) error
	// PendingEmbeddings 返回在用文档里缺向量或 embed_model 不是 model 的分块。
	PendingEmbeddings(ctx context.Context, model string, limit int) ([]StoredChunk, error)

	KeywordSearch(ctx context.Context, tsquery string, f SearchFilter, limit int) ([]int, error)
	VectorSearch(ctx context.Context, model string, vec []float32, f SearchFilter, limit int) ([]int, error)
	LoadHits(ctx context.Context, chunkIDs []int) (map[int]Hit, error)

	// CreateCase 在一个事务里写案例和案例文档。(book, signal_id) 已存在时 created=false。
	CreateCase(ctx context.Context, c CaseRecord, d NewDocument, chunks []ChunkRow) (caseID, docID int, chunkIDs []int, created bool, err error)
	FindCases(ctx context.Context, f CaseFilter, limit int) ([]Case, error)

	// Purge 把已过 expires_at 的在用文档标成 expired，再删除 cutoff 之前就已失效的文档。
	Purge(ctx context.Context, now, cutoff time.Time) (int, error)
}

// Embedder 是嵌入服务。没有配置时为 nil，检索退化为只用关键词。
type Embedder interface {
	Model() string
	Dims() int
	EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error)
	EmbedQuery(ctx context.Context, text string) ([]float32, error)
}
