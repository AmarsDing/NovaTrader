package biz

import (
	"context"
	"fmt"
	"time"
)

// CheckProbe 报表轮换烟雾探测的检查名（data_quality_issue.check_name，纯字符串无枚举）。
// 新检查名放本文件：validate.go 的常量块归既有文件，避免并行改动同一区域。
const CheckProbe = "probe"

// ProbeResult 是一张数据报表的一次探测结果。Err 非空即失败（含报表轮换错误与
// 「预期非空而 0 行」两种）；Rows 是首页返回行数。
type ProbeResult struct {
	Domain string
	Report string
	Rows   int
	Err    error
}

// ReportProber 是可选的源能力：探测自己在用的网页系报表是否仍有效。
// 源按需实现，Collector.ProbeReports 用类型断言发现（同 Supports 的惯例）。
type ReportProber interface {
	ProbeReports(ctx context.Context, day time.Time) []ProbeResult
}

// ProbeReports 每日报表烟雾探测：逐源（跳过禁用与非官方关停，与 Run 门禁同语义，
// 关掉的源不该刷红）调用 ReportProber，每张失败报表产一条域级 error Issue 并写
// source_health 的 LastError（不动成败计数与断路器），error 级另走当日一次告警。
// 语义与 stale 检查同构：失败每交易日新产一行 data_quality_issue，修复后不会自动
// 翻绿，面板按 last_seen/hits 读。
func (c *Collector) ProbeReports(ctx context.Context) (CollectSummary, error) {
	day, err := c.LastOpenDay(c.now())
	if err != nil {
		return CollectSummary{Domain: "probe"}, err
	}
	checked := 0
	// 逐源走 EachSource（名称排序的稳定快照，构建后不再写、enabled 是 atomic）；
	// cfg 原样带出与 Run 门禁同语义：跳过禁用与非官方关停（关掉的源不该刷红）。
	c.p.EachSource(func(name string, src Source, enabled bool, cfg SourceConfig) {
		if !enabled {
			return
		}
		if !cfg.Official && !c.p.allowUnofficial.Load() {
			return
		}
		prober, ok := src.(ReportProber)
		if !ok {
			return
		}
		for _, r := range prober.ProbeReports(ctx, day) {
			checked++
			if r.Err == nil {
				continue
			}
			msg := fmt.Sprintf("报表 %s 探测失败：%v", r.Report, r.Err)
			c.saveIssues(ctx, r.Domain, name, day, []Issue{{
				Domain: r.Domain, Check: CheckProbe, Severity: "error", Message: msg,
			}})
			// 健康面板「说明」列红显（行首【报表轮换】/【疑似限流/封禁】标签可辨），不计失败、不触断路。
			c.p.health.note(r.Domain, name, r.Err.Error())
			c.alertOnce(ctx, "probe:"+r.Domain+":"+r.Report+":"+day.Format("20060102"),
				"error", "数据报表探测失败："+r.Domain, msg)
		}
	})
	return CollectSummary{Domain: "probe", Rows: checked}, nil
}
