package market

import (
	"time"

	"server/pkg/ashare"
	"server/pkg/tradecal"
)

const (
	DirUp   = "UP"
	DirDown = "DOWN"

	StatusSealed = "SEALED"
	StatusBroken = "BROKEN"

	EventSeal   = "SEAL"
	EventBreak  = "BREAK"
	EventReseal = "RESEAL"
)

const priceEps = 1e-6

// LimitBoard 是一只股票一天一个方向的涨跌停记录。
type LimitBoard struct {
	Symbol      string    `json:"symbol"`
	TradeDate   time.Time `json:"trade_date"`
	Direction   string    `json:"direction"`
	Status      string    `json:"status"`
	LimitPrice  float64   `json:"limit_price"`
	FirstSealAt time.Time `json:"first_seal_at,omitempty"`
	LastSealAt  time.Time `json:"last_seal_at,omitempty"`
	OpenCount   int       `json:"open_count"`
	SealAmount  float64   `json:"seal_amount"`
	Consecutive int       `json:"consecutive"`
	AsOf        time.Time `json:"as_of"`
}

// LimitEvent 是封板、炸板、回封。
type LimitEvent struct {
	Symbol     string    `json:"symbol"`
	Direction  string    `json:"direction"`
	Kind       string    `json:"kind"`
	Time       time.Time `json:"time"`
	Price      float64   `json:"price"`
	SealAmount float64   `json:"seal_amount"`
	OpenCount  int       `json:"open_count"`
}

type side struct {
	dir     string
	price   float64
	touched bool
	sealed  bool
	first   time.Time
	last    time.Time
	opens   int
	amount  float64
	asOf    time.Time
}

// LimitTracker 跟踪一只股票当天的涨跌停状态。
type LimitTracker struct {
	Symbol  string
	Day     time.Time
	prevCon int // 前一交易日涨停连板数，未涨停为 0
	up      side
	down    side
	enabled bool
}

// NewLimitTracker 按前收盘和幅度算出当日涨跌停价。noLimit 为真（新股前 5 个交易日）时不跟踪。
func NewLimitTracker(symbol string, day time.Time, preClose, ratio float64, prevConsecutive int, noLimit bool) *LimitTracker {
	t := &LimitTracker{Symbol: symbol, Day: dateOf(day), prevCon: prevConsecutive}
	if noLimit || preClose <= 0 || ratio <= 0 {
		return t
	}
	upPx, downPx := ashare.Limit(preClose, ratio)
	t.up = side{dir: DirUp, price: upPx}
	t.down = side{dir: DirDown, price: downPx}
	t.enabled = true
	return t
}

// Enabled 为假时这只股票当天没有涨跌停限制。
func (t *LimitTracker) Enabled() bool { return t.enabled }

// Prices 返回涨停价和跌停价。
func (t *LimitTracker) Prices() (up, down float64) { return t.up.price, t.down.price }

// Push 用一条快照更新状态，返回状态变化。
// 涨停封板：最新价等于涨停价且卖一为空；跌停封板：最新价等于跌停价且买一为空。
func (t *LimitTracker) Push(s Snapshot) []LimitEvent {
	if !t.enabled || s.Last <= 0 {
		return nil
	}
	var out []LimitEvent
	bidPx, bidVol := s.Bid1()
	askPx, askVol := s.Ask1()

	upSealed := s.Last >= t.up.price-priceEps && askVol == 0
	upAmount := 0.0
	if upSealed && bidPx >= t.up.price-priceEps {
		upAmount = float64(bidVol) * t.up.price
	}
	touchedUp := s.High >= t.up.price-priceEps
	if e, ok := t.up.update(t.Symbol, s, upSealed, touchedUp, upAmount); ok {
		out = append(out, e)
	}

	downSealed := s.Last <= t.down.price+priceEps && bidVol == 0
	downAmount := 0.0
	if downSealed && askPx > 0 && askPx <= t.down.price+priceEps {
		downAmount = float64(askVol) * t.down.price
	}
	touchedDown := s.Low > 0 && s.Low <= t.down.price+priceEps
	if e, ok := t.down.update(t.Symbol, s, downSealed, touchedDown, downAmount); ok {
		out = append(out, e)
	}
	return out
}

