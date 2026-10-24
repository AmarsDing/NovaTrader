// strategy.go 是解释器求值侧与 rules.Strategy 适配。
// 严格缺失传播：任一取数环节拿不到（快照无当日、历史窗不足、昨收缺失、
// 指标窗非正价等）即 ok=false 上浮，绝不 0 冒充；Filter 缺失即不通过。
package decl

import (
	"context"
	"fmt"
	"math"

	"server/pkg/rules"
)

// Strategy 把定义按 refstrat 同观的三元组（Name/Params/构造）落成
// rules.Strategy。ParamValues 一期取默认（strategy_config 逐行覆盖留给
// 回测/进化通道）；构造期零校验负担——Parse 已全量裁决。
func (d *Definition) Strategy(price rules.PriceParams) (rules.Strategy, error) {
	if d == nil || d.Filter == nil || d.params == nil {
		return nil, fmt.Errorf("decl: strategy from unparsed definition")
	}
	return &strategy{def: d, price: price}, nil
}

// Spec 复用注册表三元组形式导出（M07 回测/进化通道取用）。扫描热路径不入
// 全局注册表：策略行可热装卸，全局 Register 重名即 panic，与重载语义相冲。
func (d *Definition) Spec() rules.Spec {
	params := make([]rules.ParamSpec, 0, len(d.Params))
	for _, p := range d.Params {
		params = append(params, rules.ParamSpec{
			Name: p.Name, Title: p.Name, Default: p.Default, Min: p.Min, Max: p.Max,
		})
	}
	return rules.Spec{Name: d.Name, Title: d.Title, Params: params}
}

type strategy struct {
	def   *Definition
	price rules.PriceParams
}

func (st *strategy) Name() string { return st.def.Name }

func (st *strategy) Filter(ctx context.Context, s rules.Snapshot) bool {
	ok, known := st.def.evalBool(st.def.Filter, s)
	return known && ok
}

func (st *strategy) Score(ctx context.Context, s rules.Snapshot) float64 {
	score := st.def.Score.Base
	for _, a := range st.def.Score.Adds {
		if a.When != nil {
			when, known := st.def.evalBool(a.When, s)
			if !known || !when {
				continue
			}
		}
		v, known := st.def.evalNum(a.Num, s)
		if !known {
			continue // 满足条件但取值缺失 → 该加分项按 0 计（包注释口径）
		}
		if a.Cap > 0 && v > a.Cap {
			v = a.Cap
		}
		score += v
	}
	if score < 0 {
		return 0
	}
	if score > 100 {
		return 100
	}
	return score
}

func (st *strategy) EntryPlan(ctx context.Context, s rules.Snapshot) (rules.Plan, bool) {
	px := s.Last()
	if ref := st.def.Entry.Price; ref != nil {
		v, known := st.def.evalNum(ref, s)
		if !known {
			return rules.Plan{}, false
		}
		px = v
	}
	if !(px > 0) { // 含 NaN：参考价取不到显式不出计划，不向价格层递伪值
		return rules.Plan{}, false
	}
	reason := st.def.Title
	if reason == "" {
		reason = st.def.Name
	}
	return rules.Plan{Side: rules.SideBuy, Price: px, HoldDays: st.def.HoldDays, Reason: reason}, true
}

// ExitPlan 与内置模板同道：止损→利空→止盈→情绪退潮→时间到，全走 rules.Exit。
func (st *strategy) ExitPlan(ctx context.Context, s rules.Snapshot, pos rules.Position) (rules.Plan, bool) {
	return rules.Exit(s, pos, st.def.HoldDays, st.price)
}

// evalNum 求数值。ok=false = 取数缺失（不区分是哪种缺），调用方按缺失语义裁决。
func (d *Definition) evalNum(e *Expr, s rules.Snapshot) (float64, bool) {
	switch e.Op {
	case "num":
		return *e.Num, true
	case "field":
		return evalField(e.Name, s)
	case "param":
		return d.params[e.Name].Default, true
	case "add", "sub", "mul", "div", "min", "max":
		a, ok := d.evalNum(e.Args[0], s)
		if !ok {
			return 0, false
		}
		b, ok := d.evalNum(e.Args[1], s)
		if !ok {
			return 0, false
		}
		switch e.Op {
		case "add":
			return a + b, true
		case "sub":
			return a - b, true
		case "mul":
			return a * b, true
		case "div":
			if b == 0 {
				return 0, false // 除零=该路径取不出数，缺失而非 0
			}
			return a / b, true
		case "min":
			return math.Min(a, b), true
		default:
			return math.Max(a, b), true
		}
	case "clamp":
		x, ok := d.evalNum(e.Args[0], s)
		if !ok {
			return 0, false
		}
		lo, ok := d.evalNum(e.Args[1], s)
		if !ok {
			return 0, false
		}
		hi, ok := d.evalNum(e.Args[2], s)
		if !ok {
			return 0, false
		}
		return math.Min(math.Max(x, lo), hi), true
	case "abs":
		x, ok := d.evalNum(e.Args[0], s)
		if !ok {
			return 0, false
		}
		return math.Abs(x), true
	case "ma_close":
		return maOf(s.Bars, int(*e.Args[0].Num))
	case "ma_amount":
		return maAmountOf(s.Bars, int(*e.Args[0].Num))
	case "high_n":
		return highOf(s.Bars, int(*e.Args[0].Num))
	case "low_n":
		return lowOf(s.Bars, int(*e.Args[0].Num))
	}
	return 0, false
}

