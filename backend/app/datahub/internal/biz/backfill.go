package biz

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// BackfillDomains 是支持补数的数据域。
var BackfillDomains = map[string]bool{
	DomainDailyBar: true, DomainAdjFactor: true, DomainMinuteBar: true, DomainMoneyFlow: true,
	DomainLhb: true, DomainMargin: true, DomainHsgtTop10: true, DomainLimitPool: true,
	DomainFinance: true, DomainSecurity: true, DomainSector: true,
}

// SubmitBackfill 校验并登记补数任务，由 core 的工作协程异步执行。FR-01-09。
func (c *Collector) SubmitBackfill(ctx context.Context, job BackfillJob) (BackfillJob, error) {
	if !BackfillDomains[job.Domain] {
		return BackfillJob{}, fmt.Errorf("datahub: %s 不支持补数", job.Domain)
	}
	if !c.p.DomainEnabled(job.Domain) {
		return BackfillJob{}, fmt.Errorf("datahub: %s 未启用", job.Domain)
	}
	if job.Source != "" {
		if _, ok := c.p.Source(job.Source); !ok {
			return BackfillJob{}, fmt.Errorf("datahub: 没有源 %s", job.Source)
		}
	}
	job.Start, job.End = dateOf(job.Start), dateOf(job.End)
	if job.End.Before(job.Start) {
		return BackfillJob{}, fmt.Errorf("datahub: 结束日期早于开始日期")
	}
	if job.End.Sub(job.Start) > 366*24*time.Hour {
		return BackfillJob{}, fmt.Errorf("datahub: 一次补数不超过一年")
	}
	days, err := c.tradingDays(job.Start, job.End)
	if err != nil {
		return BackfillJob{}, err
	}
	job.Total = len(days)
	job.Status = JobPending
	return c.repo.CreateBackfill(ctx, job)
}

// RunBackfill 逐个交易日补数。单日失败不影响其他日，最后有失败则任务为 failed。
func (c *Collector) RunBackfill(ctx context.Context, job BackfillJob) BackfillJob {
	now := c.now()
	job.Status = JobRunning
	job.StartedAt = &now
	_ = c.repo.UpdateBackfill(ctx, job)
	var errs []string
	run := func(day time.Time) (Result, error) {
		switch job.Domain {
		case DomainDailyBar:
			return c.CollectDailyBars(ctx, day, job.Symbols, job.Source)
		case DomainAdjFactor:
			return c.CollectAdjFactors(ctx, day, job.Symbols, job.Source)
		case DomainMinuteBar:
			return c.CollectMinuteBars(ctx, day, job.Symbols, job.Source)
		case DomainMoneyFlow:
			return c.CollectMoneyFlow(ctx, day, job.Source)
		case DomainLhb:
			return c.CollectLhb(ctx, day, job.Source)
		case DomainMargin:
			return c.CollectMargin(ctx, day, job.Source)
		case DomainHsgtTop10:
			return c.CollectHsgtTop10(ctx, day, job.Source)
		case DomainLimitPool:
			return c.CollectLimitPool(ctx, day, job.Source)
		}
		return Result{}, fmt.Errorf("datahub: %s 不支持补数", job.Domain)
	}
	switch job.Domain {
	case DomainFinance:
		r, err := c.CollectFinance(ctx, job.Start, job.Source)
		job.Rows += int64(r.Rows)
		job.Done = job.Total
		if err != nil {
			errs = append(errs, err.Error())
		}
	case DomainSecurity, DomainSector:
		var r Result
		var err error
		if job.Domain == DomainSecurity {
			r, err = c.CollectSecurities(ctx, job.Source)
		} else {
			r, err = c.CollectSectors(ctx, job.Source)
		}
		job.Rows += int64(r.Rows)
		job.Done = job.Total
		if err != nil {
			errs = append(errs, err.Error())
		}
	default:
		days, err := c.tradingDays(job.Start, job.End)
		if err != nil {
			errs = append(errs, err.Error())
			break
		}
		for _, day := range days {
			if ctx.Err() != nil {
				errs = append(errs, ctx.Err().Error())
				break
			}
			r, err := run(day)
			job.Rows += int64(r.Rows)
			job.Done++
			if err != nil {
				errs = append(errs, day.Format("2006-01-02")+": "+err.Error())
			}
			_ = c.repo.UpdateBackfill(ctx, job)
		}
	}
	end := c.now()
	job.FinishedAt = &end
	job.Status = JobSuccess
	if len(errs) > 0 {
		job.Status = JobFailed
		msg := strings.Join(errs, "\n")
		if len(msg) > 4000 {
			msg = msg[:4000]
		}
		job.Error = msg
	}
	if err := c.repo.UpdateBackfill(ctx, job); err != nil {
		c.log.Warnf("backfill %d: %v", job.ID, err)
	}
	return job
}

func (c *Collector) tradingDays(from, to time.Time) ([]time.Time, error) {
	var out []time.Time
	for d := dateOf(from); !d.After(to); d = d.AddDate(0, 0, 1) {
		open, err := c.cal.Open(d)
		if err != nil {
			return nil, err
		}
		if open {
			out = append(out, d)
		}
	}
	return out, nil
}
