package brokerif

import (
	"server/pkg/ashare"
	"server/pkg/risk"
)

// PreviewRequest 是一次干跑试算的请求：要下的单 + 走哪个账户、哪类来源。
// Order 沿用适配器下单的同一形状，预览所见即所下。
type PreviewRequest struct {
	Order    Order            `json:"order"`
	Account  risk.AccountType `json:"account"`
	Source   risk.Source      `json:"source"`
	Operator string           `json:"operator,omitempty"`
}

// Preview 是下单前的干跑试算（G7，见 doc/开发文档/gloomberb对标提升方案.md G7 节）：
// 风控预检逐条结果 + 缩量后的数量、预计金额与费用。模拟撮合与东方财富文件单同用这一份，
// UI 交易面板下单前强制展示；本函数不试算运行时守卫（账户快照就绪、在途预约、配置版本），
// 那些是 trade/risk 服务接线 Preview 时在校验链之外补的。
type Preview struct {
	Order Order `json:"order"`
	// Approved 为假时 BlockRule/Reason 给出第一条拦截规则；Volume 为 0。
	Approved  bool   `json:"approved"`
	BlockRule string `json:"block_rule,omitempty"`
	Reason    string `json:"reason,omitempty"`
	// Volume 是风控缩量后的股数；Adjusted 标记相对请求量被缩过。
	Volume   int  `json:"volume"`
	Adjusted bool `json:"adjusted,omitempty"`
	// Amount 预计成交金额（缩量后价×量），Fee 费用估算（佣金+印花税+过户费），
	// CashDelta 现金影响：买入为 -(Amount+Fee)，卖出为 +(Amount-Fee)。
	Amount    float64 `json:"amount"`
	Fee       float64 `json:"fee"`
	CashDelta float64 `json:"cash_delta"`
	// Results 是 R01–R15 逐条预检（沿用 risk.Decision.Results 顺序语义，第一条拒绝即停）。
	Results []risk.Result `json:"results"`
}

// PreviewOrder 干跑一次事前校验：调用方按当时行情/账户/参数装配 risk.Input（与实盘下单
// 同一来源，结果才可信），req 给要下的单，fee 是券商口径（ashare.DefaultFee 为默认）。
// 不落单、不占预约、不改任何状态。
func PreviewOrder(in risk.Input, p risk.Params, req PreviewRequest, fee ashare.Fee) Preview {
	in.Order = toRiskOrder(req)
	d := risk.Check(in, p)
	out := Preview{
		Order:    req.Order,
		Approved: d.Approved,
		Results:  d.Results,
	}
	if !d.Approved {
		out.BlockRule, out.Reason = d.Rule, d.Reason
		return out
	}
	vol := d.Volume
	amount := round2(req.Order.Price * float64(vol))
	f := fee.Cost(amount, req.Order.Side == Sell)
	out.Volume, out.Adjusted = vol, d.Adjusted
	out.Amount, out.Fee = amount, f
	if req.Order.Side == Sell {
		out.CashDelta = round2(amount - f)
	} else {
		out.CashDelta = round2(-(amount + f))
	}
	return out
}

func toRiskOrder(req PreviewRequest) risk.Order {
	side := risk.Buy
	if req.Order.Side == Sell {
		side = risk.Sell
	}
	o := req.Order
	return risk.Order{
		ClientID: o.ClientID, Account: req.Account, Symbol: o.Symbol,
		Side: side, Price: o.Price, Volume: o.Volume, Source: req.Source, Operator: req.Operator,
	}
}

// round2 把正的金额四舍五入到分；ashare.Fee.Cost 已自行取整，这里只管金额口径。
func round2(v float64) float64 {
	if v < 0 {
		return -round2(-v)
	}
	return float64(int64(v*100+0.5)) / 100
}
