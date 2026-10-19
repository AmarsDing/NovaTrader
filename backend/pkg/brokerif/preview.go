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
	in.Order = mergeOrder(in.Order, req)
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

// mergeOrder 把请求里的委托覆盖到调用方装配的 in.Order 上。风控依赖的装配字段
// （Sector 由引擎侧 ref 表回填、ExpectedConfigRevision 由计划绑定，请求形状上都不带）
// 必须原样保留——整体重建会把它们清零，R09 板块集中这类按回填字段判的规则会在
// 试算链静默放行，而同笔单在实检被缩/被拒（试算所见必须即所下）。
func mergeOrder(assembled risk.Order, req PreviewRequest) risk.Order {
	o := assembled
	o.ClientID = req.Order.ClientID
	o.Account = req.Account
	o.Symbol = req.Order.Symbol
	// 方向如实透传：Buy/Sell 明示，其余原样交给 risk.validate 判 R00。
	// 既往「非卖即买」的归并会让脏方向（缺省/错值）在试算链被当买单放行，
	// 同型请求打实检却被 R00 拒——两链结论相反。
	o.Side = risk.Side(string(req.Order.Side))
	o.Price = req.Order.Price
	o.Volume = req.Order.Volume
	o.Source = req.Source
	o.Operator = req.Operator
	return o
}

// round2 把正的金额四舍五入到分；ashare.Fee.Cost 已自行取整，这里只管金额口径。
func round2(v float64) float64 {
	if v < 0 {
		return -round2(-v)
	}
	return float64(int64(v*100+0.5)) / 100
}
