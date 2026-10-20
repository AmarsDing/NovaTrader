package data

import (
	"context"
	"testing"

	"server/app/datahub/internal/biz"
	"server/pkg/params"
)

// 能力矩阵一致性钉死：pkg/params 的静态白名单（源名/域名/源→域能力）必须与
// biz 常量集及 biz.Supports 真值表一致。params 不能 import biz（biz→appcfg→params 成环），
// 三处（model.go / pkg/params / 本文件）靠本测试防漂移：新增源/域漏改任一处即红。

// conformanceDomains 与 biz/model.go 现存 24 个域常量纯等值比对（含本批落地的
// block_trade、guba——前向声明例外已随域常量落地删除）。
var conformanceDomains = []string{
	biz.DomainSecurity, biz.DomainDailyBar, biz.DomainAdjFactor, biz.DomainMinuteBar,
	biz.DomainSnapshot, biz.DomainLimitPool, biz.DomainMoneyFlow, biz.DomainLhb,
	biz.DomainUnlock, biz.DomainBlockTrade, biz.DomainMargin, biz.DomainMarginMkt,
	biz.DomainHsgtTop10, biz.DomainSector, biz.DomainSectorQuote, biz.DomainFinance,
	biz.DomainFlash, biz.DomainNews, biz.DomainAnnouncement, biz.DomainReport,
	biz.DomainGuba, biz.DomainOverseas, biz.DomainMacro, biz.DomainHotRank,
}

var conformanceSourceNames = []string{
	biz.SourceTdxLocal, biz.SourceTdxFile, biz.SourceTdxTCP, biz.SourceAkshare,
	biz.SourceTushare, biz.SourceEastmoney, biz.SourceSina, biz.SourceTencent,
	biz.SourceCninfo, biz.SourceTavily, biz.SourceRSS, biz.SourceTdxArchive,
	biz.SourceOpenBBFred, biz.SourceSina7x24, biz.SourceBaostock,
}

// conformanceSources 零值构造全部 15 个已注册源（构造器均为纯函数无 I/O；
// sina7x24/baostock 本批装配落地后并入能力矩阵比对）。
func conformanceSources(t *testing.T) []biz.Source {
	t.Helper()
	opt := HTTPOptions{}
	universe := func(context.Context) ([]string, error) { return nil, nil }
	archive, err := NewTdxArchiveSource("http://127.0.0.1:18081")
	if err != nil {
		t.Fatal(err)
	}
	fred, err := NewOpenBBFredSource("http://127.0.0.1:18082")
	if err != nil {
		t.Fatal(err)
	}
	return []biz.Source{
		NewTdxLocal("", opt),
		NewTdxFile("", opt),
		NewTdxTCP("", opt),
		NewAkshare("", opt),
		NewTushare("", "", opt),
		NewEastmoney(opt),
		NewSina(opt, universe),
		NewTencent(opt, universe),
		NewCninfo(opt),
		NewTavily("", nil, opt),
		NewRSS(nil, opt),
		archive,
		fred,
		NewSina7x24(nil, opt),
		NewBaostock("", universe, opt),
	}
}

func nameSet(names []string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

func TestParamsSourceAndDomainWhitelistsMatchBiz(t *testing.T) {
	wantSrc := nameSet(conformanceSourceNames)
	gotSrc := nameSet(params.DataSourceNames())
	if len(gotSrc) != len(wantSrc) {
		t.Fatalf("params source whitelist size %d, want %d", len(gotSrc), len(wantSrc))
	}
	for name := range wantSrc {
		if !gotSrc[name] {
			t.Errorf("biz source %s missing from params whitelist", name)
		}
	}
	for name := range gotSrc {
		if !wantSrc[name] {
			t.Errorf("params whitelist source %s not a biz constant", name)
		}
	}

	wantDom := nameSet(conformanceDomains)
	gotDom := nameSet(params.DataDomainNames())
	// 域白名单纯双向等值：多一个少一个漂移都红。
	for name := range gotDom {
		if !wantDom[name] {
			t.Errorf("params whitelist domain %s not a biz constant", name)
		}
	}
	for name := range wantDom {
		if !gotDom[name] {
			t.Errorf("biz domain %s missing from params whitelist", name)
		}
	}
}

func TestParamsCapabilityMatrixMatchesSupports(t *testing.T) {
	for _, src := range conformanceSources(t) {
		for _, domain := range conformanceDomains {
			got := params.SupportsDataSource(domain, src.Name())
			want := biz.Supports(domain, src)
			if got != want {
				t.Errorf("domain=%s source=%s: params.SupportsDataSource=%v, biz.Supports=%v", domain, src.Name(), got, want)
			}
		}
	}
}
