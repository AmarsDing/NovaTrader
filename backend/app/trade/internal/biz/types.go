package biz

import "time"

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
	Reason          string
	Channel         string
	CreatedAt       time.Time
}

type Fill struct {
	ClientOrderID   string
	Book            string
	Symbol          string
	Side            string
	Qty             int
	Price           float64
	Amount          float64
	Fee             float64
	SignalID        string
	StrategyVersion string
	CreatedAt       time.Time
}

type Trail struct {
	ClientOrderID string
	Status        string
	Note          string
}

type Cash struct {
	Book        string
	Cash        float64
	RealizedPnL float64
	PaperDays   int
	OpenHalted  bool
	HaltReason  string
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
	ClientOrderID   string
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
