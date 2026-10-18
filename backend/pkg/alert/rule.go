// Package alert 是用户价格预警的 headless 规则引擎（G3，见 doc/开发文档/gloomberb对标提升方案.md G3 节）。
// 语义移植自 gloomberb src/plugins/builtin/alerts/alert-engine.ts：价格三态 above/below/crosses，
// crosses 逐笔沿用最上一笔价当基线判定穿越；编辑/重建走白名单字段，触发态字段一律清零，
// 防止幽灵基线在重挂后立刻误触发。
// 本包只做纯规则语义与存储编解码，不订阅行情、不投递通知：评估由后端服务（risk/market）在
// 行情快照节拍上调用 Step，触发后的事件交给既有 outbox/notify 链路。
package alert

import (
	"fmt"
	"strings"
	"time"
)

// Condition 是价格三态。
type Condition string

const (
	// Above 现价严格高于目标价触发。
	Above Condition = "above"
	// Below 现价严格低于目标价触发。
	Below Condition = "below"
	// Crosses 现价相对上一笔基线穿越目标价（任一方向）触发。
	Crosses Condition = "crosses"
)

// Status 是规则生命周期：active 参与评估；triggered 已触发，编辑或重挂前不再评估（防重复语义）。
type Status string

const (
	StatusActive    Status = "active"
	StatusTriggered Status = "triggered"
)

// Rule 是一条价格预警规则。LastCheckedPrice 是触发态字段：crosses 用它当基线，
// 编辑与重挂必须把它清零，见 Edit。
type Rule struct {
	ID          string     `json:"id"`
	Symbol      string     `json:"symbol"`
	Condition   Condition  `json:"condition"`
	TargetPrice float64    `json:"target_price"`
	Note        string     `json:"note,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	ArmedAt     time.Time  `json:"armed_at"`
	Status      Status     `json:"status"`
	LastPrice   *float64   `json:"last_price,omitempty"`
	TriggeredAt *time.Time `json:"triggered_at,omitempty"`
}

// ParseCondition 收下划线/大小写不敏感的三态名。
func ParseCondition(raw string) (Condition, error) {
	switch c := Condition(strings.ToLower(strings.TrimSpace(raw))); c {
	case Above, Below, Crosses:
		return c, nil
	default:
		return "", fmt.Errorf("alert: unknown condition %q", raw)
	}
}

// Create 建一条 active 规则。ArmedAt 记为 now，cloud/通知侧据此知道这是一次新的挂起。
// 代码只做大写去空白；规范化成通达信/交易所格式是 service 层（pkg/symbol）的职责。
func Create(id, symbol string, cond Condition, target float64, now time.Time) (Rule, error) {
	sym := strings.ToUpper(strings.TrimSpace(symbol))
	if strings.TrimSpace(id) == "" {
		return Rule{}, fmt.Errorf("alert: id is required")
	}
	if sym == "" {
		return Rule{}, fmt.Errorf("alert: symbol is required")
	}
	if target <= 0 {
		return Rule{}, fmt.Errorf("alert: target price must be positive")
	}
	return Rule{
		ID: id, Symbol: sym, Condition: cond, TargetPrice: target,
		CreatedAt: now, ArmedAt: now, Status: StatusActive,
	}, nil
}

// Edit 用白名单字段重建规则，而不是 spread 旧值：触发态字段（Status、LastPrice、
// TriggeredAt）一律丢弃，一条重挂的 crosses 必须从全新基线开始。Note 不属于触发态，保留。
// 换标的等价于换基线语境，同样从零评估。
func Edit(r Rule, symbol string, cond Condition, target float64, now time.Time) (Rule, error) {
	next, err := Create(r.ID, symbol, cond, target, now)
	if err != nil {
		return Rule{}, err
	}
	next.CreatedAt, next.Note = r.CreatedAt, r.Note
	return next, nil
}

// Rearm 重挂等价于按原参数编辑：清触发态、换新基线、ArmedAt 前进。
func Rearm(r Rule, now time.Time) (Rule, error) {
	return Edit(r, r.Symbol, r.Condition, r.TargetPrice, now)
}

// Step 用一笔现价推进一条规则，返回新规则与是否在本次触发。
// 触发后状态转 triggered，同一笔价再评估不会重复触发；persist 由调用方负责。
func Step(r Rule, price float64, now time.Time) (Rule, bool) {
	if r.Status != StatusActive || price <= 0 {
		return r, false
	}
	fired := false
	switch r.Condition {
	case Above:
		fired = price > r.TargetPrice
	case Below:
		fired = price < r.TargetPrice
	case Crosses:
		if r.LastPrice != nil {
			last := *r.LastPrice
			wasBelowOrAt := last <= r.TargetPrice
			wasAboveOrAt := last >= r.TargetPrice
			isAbove := price > r.TargetPrice
			isBelow := price < r.TargetPrice
			fired = wasBelowOrAt && isAbove || wasAboveOrAt && isBelow
		}
	}
	next := r
	p := price
	next.LastPrice = &p
	if fired {
		at := now
		next.Status = StatusTriggered
		next.TriggeredAt = &at
	}
	return next, fired
}

// Description 是给人看的一行规则摘要，如 "600519.SH > 1700.00"。
func Description(r Rule) string {
	op := "↕"
	switch r.Condition {
	case Above:
		op = ">"
	case Below:
		op = "<"
	}
	return fmt.Sprintf("%s %s %.2f", r.Symbol, op, r.TargetPrice)
}
