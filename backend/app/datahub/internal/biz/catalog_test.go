package biz

import (
	"context"
	"testing"

	"server/conf"
)

// 15 个 Source* 常量（model.go 单一事实源；catalog 静态表必须逐行覆盖）。
var allSourceConstants = []string{
	SourceTdxLocal, SourceTdxFile, SourceTdxTCP, SourceAkshare, SourceTushare,
	SourceEastmoney, SourceSina, SourceTencent, SourceCninfo, SourceTavily, SourceRSS,
	SourceTdxArchive, SourceOpenBBFred, SourceSina7x24, SourceBaostock,
}

// 24 个 Domain* 常量（model.go 单一事实源；catalogDomains 全集必须与其一一对应）。
var allDomainConstants = []string{
	DomainSecurity, DomainDailyBar, DomainAdjFactor, DomainMinuteBar, DomainSnapshot,
	DomainLimitPool, DomainMoneyFlow, DomainLhb, DomainUnlock, DomainBlockTrade, DomainMargin, DomainMarginMkt,
	DomainHsgtTop10, DomainSector, DomainSectorQuote, DomainFinance,
	DomainFlash, DomainNews, DomainAnnouncement, DomainReport, DomainGuba,
	DomainOverseas, DomainMacro, DomainHotRank,
}

func bookRow(t *testing.T, name string) sourceBookRow {
	t.Helper()
	for _, row := range sourceBook {
		if row.Name == name {
			return row
		}
	}
	t.Fatalf("sourceBook 缺行：%s", name)
	return sourceBookRow{}
}

// ①manifest 完整性：静态表行数恰好覆盖全部 Source* 常量（13 既有 + 同批新增 sina7x24/baostock），
// 多一行少一行都红——新增源常量必须同行登记本表。
func TestCatalogBookCoversAllSourceConstants(t *testing.T) {
	if len(sourceBook) != len(allSourceConstants) {
		t.Fatalf("sourceBook 行数 %d ≠ 常量数 %d", len(sourceBook), len(allSourceConstants))
	}
	seen := map[string]bool{}
	for _, row := range sourceBook {
		if seen[row.Name] {
			t.Fatalf("sourceBook 重复行：%s", row.Name)
		}
		seen[row.Name] = true
	}
	for _, name := range allSourceConstants {
		bookRow(t, name)
	}
}

// ①b 域全集钉死（评审 major 收口）：catalogDomains 与 model.go 的 Domain* 常量一一对应，
// 漏增域常量同步（如 block_trade/guba 类新域）此处即红。
func TestCatalogDomainsCoverAllDomainConstants(t *testing.T) {
	if len(catalogDomains) != len(allDomainConstants) {
		t.Fatalf("catalogDomains 域数 %d ≠ 常量数 %d", len(catalogDomains), len(allDomainConstants))
	}
	want := map[string]bool{}
	for _, d := range allDomainConstants {
		want[d] = true
	}
	for _, d := range catalogDomains {
		if !want[d] {
			t.Fatalf("catalogDomains 含非 Domain* 常量域：%s", d)
		}
		delete(want, d)
	}
	for _, d := range allDomainConstants {
		if want[d] {
			t.Fatalf("catalogDomains 漏 Domain* 常量：%s", d)
		}
	}
}

// ③manifest 完整性：每行费用档/凭证/接入枚举合法，声明域全部落进 Domain* 常量集
// （含未注册表行 declared 域与 Domain* 常量一致——enums 之外再无隐形域）。
func TestCatalogBookEnumsAndDeclaredDomains(t *testing.T) {
	domainSet := map[string]bool{}
	for _, d := range catalogDomains {
		domainSet[d] = true
	}
	if len(domainSet) != len(catalogDomains) {
		t.Fatalf("catalogDomains 有重复域，行数 %d", len(catalogDomains))
	}
	for _, row := range sourceBook {
		switch row.FeeTier {
		case FeeFree, FeeFreemium, FeePaid:
		default:
			t.Fatalf("%s: fee_tier 非法 %q", row.Name, row.FeeTier)
		}
		switch row.Requires {
		case RequiresNone, RequiresToken, RequiresKey, RequiresSidecar:
		default:
			t.Fatalf("%s: requires 非法 %q", row.Name, row.Requires)
		}
		switch row.Via {
		case ViaGoDirect, ViaPyworker, ViaSourceWorker:
		default:
			t.Fatalf("%s: via 非法 %q", row.Name, row.Via)
		}
		if len(row.Domains) == 0 {
			t.Fatalf("%s: declared 域为空（未注册源靠它上源墙）", row.Name)
		}
		for _, d := range row.Domains {
			if !domainSet[d] {
				t.Fatalf("%s: declared 域 %q 不在 Domain* 常量集", row.Name, d)
			}
		}
	}
}

