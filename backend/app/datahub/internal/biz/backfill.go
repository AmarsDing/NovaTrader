package biz

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// BackfillDomains 是支持补数的数据域。
// D10：unlock/block_trade 入册——sunk/retry 零行回补链直接提交这两域任务（2026-10-22）。
var BackfillDomains = map[string]bool{
	DomainDailyBar: true, DomainAdjFactor: true, DomainMinuteBar: true, DomainMoneyFlow: true,
	DomainLhb: true, DomainMargin: true, DomainMarginMkt: true, DomainHsgtTop10: true, DomainLimitPool: true,
	DomainFinance: true, DomainSecurity: true, DomainSector: true,
	DomainUnlock: true, DomainBlockTrade: true,
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
	var total int
	if job.Domain == DomainUnlock {
		// 窗口域：RunBackfill 单次采整窗（逐日循环会把同一窗口重复拉 N 次），
		// Total 用窗口日历天数——不依赖节假日表覆盖未来 N 天（年末常未发布，12 域口径这里没有交易语义）。
		total = int(job.End.Sub(job.Start)/(24*time.Hour)) + 1
	} else {
		days, err := c.tradingDays(job.Start, job.End)
		if err != nil {
			return BackfillJob{}, err
		}
		total = len(days)
	}
	job.Total = total
	job.Status = JobPending
	return c.repo.CreateBackfill(ctx, job)
}

// RunBackfill 逐个交易日补数。单日失败不影响其他日，最后有失败则任务为 failed。
func (c *Collector) RunBackfill(ctx context.Context, job BackfillJob) BackfillJob {
	ctx = OpenRunTrace(ctx)
	now := c.now()
	job.Status = JobRunning
	job.StartedAt = &now
	_ = c.repo.UpdateBackfill(ctx, job)
	var errs []string
	run := func(day time.Time) (CollectSummary, error) {
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
		case DomainMarginMkt:
			return c.CollectMarginMkt(ctx, day, job.Source)
		case DomainHsgtTop10:
			return c.CollectHsgtTop10(ctx, day, job.Source)
		case DomainLimitPool:
			return c.CollectLimitPool(ctx, day, job.Source)
		case DomainBlockTrade:
			return c.CollectBlockTrades(ctx, day, job.Source)
		}
		return CollectSummary{}, fmt.Errorf("datahub: %s 不支持补数", job.Domain)
	}
	switch job.Domain {
	case DomainUnlock:
		// D10：解禁窗口域一次调用即采完整个窗口（CollectUnlock 内卷 [start, start+90d)），
		// 逐日循环会把同一窗口重复拉 90 次。
		r, err := c.CollectUnlock(ctx, job.Start, job.Source)
		job.Rows += int64(r.Rows)
		job.Done = job.Total
		if err != nil {
			errs = append(errs, err.Error())
		}
	case DomainFinance:
		r, err := c.CollectFinance(ctx, job.Start, job.Source)
		job.Rows += int64(r.Rows)
		job.Done = job.Total
		if err != nil {
			errs = append(errs, err.Error())
		}
	case DomainSecurity, DomainSector:
		var r CollectSummary
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
