package market

import (
	"math"
	"time"

	"server/pkg/ashare"
)

// WithExecutionLimits attaches the already tracked rule limits for this exact
// engine day. Missing security metadata is unknown, never "no price limit".
func (e *Engine) WithExecutionLimits(b Bar) Bar {
	b.LimitKnown, b.NoLimit, b.LimitUp, b.LimitDown = false, false, nil, nil
	e.mu.RLock()
	defer e.mu.RUnlock()
	st := e.stocks[b.Symbol]
	if st == nil || !st.loaded || !sameDay(b.Time, e.day) || st.info.ListDate.IsZero() || dateOf(st.info.ListDate).After(e.day) {
		return b
	}
	// Calendar errors in the recent IPO window cannot be interpreted as an
	// ordinary price-limited stock by the legacy bool-only NoLimit helper.
	if e.day.Sub(dateOf(st.info.ListDate)) <= 40*24*time.Hour {
		for d := dateOf(st.info.ListDate); !d.After(e.day); d = d.AddDate(0, 0, 1) {
			if _, err := e.cfg.Calendar.Open(d); err != nil {
				return b
			}
		}
	}
	if NoLimit(e.cfg.Calendar, st.info.ListDate, e.day) {
		b.LimitKnown, b.NoLimit = true, true
		return b
	}
	if st.lim == nil || !st.lim.Enabled() {
		return b
	}
	up, down := st.lim.Prices()
	if up <= down || down <= 0 || math.IsNaN(up) || math.IsNaN(down) || math.IsInf(up, 0) || math.IsInf(down, 0) {
		return b
	}
	b.LimitKnown, b.LimitUp, b.LimitDown = true, &up, &down
	return b
}

// ExecutionPrices validates the explicit live-bus metadata. Returning false
// blocks runtime SIM matching; it never guesses limits from the stock code.
func (b Bar) ExecutionPrices() (up, down float64, ok bool) {
	if !b.LimitKnown {
		return 0, 0, false
	}
	for _, p := range []float64{b.Open, b.High, b.Low, b.Close} {
		if p <= 0 || math.IsNaN(p) || math.IsInf(p, 0) {
			return 0, 0, false
		}
	}
	if b.Low > b.High || b.Open > b.High || b.Open < b.Low || b.Close > b.High || b.Close < b.Low {
		return 0, 0, false
	}
	if b.NoLimit {
		return 0, 0, b.LimitUp == nil && b.LimitDown == nil
	}
	if b.LimitUp == nil || b.LimitDown == nil {
		return 0, 0, false
	}
	up, down = *b.LimitUp, *b.LimitDown
	for _, p := range []float64{up, down, b.Open, b.High, b.Low, b.Close} {
		if p <= 0 || math.IsNaN(p) || math.IsInf(p, 0) {
			return 0, 0, false
		}
	}
	if up <= down || math.Abs(up/ashare.Tick-math.Round(up/ashare.Tick)) > 1e-6 || math.Abs(down/ashare.Tick-math.Round(down/ashare.Tick)) > 1e-6 {
		return 0, 0, false
	}
	if b.Open < down-1e-6 || b.Low < down-1e-6 || b.High > up+1e-6 || b.Close > up+1e-6 || b.Low > b.High || b.Open > b.High || b.Open < b.Low || b.Close > b.High || b.Close < b.Low {
		return 0, 0, false
	}
	return up, down, true
}
