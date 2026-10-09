// Package ashare 计算涨跌停、最小价位、手数、费用和 T+1。
// 价格单位是元，股数是股。费率可以由调用方覆盖，默认值只是起点。
package ashare

import (
	"math"
	"time"

	"server/pkg/symbol"
)

const Tick = 0.01

// NoLimitDays 是新股上市后不设涨跌幅限制的交易日数（全面注册制后各板块一致）。
const NoLimitDays = 5

// mainBoardST10From 起沪深主板风险警示股票涨跌幅由 5% 调为 10%（沪深交易规则 2026 年修订）。
var mainBoardST10From = time.Date(2026, 7, 6, 0, 0, 0, 0, time.FixedZone("CST", 8*3600))

// Ratio 按今天的规则返回涨跌停幅度。回测请用 RatioOn。
func Ratio(raw string, st bool) (float64, error) {
	return RatioOn(raw, st, time.Now())
}

// RatioOn 返回某个交易日的涨跌停幅度。
// 科创板、创业板 20%（含 ST），北交所 30%（含 ST），主板 10%；主板 ST 在 2026-07-06 之前为 5%。
func RatioOn(raw string, st bool, day time.Time) (float64, error) {
	sym, err := symbol.Parse(raw)
	if err != nil {
		return 0, err
	}
	code := sym.Code
	switch {
	case sym.Market == "BJ":
		return 0.30, nil
	case isSTAR(code):
		return 0.20, nil
	case len(code) >= 2 && code[:2] == "30":
		return 0.20, nil
	case st && day.Before(mainBoardST10From):
		return 0.05, nil
	default:
		return 0.10, nil
	}
}

// Limit 按前收盘计算涨停价和跌停价，四舍五入到分。
// 用整数分计算：浮点乘法会把 1.15×1.1=1.265 算成 1.2649…，涨停价少一分。
func Limit(prev float64, ratio float64) (up, down float64) {
	cents := int64(math.Round(prev * 100))
	bp := int64(math.Round(ratio * 10000))
	upCents := (cents*(10000+bp) + 5000) / 10000
	downCents := (cents*(10000-bp) + 5000) / 10000
	return float64(upCents) / 100, float64(downCents) / 100
}

// Clamp 把价格限制在涨跌停之间，并取整到最小价位。
func Clamp(price, up, down float64) float64 {
	p := roundTick(price)
	if up > 0 && p > up {
		return up
	}
	if down > 0 && p < down {
		return down
	}
	return p
}

func roundTick(v float64) float64 {
	return math.Round(v*100) / 100
}

// RoundBuy 把买入股数取整。主板、创业板为 100 股整数倍；科创板至少 200 股、北交所至少 100 股，以上可以逐股。
// 不足一手时返回 0。
func RoundBuy(raw string, shares int) (int, error) {
	sym, err := symbol.Parse(raw)
	if err != nil {
		return 0, err
	}
	if shares <= 0 {
		return 0, nil
	}
	if min, ok := oddLotBoard(sym); ok {
		if shares < min {
			return 0, nil
		}
		return shares, nil
	}
	return shares / 100 * 100, nil
}

// oddLotBoard 对科创板、北交所返回起买股数。这两个板块超过起买股数后可以逐股申报。
func oddLotBoard(sym symbol.Symbol) (int, bool) {
	switch {
	case isSTAR(sym.Code):
		return 200, true
	case sym.Market == "BJ":
		return 100, true
	}
	return 0, false
}

func isSTAR(code string) bool {
	return len(code) >= 3 && (code[:3] == "688" || code[:3] == "689")
}

// IsSTAR 判断科创板代码（688、689 开头）。
func IsSTAR(code string) bool { return isSTAR(code) }

// IsAStock 判断是否 A 股股票：沪市 60x/68x，深市 00x/30x，北交所 4xx/8xx/92x。指数、B 股、基金返回 false。
func IsAStock(raw string) bool {
	sym, err := symbol.Parse(raw)
	if err != nil {
		return false
	}
	c := sym.Code
	switch sym.Market {
	case "SH":
		return c[:2] == "60" || c[:2] == "68"
	case "SZ":
		return c[:2] == "00" || c[:2] == "30"
	case "BJ":
		return c[0] == '4' || c[0] == '8' || c[:2] == "92"
	}
	return false
}

// CanSell 判断卖出股数是否合法。
// 主板、创业板：100 股整数倍，或者把不足 100 股的余股连同整手一次卖出。
// 科创板、北交所：不少于起买股数（200 / 100）；持仓不足起买股数时必须一次卖完。
func CanSell(raw string, available, want int) (bool, error) {
	if want <= 0 || want > available {
		return false, nil
	}
	sym, err := symbol.Parse(raw)
	if err != nil {
		return false, err
	}
	if min, ok := oddLotBoard(sym); ok {
		return want >= min || want == available, nil
	}
	return want%100 == available%100 || want%100 == 0, nil
}

// Fee 是一笔成交的费用参数。
type Fee struct {
	CommissionRate float64
	MinCommission  float64
	StampTaxRate   float64
	TransferRate   float64
}

// DefaultFee 佣金万 2.5、最低 5 元，卖出印花税万 5，过户费万 0.1。
func DefaultFee() Fee {
	return Fee{
		CommissionRate: 0.00025,
		MinCommission:  5,
		StampTaxRate:   0.0005,
		TransferRate:   0.00001,
	}
}

// Cost 计算一笔成交的费用，结果取整到分。sell 为真时收印花税。
func (f Fee) Cost(amount float64, sell bool) float64 {
	commission := amount * f.CommissionRate
	if commission < f.MinCommission {
		commission = f.MinCommission
	}
	tax := 0.0
	if sell {
		tax = amount * f.StampTaxRate
	}
	transfer := amount * f.TransferRate
	return roundTick(commission + tax + transfer)
}

// Sellable 是 T+1 之后的可卖股数。当日买入不计入。
func Sellable(total, boughtToday int) int {
	n := total - boughtToday
	if n < 0 {
		return 0
	}
	return n
}