// ②关键事实抽样（对《数据源对接与配置方案.md》§1.2/§2 的事实钉死，防静态表悄悄漂移）。
func TestCatalogBookKeyFacts(t *testing.T) {
	if row := bookRow(t, SourceTushare); row.FeeTier != FeeFreemium || row.Requires != RequiresToken || !row.Official {
		t.Fatalf("tushare 事实漂移：%+v", row)
	}
	// tavily 的 requires 必须钉 token：pkg/params 白名单键是 app.ds.tavily.token
	// （标 key 会让写面拼出 app.ds.tavily.key 被服务端拒「未登记的参数键」）。
	if row := bookRow(t, SourceTavily); row.FeeTier != FeeFreemium || row.Requires != RequiresToken {
		t.Fatalf("tavily 事实漂移：%+v", row)
	}
	// openbb_fred 反向钉 key：白名单键是 app.ds.openbb_fred.key（断言见下方）。
	if row := bookRow(t, SourceCninfo); row.FeeTier != FeeFree || !row.Official || row.Requires != RequiresNone {
		t.Fatalf("cninfo 事实漂移：%+v", row)
	}
	if row := bookRow(t, SourceTdxTCP); row.Official || row.Requires != RequiresSidecar {
		t.Fatalf("tdx_tcp 事实漂移：%+v", row)
	}
	if row := bookRow(t, SourceOpenBBFred); row.Requires != RequiresKey || row.Via != ViaSourceWorker || !row.Official {
		t.Fatalf("openbb_fred 事实漂移：%+v", row)
	}
	if row := bookRow(t, SourceTdxArchive); row.Requires != RequiresSidecar || row.Via != ViaSourceWorker {
		t.Fatalf("tdx_http_archive 事实漂移：%+v", row)
	}
	if row := bookRow(t, SourceSina7x24); row.Requires != RequiresNone || row.Via != ViaGoDirect {
		t.Fatalf("sina7x24 事实漂移：%+v", row)
	}
	if row := bookRow(t, SourceBaostock); row.Requires != RequiresSidecar || row.Via != ViaPyworker {
		t.Fatalf("baostock 事实漂移：%+v", row)
	}
	// 装配落地收尾钉：declared 域必须 ⊆ 接口反射集（源墙/候选生成靠的口径）——
	// sina7x24 声明仅 flash，baostock 恰 daily_bar+finance；漂移（多报/漏报）此处即红。
	if row := bookRow(t, SourceSina7x24); len(row.Domains) != 1 || row.Domains[0] != DomainFlash {
		t.Fatalf("sina7x24 declared 域漂移：%+v", row.Domains)
	}
	if row := bookRow(t, SourceBaostock); len(row.Domains) != 2 || row.Domains[0] != DomainDailyBar || row.Domains[1] != DomainFinance {
		t.Fatalf("baostock declared 域漂移：%+v", row.Domains)
	}
}

// 反射用桩源：同包构造 tushare 名实现 security+daily_bar 两域。
type catalogStubSource struct{ name string }

func (s catalogStubSource) Name() string { return s.name }

type catalogSecurityStub struct{ catalogStubSource }

func (catalogSecurityStub) Securities(context.Context) ([]Security, error) { return nil, nil }

type catalogDailyBarStub struct{ catalogSecurityStub }

func (catalogDailyBarStub) DailyBars(context.Context, BarQuery) (BarBatch, error) {
	return BarBatch{}, nil
}

func newCatalogStub(name string) Source {
	return catalogDailyBarStub{catalogSecurityStub{catalogStubSource{name: name}}}
}

