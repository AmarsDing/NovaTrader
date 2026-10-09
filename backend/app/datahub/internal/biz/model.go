// Package biz 是 datahub 的业务层：数据域、源接口、断路、限频、配额、校验和采集用例。
// 口径见 doc/开发文档/M01-数据采集/设计文档.md。
package biz

import (
	"time"

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
	DomainMargin       = "margin"
	DomainHsgtTop10    = "hsgt_top10"
	DomainSector       = "sector"
	DomainSectorQuote  = "sector_quote"
	DomainFinance      = "finance"
	DomainFlash        = "flash"
	DomainNews         = "news"
	DomainAnnouncement = "announcement"
	DomainReport       = "report"
	DomainOverseas     = "overseas"
	DomainMacro        = "macro"
	DomainHotRank      = "hot_rank"
)

// 源名。配置 datahub.sources 的键与这里一致。
const (
	SourceTdxLocal  = "tdx_local"
	SourceTdxFile   = "tdx_file"
	SourceTdxTCP    = "tdx_tcp"
	SourceAkshare   = "akshare"
	SourceTushare   = "tushare"
	SourceEastmoney = "eastmoney"
	SourceSina      = "sina"
	SourceTencent   = "tencent"
	SourceCninfo    = "cninfo"
	SourceTavily    = "tavily"
	SourceRSS       = "rss"
)

// Snapshot 与 M02 共用 pkg/market 的定义，写进 Redis snap:latest。
type Snapshot = market.Snapshot

// SnapshotBatch 是一轮全市场快照。
type SnapshotBatch struct {
	Items []Snapshot
}

// Security 证券基础信息。股本单位股。
type Security struct {
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
	Bars     []Bar
	Stale    bool
	LastDate string
	Expect   string
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
}

// 情报类别，与 M03 一致。
const (
	KindFlash        = "flash"
	KindNews         = "news"
	KindAnnouncement = "announcement"
	KindReport       = "report"
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
