package biz

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"
)

// ReconcileOptions 是对账参数。
type ReconcileOptions struct {
	Sample   int
	PriceTol float64 // 收盘价绝对差，元
	VolTol   float64 // 成交量相对差
}

// Reconcile 抽样对账某日日线：入库值与另一个源的值比较，不一致写 reconcile 问题并标出两个来源。FR-01-15。
func (c *Collector) Reconcile(ctx context.Context, day time.Time, opt ReconcileOptions) (Result, error) {
	if opt.Sample <= 0 {
		opt.Sample = 50
	}
	if opt.PriceTol <= 0 {
		opt.PriceTol = 0.0101
	}
	if opt.VolTol <= 0 {
		opt.VolTol = 0.01
	}
	day = dateOf(day)
	stored, sources, err := c.repo.DailyBars(ctx, day, nil)
	if err != nil {
		return Result{Domain: DomainDailyBar}, err
	}
	if len(stored) == 0 {
		return Result{Domain: DomainDailyBar}, fmt.Errorf("%s 没有已入库的日线可对账", day.Format("2006-01-02"))
	}
	sample := pickSample(stored, opt.Sample)
	bySource := map[string][]string{}
	for _, s := range sample {
		bySource[sources[s]] = append(bySource[sources[s]], s)
	}
	cfg, _ := c.p.DomainConfig(DomainDailyBar)
	var issues []Issue
	checked := 0
	var lastErr error
	for stored0, syms := range bySource {
		other := ""
		for _, name := range cfg.Sources {
			if name != stored0 && name != "market_agg" {
				other = name
				break
			}
		}
		if other == "" {
			continue
		}
		q := BarQuery{Freq: "1d", Start: day, End: day, Expect: day, Symbols: syms}
		batch, used, err := Run(ctx, c.p, DomainDailyBar, RunOptions{Only: other, Timeout: 5 * time.Minute},
			func(ctx context.Context, s Source) (BarBatch, error) { return s.(DailyBarSource).DailyBars(ctx, q) }, nil)
		if err != nil {
			lastErr = err
			continue
		}
		got := map[string]Bar{}
		for _, b := range batch.Bars {
			got[b.Symbol] = b
		}
		for _, s := range syms {
			a := stored[s]
			b, ok := got[s]
			checked++
			if !ok {
				issues = append(issues, Issue{Domain: DomainDailyBar, Check: CheckReconcile, Symbol: s, TradeDate: day,
					Source: stored0, OtherSource: used, Message: used + " 没有这根日线"})
				continue
			}
			diffs := map[string]any{}
			if math.Abs(a.Close-b.Close) > opt.PriceTol {
				diffs["close"] = []float64{a.Close, b.Close}
			}
			if math.Abs(a.Open-b.Open) > opt.PriceTol {
				diffs["open"] = []float64{a.Open, b.Open}
			}
			if relDiff(float64(a.Volume), float64(b.Volume)) > opt.VolTol {
				diffs["volume"] = []int64{a.Volume, b.Volume}
			}
			if relDiff(a.Amount, b.Amount) > opt.VolTol*2 {
				diffs["amount"] = []float64{a.Amount, b.Amount}
			}
			if len(diffs) > 0 {
				issues = append(issues, Issue{Domain: DomainDailyBar, Check: CheckReconcile, Symbol: s, TradeDate: day,
					Severity: "warn", Source: stored0, OtherSource: used,
					Message: fmt.Sprintf("%s 与 %s 不一致：%v", stored0, used, keys(diffs)), Detail: diffs})
			}
		}
	}
	c.saveIssues(ctx, DomainDailyBar, "", day, issues)
	if checked == 0 && lastErr != nil {
		return Result{Domain: DomainDailyBar}, lastErr
	}
	return Result{Domain: DomainDailyBar, Rows: checked, Message: fmt.Sprintf("抽样 %d 只，不一致 %d 只", checked, len(issues))}, nil
}

// pickSample 均匀抽样，结果可复现。
func pickSample(stored map[string]Bar, n int) []string {
	all := make([]string, 0, len(stored))
	for s := range stored {
		all = append(all, s)
	}
	sort.Strings(all)
	if len(all) <= n {
		return all
	}
	step := float64(len(all)) / float64(n)
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, all[int(float64(i)*step)])
	}
	return out
}

func relDiff(a, b float64) float64 {
	m := math.Max(math.Abs(a), math.Abs(b))
	if m == 0 {
		return 0
	}
	return math.Abs(a-b) / m
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
