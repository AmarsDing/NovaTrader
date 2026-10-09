package market

import (
	"time"

	"server/pkg/tradecal"
)

// Bar 是一根不复权 K 线。Time 为开始时间。
type Bar struct {
	Symbol string    `json:"symbol"`
	Time   time.Time `json:"time"`
	Open   float64   `json:"open"`
	High   float64   `json:"high"`
	Low    float64   `json:"low"`
	Close  float64   `json:"close"`
	Volume int64     `json:"volume"`
	Amount float64   `json:"amount"`
}

// BarsPerDay 是每个交易日的 1 分钟线根数。
const BarsPerDay = 240

// Slot 返回快照所属 1 分钟线的开始时间。
// 集合竞价（09:30 前）不单独成线，成交并入 09:30；11:30 之后到 13:00 归 11:29；15:00 收盘竞价归 14:59。
func Slot(t time.Time) (time.Time, bool) {
	d := t.In(tradecal.Shanghai())
	m := d.Hour()*60 + d.Minute()
	day := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, tradecal.Shanghai())
	switch {
	case m < 9*60+30:
		return time.Time{}, false
	case m < 11*60+30:
		return day.Add(time.Duration(m) * time.Minute), true
	case m < 13*60:
		return day.Add((11*60 + 29) * time.Minute), true
	case m < 15*60:
		return day.Add(time.Duration(m) * time.Minute), true
	case m < 15*60+30:
		return day.Add((14*60 + 59) * time.Minute), true
	default:
		return time.Time{}, false
	}
}

// NextSlot 返回下一根线的开始时间。14:59 之后没有下一根。
func NextSlot(slot time.Time) (time.Time, bool) {
	d := slot.In(tradecal.Shanghai())
	m := d.Hour()*60 + d.Minute()
	day := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, tradecal.Shanghai())
	switch {
	case m == 11*60+29:
		return day.Add(13 * time.Hour), true
	case m >= 14*60+59:
		return time.Time{}, false
	default:
		return slot.Add(time.Minute), true
	}
}

// SlotIndex 返回这根线是当天第几根（从 1 起）。用于盘中量比的已开盘分钟数。
func SlotIndex(slot time.Time) int {
	d := slot.In(tradecal.Shanghai())
	m := d.Hour()*60 + d.Minute()
	if m < 13*60 {
		return m - (9*60 + 30) + 1
	}
	return 120 + m - 13*60 + 1
}

// sessionEndGrace 是 11:29、14:59 两根线额外等待的时间：午间收盘和收盘集合竞价的成交要几秒后才出现在快照里。
const sessionEndGrace = 25 * time.Second

// slotDue 判断一根线是否可以封口：结束时间再过 grace。
func slotDue(slot, now time.Time, grace time.Duration) bool {
	end := slot.Add(time.Minute).Add(grace)
	d := slot.In(tradecal.Shanghai())
	m := d.Hour()*60 + d.Minute()
	if m == 11*60+29 || m == 14*60+59 {
		end = end.Add(sessionEndGrace)
	}
	return !now.Before(end)
}

func sameDay(a, b time.Time) bool {
	x, y := a.In(tradecal.Shanghai()), b.In(tradecal.Shanghai())
	return x.Year() == y.Year() && x.YearDay() == y.YearDay()
}

// Aggregator 把一只股票的 3 秒快照聚成 1 分钟线。不是并发安全的，每只股票一个。
type Aggregator struct {
	symbol string
	day    time.Time

	cur      *Bar
	last     *Bar // 最近一根已封口的线
	baseVol  int64
	baseAmt  float64
	dayHigh  float64
	dayLow   float64
	dayOpen  float64
	lastPx   float64
	tradedOK bool // 当天有过成交，停牌股不补平线
}

func NewAggregator(symbol string) *Aggregator {
	return &Aggregator{symbol: symbol}
}

func (a *Aggregator) reset(day time.Time) {
	*a = Aggregator{symbol: a.symbol, day: day}
}

// Current 返回尚未封口的线，供盘中预览。
func (a *Aggregator) Current() (Bar, bool) {
	if a.cur == nil {
		return Bar{}, false
	}
	return *a.cur, true
}

