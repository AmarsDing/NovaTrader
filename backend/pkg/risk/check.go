package risk

import (
	"fmt"
	"math"
	"strings"

	"server/pkg/ashare"
	"server/pkg/symbol"
	"server/pkg/tradecal"
)

// RuleInvalid 是输入不完整或审计不可用时的拒绝编号，不属于 R01–R15。
const RuleInvalid = "R00"

type outcome struct {
	ok     bool
	volume int
	note   string
}

func pass(vol int) outcome                  { return outcome{ok: true, volume: vol} }
func passNote(vol int, note string) outcome { return outcome{ok: true, volume: vol, note: note} }
func reject(format string, a ...any) outcome {
	return outcome{note: fmt.Sprintf(format, a...)}
}

type rule struct {
	id   string
	name string
	fn   func(in *Input, p *Params, vol int) outcome
}

// chain 的顺序就是需求文档的顺序，不能调整。
var chain = []rule{
	{"R01", "授权", ruleAuth},
	{"R02", "行情新鲜", ruleFresh},
	{"R03", "可交易", ruleTradable},
	{"R04", "涨跌停与价格笼子", rulePrice},
	{"R05", "手数", ruleLot},
	{"R06", "可卖数量", ruleSellable},
	{"R07", "单票仓位", ruleSingle},
	{"R08", "总仓", ruleTotal},
	{"R09", "板块集中", ruleSector},
	{"R10", "当日开仓笔数", ruleDailyBuys},
	{"R11", "流动性", ruleLiquidity},
	{"R12", "资金", ruleCash},
	{"R13", "禁止自成交", ruleSelfTrade},
	{"R14", "速度上限", ruleRate},
	{"R15", "熔断", ruleBreaker},
}

// RuleName 返回规则编号对应的名称。
func RuleName(id string) string {
	if id == RuleInvalid {
		return "输入无效"
	}
	for _, r := range chain {
		if r.id == id {
			return r.name
		}
	}
	return ""
}

// Check 按固定顺序执行规则，第一条拒绝即停止。
// 仓位、流动性、资金超限时缩量放行，缩量后的数量写在 Decision.Volume 和对应规则的 Note 里。
// 持仓表的键和 Order.Symbol 都按 600519.SH 形式比较。
func Check(in Input, p Params) Decision {
	if msg := validate(in.Order); msg != "" {
		return Reject(RuleInvalid, msg)
	}
	sym, _ := symbol.Parse(in.Order.Symbol)
	in.Order.Symbol = sym.Tongdaxin()
	vol := in.Order.Volume
	d := Decision{Results: make([]Result, 0, len(chain))}
	for _, r := range chain {
		out := r.fn(&in, &p, vol)
		d.Results = append(d.Results, Result{Rule: r.id, Name: r.name, Pass: out.ok, Note: out.note, Volume: out.volume})
		if !out.ok {
			d.Rule = r.id
			d.Reason = r.name + "：" + out.note
			return d
		}
		vol = out.volume
	}
	d.Approved = true
	d.Volume = vol
	d.Adjusted = vol != in.Order.Volume
	return d
}

// Reject 生成一条不经过规则链的拒绝，供服务在超时、审计不可用时使用。
func Reject(rule, reason string) Decision {
	return Decision{Rule: rule, Reason: reason, Results: []Result{{Rule: rule, Name: RuleName(rule), Note: reason}}}
}

func validate(o Order) string {
	switch {
	case strings.TrimSpace(o.ClientID) == "":
		return "缺少 client_order_id"
	case o.Side != Buy && o.Side != Sell:
		return fmt.Sprintf("方向无效 %q", o.Side)
	case o.Volume <= 0:
		return "数量必须大于 0"
	case o.Account != SIM && o.Account != LIVE:
		return fmt.Sprintf("账户类型无效 %q", o.Account)
	case o.Source != Auto && o.Source != Watch && o.Source != Manual:
		return fmt.Sprintf("来源无效 %q", o.Source)
	}
	if _, err := symbol.Parse(o.Symbol); err != nil {
		return "证券代码无效"
	}
	return ""
}

func isAuto(o Order) bool { return o.Source != Manual }

