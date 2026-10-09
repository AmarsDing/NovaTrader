// Package risk 是事前风控的纯规则：参数、规则链 R01–R15、盯盘、Kill Switch 状态和速度计数。
// 这里不访问数据库和网络。risk 服务把内存里的账户、行情拼成 Input 再调用 Check；回测直接调用。
package risk

import (
	"fmt"
	"strings"
	"time"

	"server/pkg/tradecal"
)

// Mode 是运行模式。L0 只看，L1 模拟盘，L2 半自动实盘，L3 全自动实盘（保留，按 L2 处理）。
type Mode int

const (
	L0 Mode = iota
	L1
	L2
	L3
)

func (m Mode) String() string {
	return fmt.Sprintf("L%d", int(m))
}

// ParseMode 接受 L0–L3。
func ParseMode(s string) (Mode, error) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "L0":
		return L0, nil
	case "L1":
		return L1, nil
	case "L2":
		return L2, nil
	case "L3":
		return L3, nil
	}
	return L0, fmt.Errorf("risk: invalid mode %q", s)
}

type Side string

const (
	Buy  Side = "buy"
	Sell Side = "sell"
)

// Source 是订单来源。watch 是盯盘触发的卖单，和 auto 一样算自动单。
type Source string

const (
	Auto   Source = "auto"
	Watch  Source = "watch"
	Manual Source = "manual"
)

type AccountType string

const (
	SIM  AccountType = "SIM"
	LIVE AccountType = "LIVE"
)

// Order 是待校验的委托。Sector 由信号带入，用于板块集中度。
type Order struct {
	ClientID string      `json:"client_order_id"`
	Account  AccountType `json:"account_type"`
	Symbol   string      `json:"symbol"`
	Side     Side        `json:"side"`
	Price    float64     `json:"price"`
	Volume   int         `json:"volume"`
	Source   Source      `json:"source"`
	Operator string      `json:"operator,omitempty"`
	Sector   string      `json:"sector,omitempty"`
}

// Quote 是一只股票的最新行情。ListedDays 含上市当日，0 表示不知道。
type Quote struct {
	Symbol     string    `json:"symbol"`
	Last       float64   `json:"last"`
	PrevClose  float64   `json:"prev_close"`
	Bid1       float64   `json:"bid1"`
	Ask1       float64   `json:"ask1"`
	Amount     float64   `json:"amount"`
	PrevAmount float64   `json:"prev_amount"`
	ST         bool      `json:"st"`
	Suspended  bool      `json:"suspended"`
	ListedDays int       `json:"listed_days"`
	Time       time.Time `json:"time"`
}

// Holding 是一只持仓。Available 已扣除当日买入（T+1），FrozenSell 是在途卖单占用。
type Holding struct {
	Symbol      string  `json:"symbol"`
	Quantity    int     `json:"quantity"`
	Available   int     `json:"available"`
	FrozenSell  int     `json:"frozen_sell"`
	AvgCost     float64 `json:"avg_cost"`
	MarketValue float64 `json:"market_value"`
	Sector      string  `json:"sector,omitempty"`
	StopPrice   float64 `json:"stop_price,omitempty"`
	TakePrice   float64 `json:"take_price,omitempty"`
	HeldDays    int     `json:"held_days"`
}

// Sellable 是现在还能再卖的股数。
func (h Holding) Sellable() int {
	n := h.Available - h.FrozenSell
	if n < 0 {
		return 0
	}
	return n
}

// OpenOrder 是柜台上尚未成交的部分。
type OpenOrder struct {
	ClientID string  `json:"client_order_id"`
	Symbol   string  `json:"symbol"`
	Side     Side    `json:"side"`
	Price    float64 `json:"price"`
	Volume   int     `json:"volume"`
	Sector   string  `json:"sector,omitempty"`
}

// Account 是 trade 推来的账户快照。Available 是柜台可用资金，已扣掉在途买单冻结。
type Account struct {
	Type           AccountType        `json:"account_type"`
	Equity         float64            `json:"equity"`
	Available      float64            `json:"available"`
	DayStartEquity float64            `json:"day_start_equity"`
	Holdings       map[string]Holding `json:"holdings"`
	Open           []OpenOrder        `json:"open_orders"`
	AsOf           time.Time          `json:"as_of"`
}

// Reservation 是风控已放行、但账户快照里还看不到的买单。
type Reservation struct {
	ClientID string    `json:"client_order_id"`
	Symbol   string    `json:"symbol"`
	Sector   string    `json:"sector,omitempty"`
	Amount   float64   `json:"amount"`
	At       time.Time `json:"at"`
}

// Market 是全市场状态。Phase、PositionScale、BurstRate 来自 M02 情绪截面（PositionScale 为 0 表示没有，
// 按 Params.PhaseScale 查表）。RiskLevel 0 正常到 3 极高。
type Market struct {
	Phase         string    `json:"phase"`
	PositionScale float64   `json:"position_scale,omitempty"`
	RiskLevel     int       `json:"risk_level"`
	IndexChange   float64   `json:"index_change"`
	Index5mChange float64   `json:"index_5m_change"`
	BurstRate     float64   `json:"burst_rate"`
	Time          time.Time `json:"time"`
}

// Counters 是校验时的计数。BuysToday 是当日已放行买单数；LastSecond、Today 是申报加撤单笔数。
type Counters struct {
	BuysToday  int `json:"buys_today"`
	LastSecond int `json:"last_second"`
	Today      int `json:"today"`
}

// Input 是一次校验看到的全部数据，原样写进审计。
type Input struct {
	Now          time.Time        `json:"now"`
	Session      tradecal.Session `json:"session"`
	Mode         Mode             `json:"mode"`
	KillSwitch   bool             `json:"kill_switch"`
	Breaker      string           `json:"breaker,omitempty"`
	Order        Order            `json:"order"`
	Account      Account          `json:"account"`
	Quote        *Quote           `json:"quote,omitempty"`
	Market       Market           `json:"market"`
	Counters     Counters         `json:"counters"`
	Reservations []Reservation    `json:"reservations,omitempty"`
}

// Result 是一条规则的结论。Volume 是这条规则之后的数量。
type Result struct {
	Rule   string `json:"rule"`
	Name   string `json:"name"`
	Pass   bool   `json:"pass"`
	Note   string `json:"note,omitempty"`
	Volume int    `json:"volume"`
}

// Decision 是校验结果。拒绝时 Rule、Reason 指向第一条失败的规则。
type Decision struct {
	Approved bool     `json:"approved"`
	Volume   int      `json:"volume"`
	Adjusted bool     `json:"adjusted"`
	Rule     string   `json:"rule,omitempty"`
	Reason   string   `json:"reason,omitempty"`
	Results  []Result `json:"results"`
}