// Push 处理一条快照，返回因此封口（或被晚到成交修正）的线。
func (a *Aggregator) Push(s Snapshot) []Bar {
	if a.day.IsZero() || !sameDay(a.day, s.Time) {
		a.reset(s.Time)
	}
	if s.Volume > 0 {
		a.tradedOK = true
	}
	if s.Open > 0 {
		a.dayOpen = s.Open
	}
	slot, ok := Slot(s.Time)
	if !ok {
		a.observe(s)
		return nil
	}
	var out []Bar
	switch {
	case a.cur == nil && a.last != nil && !slot.After(a.last.Time):
		// 已封口的线收到晚到成交：修正后再发一次，由写库 upsert 覆盖。
		if s.Volume > a.baseVol {
			fixed := *a.last
			a.apply(&fixed, s, false)
			fixed.Volume += s.Volume - a.baseVol
			fixed.Amount += s.Amount - a.baseAmt
			a.baseVol, a.baseAmt = s.Volume, s.Amount
			a.last = &fixed
			out = append(out, fixed)
		}
		a.observe(s)
		return out
	case a.cur != nil && slot.Before(a.cur.Time):
		a.observe(s)
		return nil
	case a.cur != nil && slot.After(a.cur.Time):
		out = append(out, a.close())
	}
	if a.cur == nil {
		out = append(out, a.fillUntil(slot)...)
		a.open(slot, s)
	}
	a.apply(a.cur, s, true)
	a.observe(s)
	return out
}

// Flush 在 now 时封口到期的线，并为没有新成交的分钟补平线。
func (a *Aggregator) Flush(now time.Time, grace time.Duration) []Bar {
	var out []Bar
	if a.cur != nil && (slotDue(a.cur.Time, now, grace) || !sameDay(a.cur.Time, now)) {
		out = append(out, a.close())
	}
	if a.day.IsZero() || !sameDay(a.day, now) || !a.tradedOK || a.last == nil {
		return out
	}
	for {
		next, ok := NextSlot(a.last.Time)
		if !ok || !slotDue(next, now, grace) {
			return out
		}
		out = append(out, a.flat(next))
	}
}

func (a *Aggregator) observe(s Snapshot) {
	if s.High > a.dayHigh {
		a.dayHigh = s.High
	}
	if s.Low > 0 && (a.dayLow == 0 || s.Low < a.dayLow) {
		a.dayLow = s.Low
	}
	if s.Last > 0 {
		a.lastPx = s.Last
	}
}

func (a *Aggregator) open(slot time.Time, s Snapshot) {
	px := s.Last
	if a.last == nil && a.dayOpen > 0 {
		px = a.dayOpen
	}
	a.cur = &Bar{Symbol: a.symbol, Time: slot, Open: px, High: px, Low: px, Close: px}
}

// apply 把快照并入线。日内最高 / 最低被刷新时，说明这 3 秒内出现过新极值，用它修正本根高低点。
func (a *Aggregator) apply(b *Bar, s Snapshot, cumulative bool) {
	if s.Last > 0 {
		if s.Last > b.High {
			b.High = s.Last
		}
		if s.Last < b.Low || b.Low == 0 {
			b.Low = s.Last
		}
		b.Close = s.Last
	}
	if s.High > a.dayHigh && s.High > b.High {
		b.High = s.High
	}
	if s.Low > 0 && (a.dayLow == 0 || s.Low < a.dayLow) && s.Low < b.Low {
		b.Low = s.Low
	}
	if cumulative {
		b.Volume = s.Volume - a.baseVol
		b.Amount = s.Amount - a.baseAmt
		if b.Volume < 0 {
			b.Volume = 0
		}
		if b.Amount < 0 {
			b.Amount = 0
		}
	}
}

func (a *Aggregator) close() Bar {
	b := *a.cur
	a.baseVol += b.Volume
	a.baseAmt += b.Amount
	a.last = &b
	a.cur = nil
	return b
}

func (a *Aggregator) fillUntil(slot time.Time) []Bar {
	if a.last == nil || !a.tradedOK {
		return nil
	}
	var out []Bar
	for {
		next, ok := NextSlot(a.last.Time)
		if !ok || !next.Before(slot) {
			return out
		}
		out = append(out, a.flat(next))
	}
}

func (a *Aggregator) flat(slot time.Time) Bar {
	px := a.last.Close
	b := Bar{Symbol: a.symbol, Time: slot, Open: px, High: px, Low: px, Close: px}
	a.last = &b
	return b
}

// DailyFromMinutes 把一天的分钟线合成日线。开盘取第一根开盘，量额求和。
func DailyFromMinutes(bars []Bar) (Bar, bool) {
	if len(bars) == 0 {
		return Bar{}, false
	}
	first := bars[0]
	d := first.Time.In(tradecal.Shanghai())
	out := Bar{
		Symbol: first.Symbol,
		Time:   time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, tradecal.Shanghai()),
		Open:   first.Open,
		High:   first.High,
		Low:    first.Low,
	}
	for _, b := range bars {
		if b.High > out.High {
			out.High = b.High
		}
		if b.Low > 0 && b.Low < out.Low {
			out.Low = b.Low
		}
		out.Close = b.Close
		out.Volume += b.Volume
		out.Amount += b.Amount
	}
	return out, true
}