func ruleAuth(in *Input, p *Params, vol int) outcome {
	o := in.Order
	if o.Source == Manual && strings.TrimSpace(o.Operator) == "" {
		return reject("人工单缺少操作人")
	}
	manualSell := o.Source == Manual && o.Side == Sell
	if in.KillSwitch {
		if manualSell {
			return passNote(vol, "Kill Switch 已触发，人工卖出放行")
		}
		return reject("Kill Switch 已触发，只允许人工卖出")
	}
	if manualSell {
		return pass(vol)
	}
	switch o.Account {
	case SIM:
		if in.Mode < L1 {
			return reject("当前模式 %s 不允许下单", in.Mode)
		}
	case LIVE:
		if in.Mode < L2 {
			return reject("当前模式 %s 不允许实盘", in.Mode)
		}
		if !p.LiveAdmitted {
			return reject("实盘准入未通过")
		}
	}
	return pass(vol)
}

func ruleFresh(in *Input, p *Params, vol int) outcome {
	if !isAuto(in.Order) {
		return pass(vol)
	}
	if in.Quote == nil || in.Quote.Time.IsZero() {
		return reject("没有行情")
	}
	if age := in.Now.Sub(in.Quote.Time); age > p.QuoteMaxDelay {
		return reject("行情延迟 %d 毫秒，超过 %d", age.Milliseconds(), p.QuoteMaxDelay.Milliseconds())
	}
	return pass(vol)
}

func orderPhase(ph tradecal.Phase) bool {
	switch ph {
	case tradecal.CallAuction, tradecal.PreMatch, tradecal.AMTrading, tradecal.PMTrading:
		return true
	}
	return false
}

func ruleTradable(in *Input, p *Params, vol int) outcome {
	o, q := in.Order, in.Quote
	if !orderPhase(in.Session.Phase) {
		return reject("时段 %s 不接受委托", in.Session.Phase)
	}
	if q == nil || q.PrevClose <= 0 {
		return reject("没有行情或前收盘")
	}
	if q.Suspended {
		return reject("停牌")
	}
	if o.Side == Sell {
		return pass(vol)
	}
	sym, _ := symbol.Parse(o.Symbol)
	if p.Blacklist[sym.Tongdaxin()] {
		return reject("在黑名单")
	}
	if q.ST && !p.AllowSTBuy {
		return reject("ST 股不买入")
	}
	if q.ListedDays > 0 && q.ListedDays <= p.MinListDays {
		return reject("上市第 %d 个交易日，不满 %d 日不买入", q.ListedDays, p.MinListDays)
	}
	if q.ListedDays == 0 {
		return passNote(vol, "上市日期未知")
	}
	return pass(vol)
}

// LimitPrices 返回当日涨停价、跌停价。上市前 5 日不设涨跌幅时 ok 为 false。
func LimitPrices(in *Input) (up, down float64, ok bool, err error) {
	q := in.Quote
	if q.ListedDays > 0 && q.ListedDays <= ashare.NoLimitDays {
		return 0, 0, false, nil
	}
	ratio, err := ashare.RatioOn(in.Order.Symbol, q.ST, in.Now)
	if err != nil {
		return 0, 0, false, err
	}
	up, down = ashare.Limit(q.PrevClose, ratio)
	return up, down, true, nil
}

// Continuous 判断此刻是否在连续竞价。收盘集合竞价（14:57 起）不适用价格笼子。
func Continuous(in *Input) bool {
	switch in.Session.Phase {
	case tradecal.AMTrading:
		return true
	case tradecal.PMTrading:
		t := in.Now.In(tradecal.Shanghai())
		return t.Hour()*60+t.Minute() < 14*60+57
	}
	return false
}

func rulePrice(in *Input, p *Params, vol int) outcome {
	o, q := in.Order, in.Quote
	if o.Price <= 0 || !ashare.OnTick(o.Price) {
		return reject("价格 %v 不是有效的 0.01 元价位", o.Price)
	}
	up, down, limited, err := LimitPrices(in)
	if err != nil {
		return reject("%v", err)
	}
	if limited && (o.Price > up+1e-9 || o.Price < down-1e-9) {
		return reject("价格 %.2f 超出涨跌停 [%.2f, %.2f]", o.Price, down, up)
	}
	if !Continuous(in) {
		return pass(vol)
	}
	sell := o.Side == Sell
	bench := ashare.CageBenchmark(sell, q.Bid1, q.Ask1, q.Last, q.PrevClose)
	bound, err := ashare.Cage(o.Symbol, sell, bench)
	if err != nil {
		return reject("%v", err)
	}
	if !sell && o.Price > bound+1e-9 {
		return reject("买价 %.2f 高于价格笼子上限 %.2f", o.Price, bound)
	}
	if sell && o.Price < bound-1e-9 {
		return reject("卖价 %.2f 低于价格笼子下限 %.2f", o.Price, bound)
	}
	return pass(vol)
}

