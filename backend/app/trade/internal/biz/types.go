package biz

import (
	"context"
	"time"
)

// Order 是一张委托。Account 对外用 SIM/LIVE，库里用 paper/live。
type Order struct {
	ClientOrderID   string
	Book            string
	Symbol          string
	Side            string
	Status          string
	Price           float64
	Volume          int
	Filled          int
	Source          string
	Operator        string
	SignalID        string
	StrategyVersion string
	// W5a：幂等键与参考价随单走（内存/durable 进程内透传；落库列追加随 data 批次）。
	IntentID       string
	ReferencePrice float64
	Reason         string
	Channel        string
	CreatedAt      time.Time
}

type Fill struct {
	ClientOrderID   string  `json:"client_order_id"`
	Book            string  `json:"book"`
	Symbol          string  `json:"symbol"`
	Side            string  `json:"side"`
	Qty             int     `json:"qty"`
	Price           float64 `json:"price"`
	Amount          float64 `json:"amount"`
	Fee             float64 `json:"fee"`
	SignalID        string  `json:"signal_id"`
	StrategyVersion string  `json:"strategy_version"`
	// ExecID 是券商成交回报的柜台成交号；SIM 撮合成交恒为空。
	ExecID    string    `json:"exec_id"`
	CreatedAt time.Time `json:"created_at"`
}

type Trail struct {
	ClientOrderID string
	Status        string
	Note          string
}

type Cash struct {
	Book           string
	Cash           float64
	RealizedPnL    float64
	PaperDays      int
	OpenHalted     bool
	HaltReason     string
	DayStartDate   string
	DayStartEquity float64
}

type Position struct {
	Book      string
	Symbol    string
	Quantity  int
	Available int
	AvgCost   float64
	LastPrice float64
}

type AccountView struct {
	Book          string
	Cash          float64
	Equity        float64
	RealizedPnL   float64
	UnrealizedPnL float64
	PositionCount int
	PaperDays     int
	OpenHalted    bool
	HaltReason    string
}

type PlaceRequest struct {
	ExpectedConfigRevision *int
	FinalCheck             func(context.Context) error
	BusinessKey            string
	PlanID                 string
	PlanRevision           int
	ClientOrderID          string
	// W5a 幂等键：book+intent_id 为服务端持久去重身份（见 placeDurable）。
	IntentID string
	// W5a 参考价：R 倍数/偏离检查锚；0=缺省。
	ReferencePrice  float64
	Account         string
	Symbol          string
	Side            string
	Price           float64
	Volume          int
	Source          string
	Operator        string
	SignalID        string
	StrategyVersion string
	Quote           *Quote
}

// Quote 是撮合用的一根不复权 K 线。
type Quote struct {
	Time                   time.Time // Bar start; runtime fills may only use bars starting after the order.
	Open, High, Low, Close float64
	Volume                 int64
	LimitUp, LimitDown     float64
}

type RemotePosition struct {
	Symbol    string
	Quantity  int
	Available int
}

func accountOf(book string) string {
	if book == BookLive {
		return "LIVE"
	}
	return "SIM"
}

func bookOf(account string) (string, error) {
	switch account {
	case "SIM", "sim", "paper", "PAPER", "":
		return BookPaper, nil
	case "LIVE", "live":
		return BookLive, nil
	default:
		return "", ErrInvalid
	}
}
