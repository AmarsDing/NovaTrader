package biz

import (
	"context"
	"fmt"
	"time"
)

// D10 零行数据页终态提醒（2026-10-22 定稿战略扩版）：空页本身是合法状态，但必须可观测。
//
// 两合同（均挂在「待补 backfill 任务」上，RequestedBy 前缀区分来路）：
//   - 空运行 sunk：调度入口每次采集后判 run.Rows<=0 → 立即登记 sunk 待补。
//     unlock 零行重采整窗（窗口范围用 job.Start/End 表达：start=run 日、end=+89 天，
//     零新列不占 ent 冻结位）；block_trade 等按日域重采当日（start=end=day）。
//     注：仅每日调度入口接钩；手动 RPC 链（service.Collect→RunDomain）本批未接守卫与钩子。
//   - sunk 确认回执：ConfirmSunkBackfills 轮询已完成的 sunk 任务，读窗口内已落行数
//     （DomainRows 端口）判定回补是否真落了数据：
//     >0 → 已回补确认（required 域之 stock 链本批简化：margin 在 RunBackfill 成功路径
//     已全链重采，不再复跑）；≤0 → 必填域（当前=block_trade）登记「retry:<domain>:<day>」
//     重查 sunk 差错（同域同日 marker 全状态单条——一经登记、无论终态如何绝不重复滚动，
//     否则真空页会在任务转 success 后护盾消失、热环重登记）；此后周期终止转人工；
//     非必填/unlock 直接终止周期、转净待下轮。
//
// 每条成功回落的任务只裁决一次（进程级 sunkDone）：裁决结果有三种——确认、
// 已登记重查、终止周期——均以任务终态+窗口行数为依据，复查不会翻案；重启后重判一轮兜底。
//
// 数据纪律闸：一律不按 0/diff 冒充（没取到≠0）；DomainRows 报未知即视为未确认，
// 绝不把「表面成功」当「真回补」（D7 transport_unknown 同一教训）。

func sunkMarker(domain string) string { return "sunk:" + domain }

func retryMarker(domain, dayKey string) string { return "retry:" + domain + ":" + dayKey }

// BackfillCovered 判断 day 是否已被 pending/running 回补任务覆盖（范围内即覆盖）。
// worker 每日采集入口守卫：有覆盖即跳过，避免与待执行任务重复开工。
func (c *Collector) BackfillCovered(ctx context.Context, domain string, day time.Time) (bool, error) {
	pending, err := c.repo.PendingBackfills(ctx)
	if err != nil {
		return false, err
	}
	day = dateOf(day)
	for _, job := range pending {
		if job.Domain == domain && !job.Start.After(day) && !job.End.Before(day) {
			return true, nil
		}
	}
	return false, nil
}

// SunkEmptyBackfill 空运行登记「待补」：run.Rows<=0 且当日无同日起点的 sunk 任务才登记；
// 同日重复运行（取证/回放）不重复登记，同日已有失败结果不再登记（失败即结果，按日节奏）。
// unlock 重采整窗、其它域重采当日；域不在可回补白名单（BackfillDomains）或未启用一律不登记（滤不可回补源）。
// 返回是否登记。
func (c *Collector) SunkEmptyBackfill(ctx context.Context, domain string, day time.Time, run CollectSummary) bool {
	day = dateOf(day)
	if !BackfillDomains[domain] || run.Rows > 0 || !c.p.DomainEnabled(domain) {
		return false
	}
	dayKey := day.Format("20060102")
	marker := sunkMarker(domain)
	pending, err := c.repo.PendingBackfills(ctx)
	if err != nil {
		c.log.Warnf("%s: sunk 取 pending 失败: %v", domain, err)
		return false
	}
	for _, job := range pending {
		if job.Domain == domain && job.RequestedBy == marker && job.Start.Equal(day) {
			return false
		}
	}
	if failed, err := c.repo.ListBackfills(ctx, domain, []string{JobFailed}, day.Add(-time.Hour)); err == nil {
		for _, job := range failed {
			// CreatedAt 是 timestamptz（读回为 UTC 渲染），同日口径归上海日历日。
			if job.CreatedAt.In(shanghai()).Format("20060102") == dayKey {
				return false
			}
		}
	}
	start, end := day, day
	if domain == DomainUnlock {
		end = day.AddDate(0, 0, 89)
	}
	job := BackfillJob{Domain: domain, Start: start, End: end, Source: "", RequestedBy: marker}
	if _, err := c.SubmitBackfill(ctx, job); err != nil {
		c.log.Warnf("%s: 零行 sunk登记 %s: %v", domain, dayKey, err)
		return false
	}
	c.saveIssues(ctx, domain, "", day, []Issue{{Check: CheckEmpty, Severity: "warn",
		Message: fmt.Sprintf("零行数据页已登记待补（%s %s）", domain, dayKey)}})
	return true
}

