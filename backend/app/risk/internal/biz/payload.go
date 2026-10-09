package biz

import (
	"time"

	"server/pkg/risk"
)

// 以下是 risk 订阅和发布的事件载荷，字段名以 M08 设计文档第 6 节为准。

// AccountPayload 是 trade.account：trade 每 10 秒推一次账户快照。
type AccountPayload struct {
	AccountType    risk.AccountType `json:"account_type"`
	Equity         float64          `json:"equity"`
	Available      float64          `json:"available"`
	DayStartEquity float64          `json:"day_start_equity"`
	Holdings       []risk.Holding   `json:"holdings"`
	OpenOrders     []risk.OpenOrder `json:"open_orders"`
	AsOf           time.Time        `json:"as_of"`
}

// OrderActionPayload 是 trade.order 里风控关心的部分。action=cancel 计入速度上限。
type OrderActionPayload struct {
	ClientID    string           `json:"client_order_id"`
	AccountType risk.AccountType `json:"account_type"`
	Action      string           `json:"action"`
}

// SnapshotMetaPayload 是 market.snapshot（M01 每轮快照写完后发）。全市场心跳，持仓停牌时也能证明行情还在。
type SnapshotMetaPayload struct {
	AsOf  time.Time `json:"as_of"`
	Count int       `json:"count"`
	Stale bool      `json:"stale"`
}

// StatePayload 是 market.state（M02 pkg/market.State）。index_pct 单位是百分数，broken_rate 是 0–1。
type StatePayload struct {
	AsOf       time.Time `json:"as_of"`
	Phase      string    `json:"phase"`
	RiskLevel  int       `json:"risk_level"`
	IndexPct   float64   `json:"index_pct"`
	Index5m    float64   `json:"index_pct_5m"`
	BrokenRate float64   `json:"broken_rate"`
	Position   float64   `json:"position_scale"`
	Stale      bool      `json:"stale"`
}

// IntelAlertPayload 是 intel.alert（M03）里风控用到的字段。
type IntelAlertPayload struct {
	Title     string   `json:"title"`
	Level     string   `json:"level"`
	Stocks    []string `json:"stocks"`
	Sentiment float64  `json:"sentiment"`
}

// KillRequestPayload 是 sys.killswitch.request。
type KillRequestPayload struct {
	Source string `json:"source"`
	Reason string `json:"reason"`
}

// KillSwitchPayload 是 sys.killswitch，只有 risk 发。
type KillSwitchPayload struct {
	Active   bool      `json:"active"`
	Source   string    `json:"source"`
	Reason   string    `json:"reason"`
	Operator string    `json:"operator,omitempty"`
	Mode     string    `json:"mode"`
	At       time.Time `json:"at"`
}

// ExitPayload 是 risk.exit：盯盘卖出请求。trade 生成卖单后仍走 CheckOrder（source=watch）。
type ExitPayload struct {
	ExitID string `json:"exit_id"`
	risk.Exit
	At time.Time `json:"at"`
}

// AlertPayload 是 risk.alert。
type AlertPayload struct {
	Level       string           `json:"level"`
	Kind        string           `json:"kind"`
	AccountType risk.AccountType `json:"account_type,omitempty"`
	Message     string           `json:"message"`
	At          time.Time        `json:"at"`
}
