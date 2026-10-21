package biz

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// defaultSelfHealDays 是对账自修复的默认连击上限：连续 N 个「带差异日」自动登记单日回补，
// 第 N+1 个差异日升级为 error 停环（防回补风暴），出现干净日自动复位。
const defaultSelfHealDays = 3

// selfhealWindow 记录某数据域对账自修复的窗口状态（内存近似：重启清零，
// 持久化计数表等 ent 冻结解除后随 D2 落账，接口已经预留）。
type selfhealWindow struct {
	hits    int    // 连续带差异日数；同一天重复对账不重复计数
	lastDay string // 最近一次计入的交易日 YYYYMMDD
	stopped bool   // 已达上限停环：干净日自动复位前不再登记
}

// selfhealState 挂在 Collector 上的自修复窗口表（key=数据域）。
type selfhealState struct {
	mu sync.Mutex
	m  map[string]*selfhealWindow
}

// selfHealReconcile 是 D2 对账自修复钩：reconcile 产出逐标的差异 warn 后，
// 为当日登记一个 scoped BackfillJob（域/标的/单日/对照源自动），连 N 日仍有差异升级 error 停环。
// 纪律：
//   - 滤不可回补源：域不在 BackfillDomains 白名单内一律不登记；
//   - 只回补「值不一致」的标的（Detail 非空的差异），对照源缺根的那根不回补——那是源缺数不是入库错；
//   - 不改对账本身的 warn 语义与返回；登记失败只记日志；
//   - pending 任务 best-effort 按符号集去重，防同一天重复对账/重复登记放大回补量。
func (c *Collector) selfHealReconcile(ctx context.Context, domain string, day time.Time, issues []Issue, selfHealDays int) {
	if selfHealDays == 0 {
		selfHealDays = defaultSelfHealDays
	}
	if selfHealDays < 0 || !BackfillDomains[domain] {
		return
	}
	seen := map[string]bool{}
	symbols := make([]string, 0, len(issues))
	for _, is := range issues {
		if is.Check != CheckReconcile || is.Severity != "warn" || is.Symbol == "" || len(is.Detail) == 0 || seen[is.Symbol] {
			continue
		}
		seen[is.Symbol] = true
		symbols = append(symbols, is.Symbol)
	}
	sort.Strings(symbols)
	dayKey := day.Format("20060102")

	c.selfheal.mu.Lock()
	if c.selfheal.m == nil {
		c.selfheal.m = map[string]*selfhealWindow{}
	}
	w := c.selfheal.m[domain]
	if w == nil {
		w = &selfhealWindow{}
		c.selfheal.m[domain] = w
	}
	if len(symbols) == 0 {
		// 干净日：窗口复位（含停环解除），允许下次升级到上限再次告警。
		w.hits, w.lastDay, w.stopped = 0, "", false
		c.selfheal.mu.Unlock()
		c.staleOnce.Delete("selfheal:" + domain)
		return
	}
	newDay := w.lastDay != dayKey
	if newDay {
		w.hits++
		w.lastDay = dayKey
	}
	stopped := w.stopped
	if !stopped && w.hits > selfHealDays {
		w.stopped = true
		stopped = true
		hits := w.hits
		c.selfheal.mu.Unlock()
		msg := fmt.Sprintf("对账连续 %d 日（上限 %d 日）仍有差异，自修复回补停环，请人工核对数据源", hits, selfHealDays)
		c.saveIssues(ctx, domain, "", day, []Issue{{Domain: domain, Check: CheckReconcile, Severity: "error",
			TradeDate: day, Message: msg}})
		c.alertOnce(ctx, "selfheal:"+domain, "error", "对账自修复停环："+domain, msg)
		return
	}
	c.selfheal.mu.Unlock()
	if stopped {
		return
	}
	c.registerSelfHealBackfill(ctx, domain, day, dayKey, symbols)
}

// registerSelfHealBackfill pending 去重后登记单日 scoped 回补。Source 留空：
// 对账拿不到来源全链，交给 RunBackfill 按域基线链优先级取源（对照源优先新建链规化）。
func (c *Collector) registerSelfHealBackfill(ctx context.Context, domain string, day time.Time, dayKey string, symbols []string) {
	wanted := map[string]bool{}
	for _, s := range symbols {
		wanted[s] = true
	}
	if pending, err := c.repo.PendingBackfills(ctx); err != nil {
		c.log.Warnf("selfheal pending %s: %v", domain, err)
	} else {
		for _, job := range pending {
			if job.Domain != domain || !job.Start.Equal(day) || !job.End.Equal(day) {
				continue
			}
			for _, s := range job.Symbols {
				delete(wanted, s)
			}
		}
	}
	if len(wanted) == 0 {
		return
	}
	missing := make([]string, 0, len(wanted))
	for s := range wanted {
		missing = append(missing, s)
	}
	sort.Strings(missing)
	job := BackfillJob{Domain: domain, Symbols: missing, Start: day, End: day, Source: "", RequestedBy: "selfheal:reconcile"}
	if _, err := c.SubmitBackfill(ctx, job); err != nil {
		c.log.Warnf("selfheal backfill %s %s %d 标的: %v", domain, dayKey, len(missing), err)
		return
	}
	c.log.Infof("selfheal backfill %s %s 登记 %d 标的", domain, dayKey, len(missing))
}