// evalBool 求布尔。三个逻辑连接子短路但保留缺失语义：and 任一缺即缺；
// or 全真值已知才判。比较运算一方缺即缺。
func (d *Definition) evalBool(e *Expr, s rules.Snapshot) (bool, bool) {
	switch e.Op {
	case "bool":
		return *e.Bool, true
	case "gt", "gte", "lt", "lte", "eq", "neq":
		a, ok := d.evalNum(e.Args[0], s)
		if !ok {
			return false, false
		}
		b, ok := d.evalNum(e.Args[1], s)
		if !ok {
			return false, false
		}
		switch e.Op {
		case "gt":
			return a > b, true
		case "gte":
			return a >= b, true
		case "lt":
			return a < b, true
		case "lte":
			return a <= b, true
		case "eq":
			return a == b, true
		default:
			return a != b, true
		}
	case "and":
		all := true
		for _, a := range e.Args {
			v, ok := d.evalBool(a, s)
			if !ok {
				return false, false
			}
			all = all && v
		}
		return all, true
	case "or":
		any := false
		for _, a := range e.Args {
			v, ok := d.evalBool(a, s)
			if !ok {
				return false, false
			}
			any = any || v
		}
		return any, true
	case "not":
		v, ok := d.evalBool(e.Args[0], s)
		if !ok {
			return false, false
		}
		return !v, true
	}
	return false, false
}

// evalField 今日/昨日截面字段。open/high/low/close/amount/volume 指当日——
// 盘前（Today==nil）一律缺失；与策略「盘前不出信号」合同一致。
func evalField(name string, s rules.Snapshot) (float64, bool) {
	switch name {
	case "last":
		if v := s.Last(); v > 0 {
			return v, true
		}
		return 0, false
	case "prev_close":
		if v := s.PrevClose(); v > 0 {
			return v, true
		}
		return 0, false
	case "dev_ma10":
		ma, ok := maOf(s.Bars, 10)
		if !ok || ma <= 0 {
			return 0, false
		}
		if v := s.Last(); v > 0 {
			return v/ma - 1, true
		}
		return 0, false
	default:
		if s.Today == nil {
			return 0, false
		}
		switch name {
		case "open":
			return positive(s.Today.Open)
		case "high":
			return positive(s.Today.High)
		case "low":
			return positive(s.Today.Low)
		case "close":
			return positive(s.Today.Close)
		case "amount":
			return positive(s.Today.Amount)
		case "volume":
			if s.Today.Volume > 0 {
				return float64(s.Today.Volume), true
			}
			return 0, false
		}
	}
	return 0, false
}

func positive(v float64) (float64, bool) {
	if v > 0 {
		return v, true
	}
	return 0, false
}

// maOf 是近 n 根已收盘日线均值（与 rules.MA 同口径：不足 n 根取不出）。
func maOf(bars []rules.Bar, n int) (float64, bool) {
	return rules.MA(bars, n)
}

func maAmountOf(bars []rules.Bar, n int) (float64, bool) {
	if n > len(bars) {
		return 0, false
	}
	sum := 0.0
	for _, b := range bars[len(bars)-n:] {
		sum += b.Amount
	}
	return sum / float64(n), true
}

func highOf(bars []rules.Bar, n int) (float64, bool) {
	if n > len(bars) {
		return 0, false
	}
	hi := bars[len(bars)-1].High
	for _, b := range bars[len(bars)-n:] {
		if b.High > hi {
			hi = b.High
		}
	}
	return positive(hi)
}

func lowOf(bars []rules.Bar, n int) (float64, bool) {
	if n > len(bars) {
		return 0, false
	}
	lo := bars[len(bars)-1].Low
	for _, b := range bars[len(bars)-n:] {
		if b.Low < lo {
			lo = b.Low
		}
	}
	return positive(lo)
}
