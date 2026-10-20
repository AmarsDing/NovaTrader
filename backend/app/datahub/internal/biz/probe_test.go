package biz

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/log"
)

/* W4 巡检编排钉死：fake ReportProber 源（失败/空行/成功）+ fake repo 捕获 saveIssues。 */

type fakeProberSrc struct {
	name    string
	results []ProbeResult
	calls   int
}

func (f *fakeProberSrc) Name() string { return f.name }
func (f *fakeProberSrc) ProbeReports(context.Context, time.Time) []ProbeResult {
	f.calls++
	return f.results
}

func probePipe(t *testing.T, srcs []Source, enabled map[string]bool, names ...string) *Pipeline {
	t.Helper()
	cfg := Config{
		AllowUnofficial: true, BreakerFailures: 3, BreakerCooldown: time.Minute,
		Sources: map[string]SourceConfig{},
		Domains: map[string]DomainConfig{DomainMargin: {Enabled: true, Sources: names}},
	}
	for _, n := range names {
		on, ok := enabled[n]
		if !ok {
			on = true
		}
		cfg.Sources[n] = SourceConfig{Enabled: on, Official: true, Timeout: time.Second}
	}
	p, _ := NewPipeline(cfg, srcs, nil)
	return p
}

func TestProbeReportsIssues(t *testing.T) {
	now := time.Date(2026, 10, 19, 8, 5, 0, 0, shanghai())
	bad := &fakeProberSrc{name: SourceEastmoney, results: []ProbeResult{
		{Domain: DomainMargin, Report: "RPTA_WEB_RZRQ_GGMX", Rows: 0, Err: fmt.Errorf("【报表轮换】eastmoney %s: %d %s", "RPTA_WEB_RZRQ_GGMX", 9501, "报表配置不存在")},
		{Domain: DomainHsgtTop10, Report: "RPT_MUTUAL_TOP10DEAL", Rows: 0, Err: fmt.Errorf("报表 RPT_MUTUAL_TOP10DEAL 预期非空而 0 行")},
	}}
	good := &fakeProberSrc{name: SourceTushare, results: []ProbeResult{
		{Domain: DomainMargin, Report: "RPT_OK", Rows: 1},
	}}
	off := &fakeProberSrc{name: SourceCninfo, results: []ProbeResult{
		{Domain: DomainMargin, Report: "RPT_OFF", Err: fmt.Errorf("should not run")},
	}}
	p := probePipe(t, []Source{bad, good, off}, map[string]bool{SourceCninfo: false}, SourceEastmoney, SourceTushare, SourceCninfo)
	repo := &snapshotIssueRepo{}
	c := NewCollector(p, repo, &snapshotMemoryStore{}, nil, nil, nil, Options{}, log.DefaultLogger)
	c.now = func() time.Time { return now }
	day, err := c.LastOpenDay(now)
	if err != nil {
		t.Fatalf("LastOpenDay: %v", err)
	}
	sum, err := c.ProbeReports(context.Background())
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if sum.Rows != 3 {
		t.Fatalf("checked=%d want 3（失败两张+成功一张，禁用源跳过）", sum.Rows)
	}
	if bad.calls != 1 || good.calls != 1 {
		t.Fatalf("calls bad=%d good=%d want 1/1", bad.calls, good.calls)
	}
	if off.calls != 0 {
		t.Fatal("禁用源不应被探测（关掉的源不该刷红）")
	}
	if len(repo.issues) != 2 {
		t.Fatalf("issues=%d want 2（成功源零 Issue）: %+v", len(repo.issues), repo.issues)
	}
	for _, is := range repo.issues {
		if is.Check != CheckProbe || is.Severity != "error" || is.Symbol != "" {
			t.Fatalf("域级 error Issue 形态不对: %+v", is)
		}
		if is.Source != SourceEastmoney || !is.TradeDate.Equal(day) {
			t.Fatalf("Issue 溯源/交易日不对: %+v", is)
		}
	}
	// 健康面板「说明」列红显：LastError 带报表名，且不动成败计数。
	for _, h := range p.Health() {
		if h.Domain == DomainMargin && h.Source == SourceEastmoney {
			if !strings.Contains(h.LastError, "RPTA_WEB_RZRQ_GGMX") {
				t.Fatalf("health.LastError 应含报表名: %q", h.LastError)
			}
			if h.Success != 0 || h.Failure != 0 {
				t.Fatalf("探测不得动成败计数: %+v", h)
			}
		}
	}
}
