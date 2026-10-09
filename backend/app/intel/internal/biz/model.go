package biz

import "time"

// 情报种类，对应主题 intel.raw.<kind>。
const (
	KindFlash        = "flash"
	KindNews         = "news"
	KindAnnouncement = "announcement"
	KindReport       = "report"
	KindSocial       = "social"
)

var kinds = map[string]bool{
	KindFlash: true, KindNews: true, KindAnnouncement: true, KindReport: true, KindSocial: true,
}

// 条目状态。精确重复不入库，所以没有 duplicate 状态。
const (
	StatusPending  = "pending"
	StatusScored   = "scored"
	StatusFiltered = "filtered"
)

// Ingest 的返回状态。
const (
	ResultScored        = "scored"
	ResultFiltered      = "filtered"
	ResultDuplicate     = "duplicate"
	ResultNearDuplicate = "near_duplicate"
)

const (
	TargetStock   = "stock"
	TargetConcept = "concept"
)

const (
	ScorerRule  = "rule"
	ScorerSmall = "small"
	ScorerLarge = "large"
)

// Raw 是 intel.raw.<kind> 的载荷，也是 HTTP 录入的参数。
type Raw struct {
	Source      string   `json:"source"`
	SourceID    string   `json:"source_id"`
	Kind        string   `json:"kind"`
	Title       string   `json:"title"`
	Content     string   `json:"content"`
	URL         string   `json:"url"`
	PublishTime string   `json:"publish_time"`
	Codes       []string `json:"codes"`
}

// Item 是规范化后的一条情报。
type Item struct {
	ID          int
	ClusterID   int
	Head        bool // 簇首条
	Source      string
	SourceID    string
	Kind        string
	Title       string
	Content     string
	URL         string
	Codes       []string // 源头自带，已统一成 600519.SH
	PublishTime time.Time
	TimeGuessed bool
	ReceivedAt  time.Time
	Hash        string
	SimHash     uint64
	RuneLen     int // 参与 SimHash 的字数
}

// Score 是一条情报的评分结果。
type Score struct {
	EventType       string
	Sentiment       float64
	Importance      int
	HalfLifeMinutes int
	Reason          string
	Scorer          string
	Degraded        bool
}

type Link struct {
	TargetType string
	Target     string
	Confidence float64
	Method     string
	Matched    string
}

type Fact struct {
	Type  string
	Value *float64
	Unit  string
	Text  string
	Start int
	End   int
}

// Fingerprint 是去重索引里的一项，启动时从库里预热。
type Fingerprint struct {
	SimHash   uint64
	ClusterID int
	RuneLen   int
	Codes     []string
	At        time.Time
}

// SentimentInput 是计算个股综合情感用的一条已评分簇首条。
type SentimentInput struct {
	Sentiment       float64
	Importance      int
	HalfLifeMinutes int
	PublishTime     time.Time
	Confidence      float64
}
