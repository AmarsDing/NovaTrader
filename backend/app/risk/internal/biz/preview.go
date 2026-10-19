package biz

import (
	"context"

	"server/pkg/brokerif"
	"server/pkg/risk"
)

// Preview 是下单前的干跑试算（G7，见 doc/开发文档/gloomberb对标提升方案.md G7 节）：
// 与 Check 同一条输入装配（行情、时钟、会话、市场、计数）和同一组引擎级守卫
// （配置版本、账户快照就绪、未落地预约、启动恢复、ctx 超时），但**不写审计、不占预约、
// 不计速度与当日笔数**——预览多少次都不改变任何真实下单的结果。
// 金额/费用层面由 pkg/brokerif.PreviewOrder 干跑同一 R01–R15 规则链得出。
func (e *Engine) Preview(ctx context.Context, o risk.Order) brokerif.Preview {
	if s, ok := normSymbol(o.Symbol); ok {
		o.Symbol = s
	}
	// 方向如实透传：脏方向（缺省/错值）交给 brokerif→risk.validate 判 R00，
	// 不在此归并为买单（否则同型请求试算放行、实检被拒，两链结论相反）。
	side := brokerif.Side(string(o.Side))
	req := brokerif.PreviewRequest{
		Order:   brokerif.Order{ClientID: o.ClientID, Symbol: o.Symbol, Side: side, Price: o.Price, Volume: o.Volume},
		Account: o.Account, Source: o.Source, Operator: o.Operator,
	}
	blocked := func(reason string) brokerif.Preview {
		d := risk.Reject(risk.RuleInvalid, reason)
		return brokerif.Preview{Order: req.Order, BlockRule: d.Rule, Reason: d.Reason, Results: d.Results}
	}

	snap, haveSnap := e.quote(ctx, o.Symbol)
	now := e.now()
	in, p := e.input(o, now, snap, haveSnap)
	pv := brokerif.PreviewOrder(in, p, req, p.Fee)

	if o.ExpectedConfigRevision != nil && *o.ExpectedConfigRevision != in.ConfigRevision {
		return blocked("风控尚未加载计划绑定的配置版本")
	}
	if pv.Approved {
		e.mu.RLock()
		state, known := e.accountStates[o.Account]
		unresolved := false
		for _, r := range e.reservations {
			if r.account == o.Account && now.Sub(r.At) > reservationTTL {
				unresolved = true
			}
		}
		e.mu.RUnlock()
		if !known || !state.Ready(now, p.AccountMaxAge) {
			return blocked("账户完整快照缺失、过期、日初权益未知或对账未就绪")
		}
		if o.Side == risk.Buy && unresolved {
			return blocked("已批准委托仍未进入账户账本，请核对原委托")
		}
	}
	if !e.ready.Load() {
		return blocked("风控尚未从库里恢复状态")
	}
	if ctx.Err() != nil {
		return blocked("风控超时")
	}
	return pv
}
