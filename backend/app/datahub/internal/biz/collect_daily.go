package biz

import (
	"context"
	"fmt"
	"time"
)

// CollectSecurities 同步证券基础信息。
func (c *Collector) CollectSecurities(ctx context.Context, only string) (Result, error) {
	items, src, err := Run(ctx, c.p, DomainSecurity, RunOptions{Only: only, Timeout: 2 * time.Minute},
		func(ctx context.Context, s Source) ([]Security, error) {
			return s.(SecuritySource).Securities(ctx)
		},
		func(v []Security) error {
			if len(v) < 3000 {
				return fmt.Errorf("证券列表只有 %d 只，少于 3000", len(v))
			}
			return nil
		})
	if err != nil {
		return Result{Domain: DomainSecurity}, err
	}
	day := dateOf(c.now())
	n, err := c.repo.UpsertSecurities(ctx, items, src, ready(DomainSecurity, day, len(items), src, false))
	return Result{Domain: DomainSecurity, Source: src, Rows: n}, err
}

// CollectDailyBars 采某个交易日的日线。symbols 为空表示全市场。
func (c *Collector) CollectDailyBars(ctx context.Context, day time.Time, symbols []string, only string) (Result, error) {
	return c.collectBars(ctx, DomainDailyBar, "1d", day, symbols, only)
}

// CollectMinuteBars 采某个交易日的 1 分钟和 5 分钟线。
func (c *Collector) CollectMinuteBars(ctx context.Context, day time.Time, symbols []string, only string) (Result, error) {
	total := Result{Domain: DomainMinuteBar}
	for _, freq := range []string{"1m", "5m"} {
		r, err := c.collectBars(ctx, DomainMinuteBar, freq, day, symbols, only)
		if err != nil {
			return total, fmt.Errorf("%s: %w", freq, err)
		}
		total.Rows += r.Rows
		total.Source = r.Source
		total.Stale = total.Stale || r.Stale
	}
	return total, nil
}

func (c *Collector) collectBars(ctx context.Context, domain, freq string, day time.Time, symbols []string, only string) (Result, error) {
	day = dateOf(day)
	q := BarQuery{Freq: freq, Start: day, End: day, Expect: day, Symbols: symbols}
	var expect []string
	if len(symbols) == 0 {
		var err error
		if expect, err = c.repo.ActiveSymbols(ctx, day); err != nil {
			return Result{Domain: domain}, err
		}
	} else {
		expect = symbols
	}
	var issues []Issue
	var coverage float64 = 1
	batch, src, err := Run(ctx, c.p, domain, RunOptions{Only: only, Timeout: 10 * time.Minute},
		func(ctx context.Context, s Source) (*BarBatch, error) {
			var b BarBatch
			var err error
			if freq == "1d" {
				b, err = s.(DailyBarSource).DailyBars(ctx, q)
			} else {
				b, err = s.(MinuteBarSource).MinuteBars(ctx, q)
			}
			return &b, err
		},
		func(b *BarBatch) error {
			if b.Stale && len(b.Bars) == 0 {
				return &StaleError{Reason: fmt.Sprintf("本地数据最后日期 %s，应为 %s，请在通达信终端做盘后数据下载", b.LastDate, b.Expect)}
			}
			clean, iss := CheckBars(domain, b.Bars, q, c.opt.MaxBarPct)
			b.Bars = clean
			issues = iss
			if len(b.Bars) == 0 {
				return fmt.Errorf("%s %s 没有 K 线", freq, day.Format("2006-01-02"))
			}
			got := distinctSymbols(b.Bars)
			coverage, _ = Completeness(got, expect)
			if coverage < c.opt.CompletenessFail {
				return fmt.Errorf("%s 覆盖率 %.1f%%，低于 %.0f%%", freq, coverage*100, c.opt.CompletenessFail*100)
			}
			return nil
		})
	if err != nil {
		if reason, ok := staleReason(err); ok {
			c.saveIssues(ctx, domain, "", day, []Issue{{Check: CheckStale, Severity: "error", Message: reason}})
			c.alertOnce(ctx, domain+":"+day.Format("20060102"), "error", "数据不是最新："+domain, reason)
		}
		return Result{Domain: domain}, err
	}
	if coverage < c.opt.CompletenessWarn {
		_, missing := Completeness(distinctSymbols(batch.Bars), expect)
		issues = append(issues, Issue{Check: CheckCompleteness, Severity: "warn",
			Message: fmt.Sprintf("%s 覆盖率 %.2f%%，缺 %d 只", freq, coverage*100, len(missing)),
			Detail:  map[string]any{"missing": head(missing, 50), "expect": len(expect)}})
	}
	if batch.Stale {
		issues = append(issues, Issue{Check: CheckStale, Severity: "error",
			Message: fmt.Sprintf("本地数据最后日期 %s，应为 %s；已入库的只有历史部分", batch.LastDate, batch.Expect)})
	}
	n, err := c.repo.UpsertBars(ctx, batch.Bars, src, ready(domain, day, len(batch.Bars), src, batch.Stale))
	c.saveIssues(ctx, domain, src, day, issues)
	if err != nil {
		return Result{Domain: domain, Source: src}, err
	}
	return Result{Domain: domain, Source: src, Rows: n, Stale: batch.Stale}, nil
}

