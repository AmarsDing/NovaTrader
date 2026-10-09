package backtest

import (
	"context"
	"fmt"
	"hash/fnv"
	"time"
)

// LookaheadResult 是截断自检的结论：切点日之前做出的委托决策、成交和权益必须与打乱后的运行完全相同。
type LookaheadResult struct {
	Cut            string `json:"cut"`
	OrdersCompared int    `json:"orders_compared"`
	TradesCompared int    `json:"trades_compared"`
	DaysCompared   int    `json:"days_compared"`
	Pass           bool   `json:"pass"`
	FirstDiff      string `json:"first_diff,omitempty"`
}

// LookaheadCheck 以 base 的区间中点为切点日，打乱切点日及之后的价格和成交量重跑，比对切点日之前的结果。
func LookaheadCheck(ctx context.Context, cfg Config, src Source, base *Result) (*LookaheadResult, error) {
	if len(base.Equity) < 2 {
		return &LookaheadResult{Pass: true, FirstDiff: "交易日不足 2 天，跳过"}, nil
	}
	cut := base.Equity[len(base.Equity)/2].Day
	alt, err := Run(ctx, cfg, &perturbed{Source: src, cut: cut, seed: cfg.Seed}, nil)
	if err != nil {
		return nil, fmt.Errorf("backtest: lookahead rerun: %w", err)
	}
	out := &LookaheadResult{Cut: dayKey(cut), Pass: true}
	decided := func(os []OrderLog) []OrderLog {
		var o []OrderLog
		for _, x := range os {
			if x.Time.Before(cut) {
				o = append(o, x)
			}
		}
		return o
	}
	oa, ob := decided(base.Orders), decided(alt.Orders)
	out.OrdersCompared = len(oa)
	for i := 0; i < len(oa) || i < len(ob); i++ {
		if i >= len(oa) || i >= len(ob) || oa[i] != ob[i] {
			var x OrderLog
			if i < len(oa) {
				x = oa[i]
			} else {
				x = ob[i]
			}
			return out.fail(fmt.Sprintf("第 %d 个委托决策不同（%s %s %s）：切点日前的决策用到了切点日及之后的数据", i+1,
				x.Symbol, x.Side, x.Time.Format("2006-01-02 15:04"))), nil
		}
	}
	before := func(ts []Trade) []Trade {
		var o []Trade
		for _, t := range ts {
			if Day(t.Time).Before(cut) {
				o = append(o, t)
			}
		}
		return o
	}
	a, b := before(base.Trades), before(alt.Trades)
	out.TradesCompared = len(a)
	for i := 0; i < len(a) || i < len(b); i++ {
		switch {
		case i >= len(a):
			return out.fail(fmt.Sprintf("打乱后多出成交 %s %s %s", b[i].Symbol, b[i].Side, b[i].Time.Format("2006-01-02 15:04"))), nil
		case i >= len(b):
			return out.fail(fmt.Sprintf("打乱后少了成交 %s %s %s", a[i].Symbol, a[i].Side, a[i].Time.Format("2006-01-02 15:04"))), nil
		case !sameTrade(a[i], b[i]):
			return out.fail(fmt.Sprintf("第 %d 笔成交不同：%s %s %.2f×%d vs %s %s %.2f×%d", i+1,
				a[i].Symbol, a[i].Time.Format("01-02 15:04"), a[i].Price, a[i].Qty,
				b[i].Symbol, b[i].Time.Format("01-02 15:04"), b[i].Price, b[i].Qty)), nil
		}
	}
	for i := range base.Equity {
		if !base.Equity[i].Day.Before(cut) {
			break
		}
		out.DaysCompared++
		if i >= len(alt.Equity) || alt.Equity[i].Equity != base.Equity[i].Equity {
			return out.fail("权益在 " + dayKey(base.Equity[i].Day) + " 不同"), nil
		}
	}
	return out, nil
}

func (r *LookaheadResult) fail(msg string) *LookaheadResult {
	r.Pass, r.FirstDiff = false, msg
	return r
}

func sameTrade(a, b Trade) bool {
	return a.Symbol == b.Symbol && a.Side == b.Side && a.Time.Equal(b.Time) && a.Price == b.Price && a.Qty == b.Qty && a.Fee == b.Fee
}

// perturbed 把 cut 及之后每根 K 线的价格乘一个 0.8–1.2 的系数、成交量乘另一个系数。系数按股票和日期固定。
type perturbed struct {
	Source
	cut  time.Time
	seed int64
}

func (p *perturbed) factor(sym string, day time.Time, salt string) float64 {
	h := fnv.New64a()
	fmt.Fprintf(h, "%d|%s|%s|%s", p.seed, sym, dayKey(day), salt)
	return 0.8 + 0.4*float64(h.Sum64()%1_000_000)/1_000_000
}

func (p *perturbed) DailyBars(ctx context.Context, from, to time.Time) (map[string][]DayBar, error) {
	in, err := p.Source.DailyBars(ctx, from, to)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]DayBar, len(in))
	for sym, bars := range in {
		cp := make([]DayBar, len(bars))
		for i, b := range bars {
			if !b.Day.Before(p.cut) {
				k, kv := p.factor(sym, b.Day, "p"), p.factor(sym, b.Day, "v")
				b.Open, b.High, b.Low, b.Close, b.PreClose = b.Open*k, b.High*k, b.Low*k, b.Close*k, b.PreClose*k
				b.Volume = int64(float64(b.Volume) * kv)
				b.Amount *= k * kv
			}
			cp[i] = b
		}
		out[sym] = cp
	}
	return out, nil
}

func (p *perturbed) MinuteBars(ctx context.Context, day time.Time, symbols []string) (map[string][]MinBar, error) {
	in, err := p.Source.MinuteBars(ctx, day, symbols)
	if err != nil || day.Before(p.cut) {
		return in, err
	}
	out := make(map[string][]MinBar, len(in))
	for sym, bars := range in {
		k, kv := p.factor(sym, day, "p"), p.factor(sym, day, "v")
		cp := make([]MinBar, len(bars))
		for i, b := range bars {
			b.Open, b.High, b.Low, b.Close = b.Open*k, b.High*k, b.Low*k, b.Close*k
			b.Volume = int64(float64(b.Volume) * kv)
			b.Amount *= k * kv
			cp[i] = b
		}
		out[sym] = cp
	}
	return out, nil
}