func (sd *side) update(symbol string, s Snapshot, sealed, touched bool, amount float64) (LimitEvent, bool) {
	sd.asOf = s.Time
	if touched || sealed {
		sd.touched = true
	}
	var kind string
	switch {
	case sealed && !sd.sealed:
		if sd.first.IsZero() {
			sd.first = s.Time
			kind = EventSeal
		} else {
			kind = EventReseal
		}
		sd.last = s.Time
	case !sealed && sd.sealed:
		sd.opens++
		kind = EventBreak
	}
	sd.sealed = sealed
	if sealed {
		sd.amount = amount
	}
	if kind == "" {
		return LimitEvent{}, false
	}
	return LimitEvent{
		Symbol: symbol, Direction: sd.dir, Kind: kind, Time: s.Time,
		Price: sd.price, SealAmount: sd.amount, OpenCount: sd.opens,
	}, true
}

// Boards 返回当前状态：封住为 SEALED，触板没封住为 BROKEN，没碰到不返回。
func (t *LimitTracker) Boards() []LimitBoard {
	if !t.enabled {
		return nil
	}
	var out []LimitBoard
	for _, sd := range []side{t.up, t.down} {
		if !sd.touched {
			continue
		}
		b := LimitBoard{
			Symbol: t.Symbol, TradeDate: t.Day, Direction: sd.dir, LimitPrice: sd.price,
			FirstSealAt: sd.first, LastSealAt: sd.last, OpenCount: sd.opens,
			SealAmount: sd.amount, AsOf: sd.asOf, Status: StatusBroken,
		}
		if sd.sealed {
			b.Status = StatusSealed
			if sd.dir == DirUp {
				b.Consecutive = t.prevCon + 1
			}
		}
		out = append(out, b)
	}
	return out
}

// BoardsFromDaily 用日线补算涨跌停，没有封板时间和封单。用于盘后补数和回测。
func BoardsFromDaily(symbol string, b DayBar, ratio float64, prevConsecutive int, noLimit bool) []LimitBoard {
	pre := b.PreClose
	if noLimit || pre <= 0 || ratio <= 0 {
		return nil
	}
	upPx, downPx := ashare.Limit(pre, ratio)
	day := dateOf(b.Time)
	asOf := closeTime(day)
	var out []LimitBoard
	if b.High >= upPx-priceEps {
		lb := LimitBoard{Symbol: symbol, TradeDate: day, Direction: DirUp, LimitPrice: upPx, Status: StatusBroken, AsOf: asOf}
		if b.Close >= upPx-priceEps {
			lb.Status = StatusSealed
			lb.Consecutive = prevConsecutive + 1
		}
		out = append(out, lb)
	}
	if b.Low > 0 && b.Low <= downPx+priceEps {
		lb := LimitBoard{Symbol: symbol, TradeDate: day, Direction: DirDown, LimitPrice: downPx, Status: StatusBroken, AsOf: asOf}
		if b.Close <= downPx+priceEps {
			lb.Status = StatusSealed
		}
		out = append(out, lb)
	}
	return out
}

// NoLimit 判断 day 是否仍在上市后前 5 个交易日内（含上市日）。
func NoLimit(cal *tradecal.Calendar, listDate, day time.Time) bool {
	if listDate.IsZero() {
		return false
	}
	l, d := dateOf(listDate), dateOf(day)
	if d.Before(l) {
		return true
	}
	if d.Sub(l) > 40*24*time.Hour {
		return false
	}
	if cal == nil {
		cal = tradecal.Default
	}
	n := 0
	for x := l; !x.After(d); x = x.AddDate(0, 0, 1) {
		open, err := cal.Open(x)
		if err != nil {
			return false
		}
		if open {
			n++
		}
	}
	return n <= ashare.NoLimitDays
}

func dateOf(t time.Time) time.Time {
	d := t.In(tradecal.Shanghai())
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, tradecal.Shanghai())
}

// DateOf 返回 t 在上海时区的日期零点。
func DateOf(t time.Time) time.Time { return dateOf(t) }

func closeTime(day time.Time) time.Time {
	return dateOf(day).Add(15 * time.Hour)
}

// CloseTime 返回当日 15:00，收盘因子的 as_of。
func CloseTime(day time.Time) time.Time { return closeTime(day) }
