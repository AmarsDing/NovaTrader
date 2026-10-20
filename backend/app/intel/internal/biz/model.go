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
	// Forecast 研报盈利预测，仅 kind=report 且源头披露时非空；与 datahub biz.Intel 同构镜像。
	Forecast *ReportForecast `json:"forecast,omitempty"`
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
	Forecast    *ReportForecast // 研报盈利预测镜像（prepare 原样透传，不加工）；是否落 dataset_record 由 data 层判缺失
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

// EPSPoint 一个预测年度的每股收益与市盈率。双 nil 的年度点整条丢弃——没取到≠0。
// 与 M01 datahub biz/model.go 的 EPSPoint 同构：跨 app 不互相 import，
// 按现有 Intel/Raw 重复维护的先例手工对齐，改动必须两侧同步。
type EPSPoint struct {
	Year int      `json:"year"`
	EPS  *float64 `json:"eps,omitempty"`
	PE   *float64 `json:"pe,omitempty"`
}

// ReportForecast 研报盈利预测与评级（东财 reportapi 扩展字段）的同构镜像，
// 源头定义在 datahub biz/model.go，字段与 JSON tag 逐字对齐。全部可选：
// 源头空串=未披露，解析为键缺席（omitempty 指针），绝不写 0。
// RatingChange/ReportType 的枚举语义东财无公开文档，原样透传，口径待标注，不臆造中文名。
type ReportForecast struct {
	EPS          []EPSPoint `json:"eps,omitempty"`
	AimPriceLow  *float64   `json:"aim_price_low,omitempty"` // 目标价下限，元；实测多数报告为空
	AimPriceHigh *float64   `json:"aim_price_high,omitempty"`
	Rating       string     `json:"rating,omitempty"` // emRatingName
	LastRating   string     `json:"last_rating,omitempty"`
	RatingChange *int       `json:"rating_change,omitempty"`
	ReportType   *int       `json:"report_type,omitempty"`
	Researcher   string     `json:"researcher,omitempty"`
	Industry     string     `json:"industry,omitempty"` // indvInduName
	OrgCode      string     `json:"org_code,omitempty"`
	OrgName      string     `json:"org_name,omitempty"`  // orgName（机构全称，如「东吴证券研究所」）
	OrgSName     string     `json:"org_sname,omitempty"` // orgSName（签发机构简称，如「东吴证券」；report_estimate.org_sname 的锚）
}
