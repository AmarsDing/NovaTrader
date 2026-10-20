// Package biz 是 datahub 的业务层：数据域、源接口、断路、限频、配额、校验和采集用例。
// 口径见 doc/开发文档/M01-数据采集/设计文档.md。
package biz

import (
	"time"

	"server/pkg/dataset"
	"server/pkg/market"
)

// 数据域名。配置 datahub.domains 的键与这里一致。
const (
	DomainSecurity     = "security"
	DomainDailyBar     = "daily_bar"
	DomainAdjFactor    = "adj_factor"
	DomainMinuteBar    = "minute_bar"
	DomainSnapshot     = "snapshot"
	DomainLimitPool    = "limit_pool"
	DomainMoneyFlow    = "money_flow"
	DomainLhb          = "lhb"
	DomainUnlock       = "unlock"
	DomainBlockTrade   = "block_trade"
	DomainMargin       = "margin"
	DomainMarginMkt    = "margin_mkt"
	DomainHsgtTop10    = "hsgt_top10"
	DomainSector       = "sector"
	DomainSectorQuote  = "sector_quote"
	DomainFinance      = "finance"
	DomainFlash        = "flash"
	DomainNews         = "news"
	DomainAnnouncement = "announcement"
	DomainReport       = "report"
	DomainGuba         = "guba"
	DomainOverseas     = "overseas"
	DomainMacro        = "macro"
	DomainHotRank      = "hot_rank"
)

// 源名。配置 datahub.sources 的键与这里一致。
const (
	SourceTdxLocal   = "tdx_local"
	SourceTdxFile    = "tdx_file"
	SourceTdxTCP     = "tdx_tcp"
	SourceAkshare    = "akshare"
	SourceTushare    = "tushare"
	SourceEastmoney  = "eastmoney"
	SourceSina       = "sina"
	SourceTencent    = "tencent"
	SourceCninfo     = "cninfo"
	SourceTavily     = "tavily"
	SourceRSS        = "rss"
	SourceTdxArchive = "tdx_http_archive"
	SourceOpenBBFred = "openbb_fred"
	// 新浪 7×24 快讯副源：与行情快照源 "sina" 分名，避免断路/健康/配额状态互覆。
	SourceSina7x24 = "sina7x24"
	// Baostock 盘后回补与财务三表（pyworker baostock 角色适配）。
	SourceBaostock = "baostock"
)

// Snapshot 与 M02 共用 pkg/market 的定义，写进 Redis snap:latest。
type Snapshot = market.Snapshot

// SnapshotBatch 是一轮全市场快照。
type SnapshotBatch struct {
	Items []Snapshot
}

// Security 证券基础信息。股本单位股。
type Security struct {
	dataset.Availability
	Symbol     string
	Name       string
	Market     string
	ListDate   *time.Time
	DelistDate *time.Time
	Industry   string
	TotalShare *int64
	FloatShare *int64
	ST         bool
	Suspended  bool
}

// Bar 不复权 K 线。Time 为开始时刻；价格元、成交量股、成交额元。
type Bar struct {
	dataset.Availability
	Symbol   string
	Time     time.Time
	Freq     string
	Open     float64
	High     float64
	Low      float64
	Close    float64
	PreClose *float64
	Volume   int64
	Amount   float64
}

// BarBatch 是一次 K 线拉取的结果。Stale 为真表示源头数据没有到应有交易日。
type BarBatch struct {
	CoverageUnknown bool // The archive row count has no authoritative historical denominator.
	Bars            []Bar
	Stale           bool
	LastDate        string
	Expect          string
}

// BarQuery 是 K 线拉取条件。Symbols 为空表示全市场。Expect 是应有的最新交易日。
type BarQuery struct {
	Freq    string
	Start   time.Time
	End     time.Time
	Expect  time.Time
	Symbols []string
}

// AdjFactor 后复权因子。
type AdjFactor struct {
	dataset.Availability
	Symbol string
	Date   time.Time
	Factor float64
}

// LimitEntry 源头给出的涨停、跌停、炸板池里的一只股票。
type LimitEntry struct {
	Symbol      string
	Pool        string
	Name        string
	Close       *float64
	PctChg      *float64
	Amount      *float64
	FirstSealAt *time.Time
	LastSealAt  *time.Time
	OpenCount   int
	SealAmount  float64
	Consecutive int
	Reason      string
}

