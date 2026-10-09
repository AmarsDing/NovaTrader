package biz

import (
	"context"
	"fmt"
	"time"
)

// RunDomain 按数据域名采集一次。date 为空则用最近一个交易日。供 RPC 和调度调用。
func (c *Collector) RunDomain(ctx context.Context, domain, date, only string) (Result, error) {
	day, err := c.parseDay(date)
	if err != nil {
		return Result{Domain: domain}, err
	}
	switch domain {
	case DomainSnapshot:
		meta, err := c.CollectSnapshot(ctx)
		return Result{Domain: DomainSnapshot, Source: meta.Source, Rows: meta.Count, Stale: meta.Stale}, err
	case DomainSectorQuote:
		return c.CollectSectorQuotes(ctx)
	case DomainSecurity:
		return c.CollectSecurities(ctx, only)
	case DomainDailyBar:
		return c.CollectDailyBars(ctx, day, nil, only)
	case DomainMinuteBar:
		return c.CollectMinuteBars(ctx, day, nil, only)
	case DomainAdjFactor:
		return c.CollectAdjFactors(ctx, day, nil, only)
	case DomainLimitPool:
		return c.CollectLimitPool(ctx, day, only)
	case DomainMoneyFlow:
		return c.CollectMoneyFlow(ctx, day, only)
	case DomainLhb:
		return c.CollectLhb(ctx, day, only)
	case DomainMargin:
		return c.CollectMargin(ctx, day, only)
	case DomainHsgtTop10:
		return c.CollectHsgtTop10(ctx, day, only)
	case DomainSector:
		return c.CollectSectors(ctx, only)
	case DomainFinance:
		return c.CollectFinance(ctx, day.AddDate(0, 0, -7), only)
	case DomainOverseas:
		return c.CollectOverseas(ctx)
	case DomainMacro:
		return c.CollectMacro(ctx)
	case DomainHotRank:
		return c.CollectHotRank(ctx)
	case DomainFlash, DomainNews, DomainAnnouncement, DomainReport:
		return c.CollectIntel(ctx, domain)
	default:
		return Result{}, fmt.Errorf("datahub: unknown domain %s", domain)
	}
}

func (c *Collector) parseDay(date string) (time.Time, error) {
	if date == "" {
		return c.LastOpenDay(c.now())
	}
	t, err := time.ParseInLocation("2006-01-02", date, shanghai())
	if err != nil {
		return time.Time{}, fmt.Errorf("datahub: date %q, want YYYY-MM-DD", date)
	}
	return t, nil
}

// LastOpenDay 返回 now 当天或之前最近一个交易日。
func (c *Collector) LastOpenDay(now time.Time) (time.Time, error) {
	d := dateOf(now)
	for i := 0; i < 14; i++ {
		open, err := c.cal.Open(d)
		if err != nil {
			return time.Time{}, err
		}
		if open {
			return d, nil
		}
		d = d.AddDate(0, 0, -1)
	}
	return time.Time{}, fmt.Errorf("datahub: 近两周没有交易日")
}

// PreviousOpenDay 返回 day 之前最近一个交易日（不含 day）。两融在次日盘前抓。
func (c *Collector) PreviousOpenDay(day time.Time) (time.Time, error) {
	return c.LastOpenDay(dateOf(day).AddDate(0, 0, -1))
}

// PersistHealth 把运行统计写进 source_health。
func (c *Collector) PersistHealth(ctx context.Context) error {
	return c.repo.SaveHealth(ctx, c.p.Health())
}

// DispatchOutbox 投递 md.*.ready。
func (c *Collector) DispatchOutbox(ctx context.Context) (int, error) {
	return c.repo.DispatchOutbox(ctx, c.bus, 100)
}

// PurgeRaw 删掉 keep 天之前的原始响应。
func (c *Collector) PurgeRaw(ctx context.Context, keep time.Duration) (int, error) {
	if keep <= 0 {
		keep = 7 * 24 * time.Hour
	}
	return c.repo.PurgeRaw(ctx, c.now().Add(-keep))
}

func (c *Collector) SnapshotMeta(ctx context.Context) (SnapshotMeta, error) {
	return c.snap.Meta(ctx)
}

func (c *Collector) ListIssues(ctx context.Context, day *time.Time, domain string, limit int) ([]StoredIssue, error) {
	return c.repo.ListIssues(ctx, day, domain, limit)
}

func (c *Collector) GetBackfill(ctx context.Context, id int) (BackfillJob, error) {
	return c.repo.GetBackfill(ctx, id)
}

func (c *Collector) RecoverBackfills(ctx context.Context) ([]BackfillJob, error) {
	return c.repo.PendingBackfills(ctx)
}
