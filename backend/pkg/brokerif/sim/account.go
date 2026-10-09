package sim

import (
	"context"
	"fmt"
	"math"

	"server/pkg/ashare"
	"server/pkg/brokerif"
	"server/pkg/symbol"
)

// Account 是带资金和费用的模拟账户。持仓与 T+1 由 Book 维护。
type Account struct {
	Book *Book
	Cash float64
	Fee  ashare.Fee
}

func NewAccount(cash float64, fee ashare.Fee) *Account {
	return &Account{Book: New(), Cash: cash, Fee: fee}
}

// Execution 是一笔已入账的成交。
type Execution struct {
	Qty    int
	Price  float64
	Amount float64
	Fee    float64
}

// Affordable 返回按 price 买入时资金够买的最大合法股数（含费用），不超过 want。
func (a *Account) Affordable(sym string, want int, price float64) int {
	if price <= 0 || want <= 0 || a.Cash <= 0 {
		return 0
	}
	if most := int(math.Floor(a.Cash / price)); most < want {
		want = most
	}
	n, err := ashare.RoundBuy(sym, want)
	if err != nil {
		return 0
	}
	lot := 100
	if code, err := symbol.Parse(sym); err == nil && ashare.IsSTAR(code.Code) {
		lot = 1
	}
	for n > 0 {
		amount := price * float64(n)
		if amount+a.Fee.Cost(amount, false) <= a.Cash+1e-6 {
			return n
		}
		n, _ = ashare.RoundBuy(sym, n-lot)
	}
	return 0
}

// Buy 提交并立即成交一笔买入，扣除成交额和费用。资金不足时返回错误，不改账户。
func (a *Account) Buy(ctx context.Context, clientID, sym string, qty int, price float64) (Execution, error) {
	amount := price * float64(qty)
	fee := a.Fee.Cost(amount, false)
	if amount+fee > a.Cash+1e-6 {
		return Execution{}, fmt.Errorf("sim: cash %.2f < %.2f", a.Cash, amount+fee)
	}
	if _, err := a.Book.Submit(ctx, brokerif.Order{ClientID: clientID, Symbol: sym, Side: brokerif.Buy, Price: price, Volume: qty}); err != nil {
		return Execution{}, err
	}
	if err := a.Book.FillQty(clientID, qty, price); err != nil {
		return Execution{}, err
	}
	a.Cash -= amount + fee
	return Execution{Qty: qty, Price: price, Amount: amount, Fee: fee}, nil
}

// Sell 提交并立即成交一笔卖出，入账成交额减费用（含印花税）。
func (a *Account) Sell(ctx context.Context, clientID, sym string, qty int, price float64) (Execution, error) {
	if _, err := a.Book.Submit(ctx, brokerif.Order{ClientID: clientID, Symbol: sym, Side: brokerif.Sell, Price: price, Volume: qty}); err != nil {
		return Execution{}, err
	}
	if err := a.Book.FillQty(clientID, qty, price); err != nil {
		return Execution{}, err
	}
	amount := price * float64(qty)
	fee := a.Fee.Cost(amount, true)
	a.Cash += amount - fee
	return Execution{Qty: qty, Price: price, Amount: amount, Fee: fee}, nil
}

// Credit 记入非交易现金，例如除权等值入账。
func (a *Account) Credit(v float64) { a.Cash += v }