func ruleLot(in *Input, p *Params, vol int) outcome {
	o := in.Order
	max, err := ashare.MaxOrderVolume(o.Symbol)
	if err != nil {
		return reject("%v", err)
	}
	if vol > max {
		return reject("单笔 %d 股超过上限 %d", vol, max)
	}
	if o.Side == Buy {
		n, err := ashare.RoundBuy(o.Symbol, vol)
		if err != nil {
			return reject("%v", err)
		}
		if n != vol {
			return reject("买入 %d 股不是合法手数", vol)
		}
	}
	return pass(vol)
}

func ruleSellable(in *Input, p *Params, vol int) outcome {
	if in.Order.Side != Sell {
		return pass(vol)
	}
	h := in.Account.Holdings[in.Order.Symbol]
	can := h.Sellable()
	if vol > can {
		return reject("可卖 %d 股（可用 %d、在途卖出 %d），申报 %d", can, h.Available, h.FrozenSell, vol)
	}
	ok, err := ashare.CanSell(in.Order.Symbol, can, vol)
	if err != nil {
		return reject("%v", err)
	}
	if !ok {
		return reject("卖出 %d 股不符合余股规则（可卖 %d）", vol, can)
	}
	return pass(vol)
}

// pending 汇总在途买单和尚未进入快照的预留。filter 为空时取全部。
func pending(in *Input, filter func(symbol, sector string) bool) float64 {
	seen := map[string]bool{}
	total := 0.0
	for _, o := range in.Account.Open {
		seen[o.ClientID] = true
		if o.Side == Buy && (filter == nil || filter(o.Symbol, o.Sector)) {
			total += o.Price * float64(o.Volume)
		}
	}
	for _, r := range in.Reservations {
		if seen[r.ClientID] {
			continue
		}
		if filter == nil || filter(r.Symbol, r.Sector) {
			total += r.Amount
		}
	}
	return total
}

func reserved(in *Input) float64 {
	seen := map[string]bool{}
	for _, o := range in.Account.Open {
		seen[o.ClientID] = true
	}
	total := 0.0
	for _, r := range in.Reservations {
		if !seen[r.ClientID] {
			total += r.Amount
		}
	}
	return total
}

// lotStep 是起买之后的递增股数：科创板、北交所逐股，其余 100 股。
func lotStep(sym string) int {
	if n, _ := ashare.RoundBuy(sym, 201); n == 201 {
		return 1
	}
	return 100
}

// shrink 把买入量缩到金额 room 以内并按板块取整。返回 0 表示一手都放不下。
func shrink(sym string, price float64, vol int, room float64) int {
	if room <= 0 || price <= 0 {
		return 0
	}
	max := int(math.Floor(room/price + 1e-9))
	if max >= vol {
		return vol
	}
	n, _ := ashare.RoundBuy(sym, max)
	return n
}

func capRule(in *Input, vol int, limit, used float64, label string) outcome {
	o := in.Order
	room := limit - used
	n := shrink(o.Symbol, o.Price, vol, room)
	if n == vol {
		return pass(vol)
	}
	if n <= 0 {
		return reject("%s已用 %.0f 元，上限 %.0f 元，放不下一手", label, used, limit)
	}
	return passNote(n, fmt.Sprintf("%s上限 %.0f 元，缩量 %d → %d", label, limit, vol, n))
}

func ruleSingle(in *Input, p *Params, vol int) outcome {
	if in.Order.Side != Buy {
		return pass(vol)
	}
	a := in.Account
	if a.AsOf.IsZero() || a.Equity <= 0 {
		return reject("没有账户快照")
	}
	if age := in.Now.Sub(a.AsOf); age > p.AccountMaxAge {
		return reject("账户快照已过 %d 秒", int(age.Seconds()))
	}
	sym := in.Order.Symbol
	used := a.Holdings[sym].MarketValue + pending(in, func(s, _ string) bool { return s == sym })
	return capRule(in, vol, a.Equity*p.MaxSinglePct*p.Scale(in.Market), used, "单票")
}

func ruleTotal(in *Input, p *Params, vol int) outcome {
	if in.Order.Side != Buy {
		return pass(vol)
	}
	used := pending(in, nil)
	for _, h := range in.Account.Holdings {
		used += h.MarketValue
	}
	return capRule(in, vol, in.Account.Equity*p.MaxTotalPct*p.Scale(in.Market), used, "总仓")
}

func ruleSector(in *Input, p *Params, vol int) outcome {
	if in.Order.Side != Buy {
		return pass(vol)
	}
	sec := in.Order.Sector
	if sec == "" {
		return passNote(vol, "订单没有板块信息，未检查")
	}
	used := pending(in, func(_, s string) bool { return s == sec })
	for _, h := range in.Account.Holdings {
		if h.Sector == sec {
			used += h.MarketValue
		}
	}
	return capRule(in, vol, in.Account.Equity*p.MaxSectorPct, used, "板块「"+sec+"」")
}

