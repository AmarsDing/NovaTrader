// Package sim 是内存里的模拟账户。成交价由调用方给出，手数和 T+1 用 pkg/ashare。
package sim

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"server/pkg/ashare"
	"server/pkg/brokerif"
)

type orderState struct {
	order  brokerif.Order
	status string
	filled int
}

// Book 记录委托和持仓。Fill 在下一根行情到来时调用。
type Book struct {
	mu     sync.Mutex
	orders map[string]*orderState
	pos    map[string]*brokerif.Position
	bought map[string]int
}

func New() *Book {
	return &Book{
		orders: map[string]*orderState{},
		pos:    map[string]*brokerif.Position{},
		bought: map[string]int{},
	}
}

func (b *Book) Submit(_ context.Context, order brokerif.Order) (string, error) {
	if order.ClientID == "" || order.Volume <= 0 || order.Price <= 0 {
		return "", fmt.Errorf("sim: invalid order")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.orders[order.ClientID]; ok {
		return "", fmt.Errorf("sim: duplicate client id %s", order.ClientID)
	}
	if order.Side == brokerif.Buy {
		n, err := ashare.RoundBuy(order.Symbol, order.Volume)
		if err != nil {
			return "", err
		}
		if n != order.Volume {
			return "", fmt.Errorf("sim: buy volume %d is not a valid lot", order.Volume)
		}
	}
	if order.Side == brokerif.Sell {
		have := 0
		if p := b.pos[order.Symbol]; p != nil {
			have = p.Available
		}
		ok, err := ashare.CanSell(order.Symbol, have, order.Volume)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", fmt.Errorf("sim: cannot sell %d of %s", order.Volume, order.Symbol)
		}
	}
	b.orders[order.ClientID] = &orderState{order: order, status: "accepted"}
	return order.ClientID, nil
}

func (b *Book) Cancel(_ context.Context, clientID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.orders[clientID]
	if !ok {
		return fmt.Errorf("sim: unknown order %s", clientID)
	}
	if st.status == "filled" {
		return fmt.Errorf("sim: order %s already filled", clientID)
	}
	st.status = "cancelled"
	return nil
}

// Fill 按给定价格整单成交一笔尚未撤销的委托。
func (b *Book) Fill(clientID string, price float64) error {
	b.mu.Lock()
	st, ok := b.orders[clientID]
	b.mu.Unlock()
	if !ok {
		return fmt.Errorf("sim: unknown order %s", clientID)
	}
	return b.FillQty(clientID, st.order.Volume-st.filled, price)
}

// FillQty 成交其中 qty 股。未成交完的委托状态为 partial，可以继续成交或撤单。
func (b *Book) FillQty(clientID string, qty int, price float64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.orders[clientID]
	if !ok {
		return fmt.Errorf("sim: unknown order %s", clientID)
	}
	if st.status != "accepted" && st.status != "partial" {
		return fmt.Errorf("sim: order %s is %s", clientID, st.status)
	}
	if qty <= 0 || qty > st.order.Volume-st.filled {
		return fmt.Errorf("sim: fill %d exceeds remaining %d", qty, st.order.Volume-st.filled)
	}
	p := b.pos[st.order.Symbol]
	if p == nil {
		p = &brokerif.Position{Symbol: st.order.Symbol}
		b.pos[st.order.Symbol] = p
	}
	switch st.order.Side {
	case brokerif.Buy:
		totalCost := p.AvgCost*float64(p.Quantity) + price*float64(qty)
		p.Quantity += qty
		p.AvgCost = totalCost / float64(p.Quantity)
		b.bought[st.order.Symbol] += qty
	case brokerif.Sell:
		if qty > p.Available {
			return fmt.Errorf("sim: sell %d exceeds available %d", qty, p.Available)
		}
		p.Quantity -= qty
		if p.Quantity == 0 {
			p.AvgCost = 0
		}
	default:
		return fmt.Errorf("sim: unknown side")
	}
	p.Available = ashare.Sellable(p.Quantity, b.bought[st.order.Symbol])
	st.filled += qty
	if st.filled == st.order.Volume {
		st.status = "filled"
	} else {
		st.status = "partial"
	}
	return nil
}

// Position 返回一只股票的持仓；没有时返回零值。
func (b *Book) Position(symbol string) brokerif.Position {
	b.mu.Lock()
	defer b.mu.Unlock()
	if p := b.pos[symbol]; p != nil {
		return *p
	}
	return brokerif.Position{Symbol: symbol}
}

// RollDay 进入下一交易日，当日买入变为可卖。
func (b *Book) RollDay() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.bought = map[string]int{}
	for _, p := range b.pos {
		p.Available = p.Quantity
	}
}

// Positions 按代码排序返回非空持仓。
func (b *Book) Positions(context.Context) ([]brokerif.Position, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]brokerif.Position, 0, len(b.pos))
	for _, p := range b.pos {
		if p.Quantity > 0 {
			out = append(out, *p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Symbol < out[j].Symbol })
	return out, nil
}