// CollectAdjFactors 采某日的复权因子并更新当日日线。依赖日线已入库。
func (c *Collector) CollectAdjFactors(ctx context.Context, day time.Time, symbols []string, only string) (Result, error) {
	day = dateOf(day)
	if len(symbols) == 0 {
		var err error
		if symbols, err = c.repo.ActiveSymbols(ctx, day); err != nil {
			return Result{Domain: DomainAdjFactor}, err
		}
	}
	items, src, err := Run(ctx, c.p, DomainAdjFactor, RunOptions{Only: only, Timeout: 20 * time.Minute},
		func(ctx context.Context, s Source) ([]AdjFactor, error) {
			return s.(AdjFactorSource).AdjFactors(ctx, day, symbols)
		},
		func(v []AdjFactor) error {
			if len(v) == 0 {
				return fmt.Errorf("复权因子为空")
			}
			for _, a := range v {
				if a.Factor <= 0 {
					return fmt.Errorf("%s 复权因子 %v 非正", a.Symbol, a.Factor)
				}
			}
			return nil
		})
	if err != nil {
		return Result{Domain: DomainAdjFactor}, err
	}
	n, err := c.repo.UpdateAdjFactors(ctx, items, ready(DomainAdjFactor, day, len(items), src, false))
	return Result{Domain: DomainAdjFactor, Source: src, Rows: n}, err
}

// CollectLimitPool 采涨停、跌停、炸板池。盘中每 10 秒，收盘后再采一次定稿。
func (c *Collector) CollectLimitPool(ctx context.Context, day time.Time, only string) (Result, error) {
	day = dateOf(day)
	items, src, err := Run(ctx, c.p, DomainLimitPool, RunOptions{Only: only},
		func(ctx context.Context, s Source) ([]LimitEntry, error) {
			return s.(LimitPoolSource).LimitPool(ctx, day)
		}, nonEmpty[LimitEntry]("涨停池"))
	if err != nil {
		return Result{Domain: DomainLimitPool}, err
	}
	n, err := c.repo.UpsertLimitPool(ctx, day, items, src, ready(DomainLimitPool, day, len(items), src, false))
	return Result{Domain: DomainLimitPool, Source: src, Rows: n}, err
}