// ConfirmSunkBackfills 轮询已完成 sunk 任务的窗口行数，确认真回补/登记重查。
// worker backfillLoop 以 60s 节流调用（tick 本身是 2s）；非 sunk 域（及 retry:* 任务）一律不介入，
// 每条任务进程内只裁决一次（sunkDone）。
func (c *Collector) ConfirmSunkBackfills(ctx context.Context) {
	scope := map[string]bool{DomainBlockTrade: true, DomainUnlock: true}
	today := dateOf(c.now())
	for domain := range scope {
		done, err := c.repo.ListBackfills(ctx, domain, []string{JobSuccess}, today.AddDate(0, 0, -7))
		if err != nil {
			continue
		}
		for _, job := range done {
			if job.RequestedBy != sunkMarker(domain) {
				continue
			}
			if !c.sunkDone.mark(job.ID) {
				continue // 进程内已裁决：确认/重查/终止都只落一次
			}
			count, err := c.repo.DomainRows(ctx, domain, job.Start, job.End)
			dayKey := job.Start.Format("20060102")
			if err == nil && count > 0 {
				c.log.Infof("%s: sunk回补已确认（%s 窗口行数=%d）", domain, dayKey, count)
				continue
			}
			// err!=nil（读口未知）与 count<=0 同路：必填域登记重查；非必填终止周期。
			if domain != DomainBlockTrade {
				continue
			}
			marker := retryMarker(domain, dayKey)
			// 护盾必须跨全状态：retry 转终态（真空页以 0 行 success 是常态）后 active 查会失真，
			// viewless 热环重登记就是这里漏的。同域同日 marker 终生单条。
			exists, err := c.markerExists(ctx, domain, marker, today.AddDate(0, 0, -7))
			if err == nil && exists {
				continue
			}
			retry := BackfillJob{Domain: domain, Start: job.Start, End: job.End, Source: job.Source, RequestedBy: marker}
			if _, err := c.SubmitBackfill(ctx, retry); err != nil {
				continue
			}
			// 交易日挂在仍空的原始数据页上（与 sunk 登记 warn 同键合并、hits 递增）。
			c.saveIssues(ctx, domain, job.Source, job.Start, []Issue{{Check: CheckEmpty, Severity: "warn",
				Message: fmt.Sprintf("零行 sunk 完成后窗口行数仍≤0（%s %s），已登记重查任务（单条终止，此后转人工）", domain, dayKey)}})
		}
	}
}

// markerExists 查指定 RequestedBy 的任务是否已存在（全状态单条护盾）。
func (c *Collector) markerExists(ctx context.Context, domain, marker string, since time.Time) (bool, error) {
	jobs, err := c.repo.ListBackfills(ctx, domain, []string{JobPending, JobRunning, JobSuccess, JobFailed}, since)
	if err != nil {
		return false, err
	}
	for _, j := range jobs {
		if j.RequestedBy == marker {
			return true, nil
		}
	}
	return false, nil
}
