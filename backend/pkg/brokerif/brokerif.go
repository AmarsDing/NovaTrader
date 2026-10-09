// Package brokerif 是模拟撮合和东方财富文件单共用的下单接口。
package brokerif

import "context"

type Side string

const (
	Buy  Side = "buy"
	Sell Side = "sell"
)

type Order struct {
	ClientID string
	Symbol   string
	Side     Side
	Price    float64
	Volume   int
}

type Position struct {
	Symbol    string
	Quantity  int
	Available int
	AvgCost   float64
}

// Adapter 提交委托、撤单和读取持仓。模拟盘与文件单各自实现。
type Adapter interface {
	Submit(ctx context.Context, order Order) (string, error)
	Cancel(ctx context.Context, clientID string) error
	Positions(ctx context.Context) ([]Position, error)
}