// CollectMoneyFlow 采盘后个股资金流向。
func (c *Collector) CollectMoneyFlow(ctx context.Context, day time.Time, only string) (Result, error) {
	day = dateOf(day)
	expect, err := c.repo.ActiveSymbols(ctx, day)
	if err != nil {
		return Result{Domain: DomainMoneyFlow}, err
	}
	items, src, err := Run(ctx, c.p, DomainMoneyFlow, RunOptions{Only: only, Timeout: 2 * time.Minute},
		func(ctx context.Context, s Source) ([]MoneyFlow, error) {
			fs, ok := s.(MoneyFlowSource)
			if !ok {
				return nil, ErrUnsupported
			}
			return fs.MoneyFlows(ctx, day)
		},
		func(v []MoneyFlow) error {
			syms := make([]string, len(v))
			for i, m := range v {
				syms[i] = m.Symbol
			}
			if cov, _ := Completeness(syms, expect); cov < c.opt.CompletenessFail {
				return fmt.Errorf("资金流向覆盖率 %.1f%%", cov*100)
			}
			return nil
		})
	if err != nil {
		return Result{Domain: DomainMoneyFlow}, err
	}
	n, err := c.repo.UpsertMoneyFlows(ctx, day, items, src, ready(DomainMoneyFlow, day, len(items), src, false))
	return Result{Domain: DomainMoneyFlow, Source: src, Rows: n}, err
}

// CollectLhb 采龙虎榜席位。
func (c *Collector) CollectLhb(ctx context.Context, day time.Time, only string) (Result, error) {
	day = dateOf(day)
	items, src, err := Run(ctx, c.p, DomainLhb, RunOptions{Only: only, Timeout: 2 * time.Minute},
		func(ctx context.Context, s Source) ([]LhbSeat, error) {
			return s.(LhbSource).LhbSeats(ctx, day)
		}, nonEmpty[LhbSeat]("龙虎榜"))
	if err != nil {
		return Result{Domain: DomainLhb}, err
	}
	n, err := c.repo.UpsertLhbSeats(ctx, day, items, ready(DomainLhb, day, len(items), src, false))
	return Result{Domain: DomainLhb, Source: src, Rows: n}, err
}

// CollectMargin 采融资融券。数据次日早上才有，调度在次交易日盘前抓上一交易日。
func (c *Collector) CollectMargin(ctx context.Context, day time.Time, only string) (Result, error) {
	day = dateOf(day)
	items, src, err := Run(ctx, c.p, DomainMargin, RunOptions{Only: only, Timeout: 3 * time.Minute},
		func(ctx context.Context, s Source) ([]Margin, error) {
			return s.(MarginSource).Margins(ctx, day)
		},
		func(v []Margin) error {
			if len(v) < 1000 {
				return fmt.Errorf("两融明细只有 %d 只", len(v))
			}
			return nil
		})
	if err != nil {
		return Result{Domain: DomainMargin}, err
	}
	n, err := c.repo.UpsertMargins(ctx, day, items, src, ready(DomainMargin, day, len(items), src, false))
	return Result{Domain: DomainMargin, Source: src, Rows: n}, err
}

// CollectHsgtTop10 采沪深股通十大成交股。
func (c *Collector) CollectHsgtTop10(ctx context.Context, day time.Time, only string) (Result, error) {
	day = dateOf(day)
	items, src, err := Run(ctx, c.p, DomainHsgtTop10, RunOptions{Only: only},
		func(ctx context.Context, s Source) ([]HsgtTop, error) {
			return s.(HsgtSource).HsgtTop10(ctx, day)
		}, nonEmpty[HsgtTop]("沪深股通十大成交股"))
	if err != nil {
		return Result{Domain: DomainHsgtTop10}, err
	}
	n, err := c.repo.UpsertHsgtTop10(ctx, day, items, src, ready(DomainHsgtTop10, day, len(items), src, false))
	return Result{Domain: DomainHsgtTop10, Source: src, Rows: n}, err
}