func ruleDailyBuys(in *Input, p *Params, vol int) outcome {
	if in.Order.Side != Buy {
		return pass(vol)
	}
	if in.Counters.BuysToday >= p.MaxDailyBuys {
		return reject("当日已放行 %d 笔买单，上限 %d", in.Counters.BuysToday, p.MaxDailyBuys)
	}
	return pass(vol)
}

func ruleLiquidity(in *Input, p *Params, vol int) outcome {
	if in.Order.Side != Buy {
		return pass(vol)
	}
	q := in.Quote
	base := math.Max(q.Amount, q.PrevAmount)
	if base < p.MinTurnover {
		return reject("成交额 %.0f 元低于下限 %.0f", base, p.MinTurnover)
	}
	return capRule(in, vol, base*p.MaxParticipation, 0, "流动性")
}

func ruleCash(in *Input, p *Params, vol int) outcome {
	o := in.Order
	if o.Side != Buy {
		return pass(vol)
	}
	avail := in.Account.Available - reserved(in)
	cost := func(n int) float64 {
		amt := o.Price * float64(n)
		return amt + p.Fee.Cost(amt, false)
	}
	if cost(vol) <= avail+1e-6 {
		return pass(vol)
	}
	n := shrink(o.Symbol, o.Price, vol, avail-p.Fee.Cost(avail, false))
	for n > 0 && cost(n) > avail+1e-6 {
		n, _ = ashare.RoundBuy(o.Symbol, n-lotStep(o.Symbol))
	}
	if n <= 0 {
		return reject("可用资金 %.2f 元（已预留 %.2f），不够一手", avail, reserved(in))
	}
	return passNote(n, fmt.Sprintf("可用资金 %.2f 元，缩量 %d → %d", avail, vol, n))
}

func ruleSelfTrade(in *Input, p *Params, vol int) outcome {
	o := in.Order
	for _, open := range in.Account.Open {
		if open.Symbol != o.Symbol || open.Side == o.Side {
			continue
		}
		if o.Side == Buy && o.Price >= open.Price-1e-9 {
			return reject("与在途卖单 %s（%.2f）可能自成交", open.ClientID, open.Price)
		}
		if o.Side == Sell && o.Price <= open.Price+1e-9 {
			return reject("与在途买单 %s（%.2f）可能自成交", open.ClientID, open.Price)
		}
	}
	return pass(vol)
}

func ruleRate(in *Input, p *Params, vol int) outcome {
	c := in.Counters
	if c.LastSecond >= p.MaxOrdersPerSec {
		return reject("近 1 秒申报撤单 %d 笔，上限 %d", c.LastSecond, p.MaxOrdersPerSec)
	}
	if c.Today >= p.MaxOrdersPerDay {
		return reject("当日申报撤单 %d 笔，上限 %d", c.Today, p.MaxOrdersPerDay)
	}
	return pass(vol)
}

// DailyLossHit 判断当日亏损是否达到熔断线。达到后由服务锁存到收盘。
func DailyLossHit(a Account, p Params) bool {
	if a.DayStartEquity <= 0 || a.Equity <= 0 {
		return false
	}
	return (a.Equity-a.DayStartEquity)/a.DayStartEquity <= -p.DailyLossPct
}

// MarketBreaker 返回市场类熔断原因：指数大跌、炸板率过高、风险度极高。没有触发时返回空。
func MarketBreaker(m Market, p Params) string {
	switch {
	case m.IndexChange <= -p.IndexDropPct:
		return fmt.Sprintf("指数跌 %.2f%%", -m.IndexChange*100)
	case m.BurstRate >= p.BurstRateMax:
		return fmt.Sprintf("炸板率 %.0f%%", m.BurstRate*100)
	case m.RiskLevel >= 3:
		return "风险度极高"
	}
	return ""
}

func ruleBreaker(in *Input, p *Params, vol int) outcome {
	if in.Order.Side != Buy {
		return pass(vol)
	}
	if in.Breaker != "" {
		return reject("已熔断（%s），收盘前只允许卖出", in.Breaker)
	}
	if DailyLossHit(in.Account, *p) {
		return reject("当日亏损达到 %.1f%%，收盘前只允许卖出", p.DailyLossPct*100)
	}
	if why := MarketBreaker(in.Market, *p); why != "" {
		return reject("%s，暂停开仓", why)
	}
	return pass(vol)
}