// 涨停池类别。
const (
	PoolUp     = "up"
	PoolDown   = "down"
	PoolBroken = "broken"
)

// MoneyFlow 个股资金流向，单位元，正为净流入。
type MoneyFlow struct {
	Symbol   string
	MainNet  float64
	SuperNet float64
	BigNet   float64
	MidNet   float64
	SmallNet float64
}

// LhbSeat 龙虎榜席位。
type LhbSeat struct {
	Symbol     string
	Reason     string
	SeatName   string
	BuyAmount  float64
	SellAmount float64
}

// UnlockPlan 一只股票的一次限售解禁批次（东财数据中心 RPT_LIFT_STAGE）。
// 源头报表按批次汇总不拆股东；Shares 单位万股，与源头一致。
// 比例与市值源没给到就留 nil——没取到不等于 0。
type UnlockPlan struct {
	Symbol         string
	FreeDate       time.Time
	LiftType       string
	Shares         float64 // 万股
	BatchHolderNum *int
	RatioPct       *float64 // 解禁数量占流通股本比例（%）
	TotalRatioPct  *float64 // 解禁数量占总股本比例（%）
	MarketValue    *float64 // 元；源报表一般没有
}

// BlockTrade 一笔大宗交易（东财数据中心 RPT_DATA_BLOCKTRADE，盘后披露）。
// 过渡期落 dataset_record 的 PIT payload（零迁移；ent 窗口开放后再补 current 表），
// 消费方只能按 PIT 口径查，禁止假设存在 block_trade 当前表。
// Volume 万股、Amount 元（源头 TURNOVER 万元×1e4 归一）；溢价率等源可能不给的字段留 nil——没取到≠0。
type BlockTrade struct {
	Symbol       string
	TradeDate    time.Time
	TradePrice   float64
	Volume       float64  // 万股
	Amount       float64  // 元
	Buyer        string   // 买方营业部
	Seller       string   // 卖方营业部
	PremiumRatio *float64 // 溢价率（%）；源缺列保持 nil
	ClosePrice   *float64 // 当日收盘价（算溢价参考）；源缺列保持 nil
}

// Margin 融资融券。
type Margin struct {
	Symbol string
	Rzye   float64
	Rzmre  float64
	Rzche  float64
	Rqye   float64
	Rqmcl  int64
	Rzrqye float64
}

// MarginMkt 全市场两融汇总，交易所维度（SSE/SZSE/BSE 每交易日一行）。
// 与 Margin（个股明细）互补：这里答「全市场杠杆水位」。数据次交易日早上才有，
// 调度在次交易日盘前抓上一交易日（T+1 口径，不算 stale）。
// 数值全指针：源端某列缺发保持 nil 落 NULL——没取到不等于 0（0.数据纪律 §3）。
type MarginMkt struct {
	Exchange string   // SSE/SZSE/BSE
	Rzye     *float64 // 融资余额，元
	Rzmre    *float64 // 融资买入额，元
	Rzche    *float64 // 融资偿还额，元
	Rqye     *float64 // 融券余额，元
	Rqmcl    *int64   // 融券卖出量，股
	Rzrqye   *float64 // 融资融券余额，元
}

// HsgtTop 沪深股通十大成交股。
type HsgtTop struct {
	Symbol    string
	Channel   string
	Rank      int
	Name      string
	Close     *float64
	PctChg    *float64
	Amount    float64
	NetAmount *float64
}

// Sector 板块与成分。
type Sector struct {
	dataset.Availability
	Code    string
	Name    string
	Kind    string
	Members []string
}

// 板块类别，与 sector.kind 一致。
const (
	SectorIndustry = "industry"
	SectorConcept  = "concept"
	SectorStyle    = "style"
)

// SectorQuote 板块实时行情，写 Redis snap:sector。
type SectorQuote struct {
	Code    string    `json:"code"`
	Name    string    `json:"name"`
	Last    float64   `json:"last"`
	PctChg  float64   `json:"pct_chg"`
	Amount  float64   `json:"amount"`
	Up      int       `json:"up"`
	Down    int       `json:"down"`
	Leader  string    `json:"leader"`
	Source  string    `json:"source"`
	AsOf    time.Time `json:"as_of"`
	QuoteAt time.Time `json:"time"`
}