// CollectSectors 同步板块与成分。成分消失的记 out_date，新出现的记 in_date。
func (c *Collector) CollectSectors(ctx context.Context, only string) (Result, error) {
	items, src, err := Run(ctx, c.p, DomainSector, RunOptions{Only: only, Timeout: 5 * time.Minute},
		func(ctx context.Context, s Source) ([]Sector, error) {
			return s.(SectorSource).Sectors(ctx)
		},
		func(v []Sector) error {
			members := 0
			for _, s := range v {
				members += len(s.Members)
			}
			if len(v) < 50 || members < 3000 {
				return fmt.Errorf("板块 %d 个、成分 %d 条，数量不足", len(v), members)
			}
			return nil
		})
	if err != nil {
		return Result{Domain: DomainSector}, err
	}
	day := dateOf(c.now())
	n, err := c.repo.SyncSectors(ctx, day, items, src, ready(DomainSector, day, len(items), src, false))
	return Result{Domain: DomainSector, Source: src, Rows: n}, err
}

// CollectFinance 采最近一周公告的业绩预告、快报。
func (c *Collector) CollectFinance(ctx context.Context, since time.Time, only string) (Result, error) {
	items, src, err := Run(ctx, c.p, DomainFinance, RunOptions{Only: only, Timeout: 3 * time.Minute},
		func(ctx context.Context, s Source) ([]FinanceItem, error) {
			return s.(FinanceSource).Finance(ctx, since)
		}, nil)
	if err != nil {
		return Result{Domain: DomainFinance}, err
	}
	day := dateOf(c.now())
	n, err := c.repo.UpsertFinance(ctx, items, src, ready(DomainFinance, day, len(items), src, false))
	return Result{Domain: DomainFinance, Source: src, Rows: n}, err
}

// CollectOverseas 采外围行情。
func (c *Collector) CollectOverseas(ctx context.Context) (Result, error) {
	items, src, err := Run(ctx, c.p, DomainOverseas, RunOptions{Timeout: time.Minute},
		func(ctx context.Context, s Source) ([]OverseasQuote, error) {
			return s.(OverseasSource).Overseas(ctx)
		},
		func(v []OverseasQuote) error {
			if len(v) < 3 {
				return fmt.Errorf("外围行情只有 %d 项", len(v))
			}
			return nil
		})
	if err != nil {
		return Result{Domain: DomainOverseas}, err
	}
	day := dateOf(c.now())
	n, err := c.repo.UpsertOverseas(ctx, items, src, ready(DomainOverseas, day, len(items), src, false))
	return Result{Domain: DomainOverseas, Source: src, Rows: n}, err
}

// CollectMacro 采宏观序列。
func (c *Collector) CollectMacro(ctx context.Context) (Result, error) {
	items, src, err := Run(ctx, c.p, DomainMacro, RunOptions{Timeout: 3 * time.Minute},
		func(ctx context.Context, s Source) ([]MacroPoint, error) {
			return s.(MacroSource).Macro(ctx)
		}, nonEmpty[MacroPoint]("宏观序列"))
	if err != nil {
		return Result{Domain: DomainMacro}, err
	}
	day := dateOf(c.now())
	n, err := c.repo.UpsertMacro(ctx, items, src, ready(DomainMacro, day, len(items), src, false))
	return Result{Domain: DomainMacro, Source: src, Rows: n}, err
}

// CollectHotRank 采热榜，时间按 5 分钟取整。
func (c *Collector) CollectHotRank(ctx context.Context) (Result, error) {
	items, src, err := Run(ctx, c.p, DomainHotRank, RunOptions{},
		func(ctx context.Context, s Source) ([]HotItem, error) {
			return s.(HotRankSource).HotRank(ctx)
		}, nonEmpty[HotItem]("热榜"))
	if err != nil {
		return Result{Domain: DomainHotRank}, err
	}
	at := c.now().Truncate(5 * time.Minute)
	n, err := c.repo.InsertHotRank(ctx, at, items, src)
	return Result{Domain: DomainHotRank, Source: src, Rows: n}, err
}

func distinctSymbols(bars []Bar) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, b := range bars {
		if _, ok := seen[b.Symbol]; !ok {
			seen[b.Symbol] = struct{}{}
			out = append(out, b.Symbol)
		}
	}
	return out
}

func head(s []string, n int) []string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