// ④域×Supports 一致性：BuildCatalog 反射实测——注册源域集合与 Supports 全真一致、
// 未注册行 declared 透传且 registered:false、candidates 排序 = 默认链优先 + 表序。
func TestBuildCatalogReflectionAndMerging(t *testing.T) {
	tushare := newCatalogStub(SourceTushare)
	p, _ := NewPipeline(Config{
		AllowUnofficial: true,
		Sources:         map[string]SourceConfig{SourceTushare: {Enabled: true, Official: true}},
		Domains:         map[string]DomainConfig{},
	}, []Source{tushare}, nil)
	cfg := &conf.Datahub{}
	cfg.Domains = map[string]*conf.DataDomain{
		DomainDailyBar: {Sources: []string{SourceTushare, SourceTdxFile}},
		DomainMacro:    {Sources: []string{SourceAkshare}},
	}
	cat := BuildCatalog(p, cfg)
	if cat.Version != CatalogVersion || cat.GeneratedAt == "" || cat.ServicesNote == "" {
		t.Fatalf("目录头缺失：%+v", cat)
	}
	if len(cat.Sources) != len(sourceBook) {
		t.Fatalf("manifest 行数 %d ≠ 表行数 %d", len(cat.Sources), len(sourceBook))
	}
	var ts, fred, bao *SourceManifest
	for i := range cat.Sources {
		switch cat.Sources[i].Name {
		case SourceTushare:
			ts = &cat.Sources[i]
		case SourceOpenBBFred:
			fred = &cat.Sources[i]
		case SourceBaostock:
			bao = &cat.Sources[i]
		}
	}
	if ts == nil || !ts.Registered {
		t.Fatalf("tushare 应注册：%+v", ts)
	}
	wantTS := []string{DomainSecurity, DomainDailyBar}
	if len(ts.Domains) != len(wantTS) || ts.Domains[0] != wantTS[0] || ts.Domains[1] != wantTS[1] {
		t.Fatalf("注册源反射域 %v ≠ %v", ts.Domains, wantTS)
	}
	if fred == nil || fred.Registered {
		t.Fatalf("openbb_fred 应未注册：%+v", fred)
	}
	// 未注册行域列表取表声明、registered:false（FRED 在源墙可见可配而不消失）。
	if len(fred.Domains) != 1 || fred.Domains[0] != DomainMacro {
		t.Fatalf("openbb_fred declared 透传错位：%v", fred.Domains)
	}
	if bao == nil || bao.Registered {
		t.Fatalf("baostock 应未注册：%+v", bao)
	}
	var daily, macro *DomainInfo
	for i := range cat.Domains {
		switch cat.Domains[i].Name {
		case DomainDailyBar:
			daily = &cat.Domains[i]
		case DomainMacro:
			macro = &cat.Domains[i]
		}
	}
	if daily == nil || macro == nil {
		t.Fatalf("cfg 域缺失：%+v", cat.Domains)
	}
	if len(daily.DefaultChain) != 2 || daily.DefaultChain[0] != SourceTushare || daily.DefaultChain[1] != SourceTdxFile {
		t.Fatalf("default_chain 漂移：%v", daily.DefaultChain)
	}
	// daily_bar 候选 = 链序优先（tushare/tdx_file），其余按表序：
	// tdx_local → tdx_tcp → eastmoney → tdx_http_archive → baostock（均 declared）。
	want := []string{SourceTushare, SourceTdxFile, SourceTdxLocal, SourceTdxTCP, SourceEastmoney, SourceTdxArchive, SourceBaostock}
	if len(daily.Candidates) != len(want) {
		t.Fatalf("candidates 数 %d ≠ %d：%v", len(daily.Candidates), len(want), daily.Candidates)
	}
	for i, name := range want {
		if daily.Candidates[i] != name {
			t.Fatalf("candidates[%d] %s ≠ %s：%v", i, daily.Candidates[i], name, daily.Candidates)
		}
	}
	if len(macro.DefaultChain) != 1 || macro.DefaultChain[0] != SourceAkshare {
		t.Fatalf("macro 默认链漂移：%v", macro.DefaultChain)
	}
	// macro 候选：链内 akshare + 表声明 openbb_fred；tushare 桩不实现 MacroSource 不得混入。
	if len(macro.Candidates) != 2 || macro.Candidates[0] != SourceAkshare || macro.Candidates[1] != SourceOpenBBFred {
		t.Fatalf("macro 候选漂移：%v", macro.Candidates)
	}
}

// 无 pipeline（服务还没建流水线）也不炸：全部按 declared 透传。
func TestBuildCatalogNilPipeline(t *testing.T) {
	cat := BuildCatalog(nil, &conf.Datahub{})
	if len(cat.Sources) != len(sourceBook) {
		t.Fatalf("nil pipeline 时行数 %d", len(cat.Sources))
	}
	for _, m := range cat.Sources {
		if m.Registered {
			t.Fatalf("nil pipeline 不应有注册源：%s", m.Name)
		}
		if len(m.Domains) == 0 {
			t.Fatalf("nil pipeline 时 %s declared 为空", m.Name)
		}
	}
}