// IntradayFlow 盘中个股主力资金，写 Redis snap:flow。
type IntradayFlow struct {
	Symbol  string    `json:"symbol"`
	MainNet float64   `json:"main_net"`
	Super   float64   `json:"super_net"`
	Big     float64   `json:"big_net"`
	Mid     float64   `json:"mid_net"`
	Small   float64   `json:"small_net"`
	Time    time.Time `json:"time"`
	AsOf    time.Time `json:"as_of"`
	Source  string    `json:"source"`
}

// FinanceItem 业绩预告、快报、指标。
type FinanceItem struct {
	dataset.Availability
	Symbol       string
	EndDate      time.Time
	Kind         string
	AnnDate      time.Time
	ForecastType string
	NetProfit    *float64
	NetProfitYoY *float64
	Summary      string
	Data         map[string]any
}

// 财务类别。
const (
	FinanceForecast  = "forecast"
	FinanceExpress   = "express"
	FinanceIndicator = "indicator"
)

// Intel 是 intel.raw.<kind> 的载荷，字段与 M03 的 biz.Raw 一致。
type Intel struct {
	Source      string   `json:"source"`
	SourceID    string   `json:"source_id"`
	Kind        string   `json:"kind"`
	Title       string   `json:"title"`
	Content     string   `json:"content"`
	URL         string   `json:"url"`
	PublishTime string   `json:"publish_time"`
	Codes       []string `json:"codes"`
	// Forecast 研报盈利预测，仅 kind=report 且源头披露时非空；M03 biz.Raw 有同构镜像。
	Forecast *ReportForecast `json:"forecast,omitempty"`
}

// EPSPoint 一个预测年度的每股收益与市盈率。双 nil 的年度点整条丢弃——没取到≠0。
type EPSPoint struct {
	Year int      `json:"year"`
	EPS  *float64 `json:"eps,omitempty"`
	PE   *float64 `json:"pe,omitempty"`
}

// ReportForecast 研报的盈利预测与评级（东财 reportapi 扩展字段）。全部可选：
// 源头空串=未披露，解析为键缺席（omitempty 指针），绝不写 0。
// RatingChange/ReportType 的枚举语义东财无公开文档，原样透传，口径待标注，不臆造中文名。
type ReportForecast struct {
	EPS          []EPSPoint `json:"eps,omitempty"`
	AimPriceLow  *float64   `json:"aim_price_low,omitempty"`  // 目标价下限，元；实测多数报告为空
	AimPriceHigh *float64   `json:"aim_price_high,omitempty"` // 目标价上限，元
	Rating       string     `json:"rating,omitempty"`         // emRatingName
	LastRating   string     `json:"last_rating,omitempty"`
	RatingChange *int       `json:"rating_change,omitempty"`
	ReportType   *int       `json:"report_type,omitempty"`
	Researcher   string     `json:"researcher,omitempty"`
	Industry     string     `json:"industry,omitempty"` // indvInduName
	OrgCode      string     `json:"org_code,omitempty"`
	OrgName      string     `json:"org_name,omitempty"`  // orgName（机构全称，如「东吴证券研究所」）
	OrgSName     string     `json:"org_sname,omitempty"` // orgSName（签发机构简称，如「东吴证券」；report_estimate.org_sname 的锚）
}

// 情报类别，与 M03 一致。
const (
	KindFlash        = "flash"
	KindNews         = "news"
	KindAnnouncement = "announcement"
	KindReport       = "report"
	KindSocial       = "social"
)

// IntelQuery 增量拉取情报。Since 为上次水位，源头早于它的条目可以不返回。
type IntelQuery struct {
	Since time.Time
	Limit int
}

// OverseasQuote 外围行情。TradeDate 为当地交易日。
type OverseasQuote struct {
	Code      string
	Name      string
	TradeDate time.Time
	Close     float64
	PctChg    float64
	AsOf      time.Time
}

// MacroPoint 宏观序列的一期。
type MacroPoint struct {
	dataset.Availability
	Code  string
	Name  string
	Date  time.Time
	Value float64
	Unit  string
}

// HotItem 热榜一行。
type HotItem struct {
	Board  string
	Rank   int
	Symbol string
	Heat   *float64
}
