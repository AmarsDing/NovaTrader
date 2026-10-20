package biz

import (
	"sort"
	"time"

	"server/conf"
)

// 源目录（Source Catalog）：「每个源能提供什么数据」的单一事实源。
// 形状对齐 doc/开发文档/M01-数据采集/数据源对接与配置方案.md §4.2。
// 合成规则：运行态能力（registered、实际域集合）以 Pipeline 的 Supports() 反射为准；
// 费用/凭证/接入/风险标注挂本文件的静态表 sourceBook（事实出处逐行注 §1.2/§2）。
// 通过 datahub 自有 HTTP（/v1/datahub/source-catalog）直出 JSON——管理面只读元数据，
// 与 server.go 的 healthz/metrics 直写先例同手法：数据归属方在 datahub（装配于 data/sources.go），
// 不过 proto/gateway 新路由。
const (
	// CatalogVersion 是目录契约版本；形状演进时抬。
	CatalogVersion = 1
	// CatalogServicesNote 标注读面与写面的接口分工（透传给前端提示用）。
	CatalogServicesNote = "静态自描述+反射；健康与开关走 /api/datahub/sources，链与凭证写面走 /api/config"
)

// 费用档枚举（前端配色口径：free=成功色、freemium=warn 色标价、paid=灰标；空串显「—」不造 0）。
const (
	FeeFree     = "free"
	FeeFreemium = "freemium"
	FeePaid     = "paid"
)

// 凭证需求枚举：源要开跑用户还必须补什么。
// none=开箱即用、sidecar=需本地边车进程、token=需 token、key=需 API key。
const (
	RequiresNone    = "none"
	RequiresToken   = "token"
	RequiresKey     = "key"
	RequiresSidecar = "sidecar"
)

// 接入位置枚举：这个源在哪里跑。
const (
	ViaGoDirect     = "go_direct"
	ViaPyworker     = "pyworker"
	ViaSourceWorker = "source_worker"
)

// sourceBookRow 是源目录的静态事实行。Domains 是声明的覆盖域：
// 未注册（已登记键位、装配中/未启用）的源靠它上源墙可配；已注册源以 Supports() 反射为准，
// 反射集必须 ⊇ 这里声明的域（catalog_test.go 与 data 层的反射一致性集成测试钉死）。
type sourceBookRow struct {
	Name     string
	Domains  []string
	FeeTier  string
	FeeNote  string
	Official bool
	Requires string
	Via      string
	Notes    string
	RiskNote string
}

// catalogDomains 是反射遍历的域全集，顺序即域视图输出顺序。
// 同步纪律：与 model.go 的 Domain* 常量一一对应，新增域常量同步追加；
// pkg/params/validate.go 的 dsDomainNames 是这张表的跨包镜像（漂移由一致性测试守护）。
var catalogDomains = []string{
	DomainSecurity, DomainDailyBar, DomainAdjFactor, DomainMinuteBar, DomainSnapshot,
	DomainLimitPool, DomainMoneyFlow, DomainLhb, DomainUnlock, DomainBlockTrade, DomainMargin, DomainMarginMkt,
	DomainHsgtTop10, DomainSector, DomainSectorQuote, DomainFinance,
	DomainFlash, DomainNews, DomainAnnouncement, DomainReport, DomainGuba,
	DomainOverseas, DomainMacro, DomainHotRank,
}

// sourceBook 覆盖全部 15 个 Source* 常量（13 个常驻装配 + tdx_http_archive/openbb_fred
// 条件装配）。顺序即源墙输出顺序（= model.go 常量声明序）。
// 同步纪律：新增源常量必须同处登记本表（catalog_test.go 按常量数钉行数）。
var sourceBook = []sourceBookRow{
	{
		Name:     SourceTdxLocal,
		Domains:  []string{DomainSecurity, DomainDailyBar, DomainMinuteBar, DomainSnapshot},
		FeeTier:  FeeFree,
		Official: true, Requires: RequiresSidecar, Via: ViaPyworker,
		Notes: "通达信官方本地插件，决策 3 行情主线（§1.2）",
	},
	{
		Name:     SourceTdxFile,
		Domains:  []string{DomainDailyBar, DomainMinuteBar, DomainSector},
		FeeTier:  FeeFree,
		Official: true, Requires: RequiresSidecar, Via: ViaPyworker,
		Notes:    "通达信 vipdoc 盘后文件（§1.2）",
		RiskNote: "盘后数据，盘中不更新",
	},
	{
		Name:     SourceTdxTCP,
		Domains:  []string{DomainAdjFactor, DomainDailyBar, DomainMinuteBar, DomainSecurity, DomainSnapshot},
		FeeTier:  FeeFree,
		Official: false, Requires: RequiresSidecar, Via: ViaPyworker,
		Notes:    "通达信 mootdx 协议 7709（§1.2）",
		RiskNote: "逆向协议、库停更（mootdx 续命中），IP 池需自维护，商用灰色（§2.1/§7）",
	},
	{
		Name:     SourceAkshare,
		Domains:  []string{DomainLimitPool, DomainMacro, DomainMinuteBar, DomainOverseas},
		FeeTier:  FeeFree,
		Official: true, Requires: RequiresSidecar, Via: ViaPyworker,
		Notes:    "聚合库：外围指数/宏观/涨停池/分钟线回补（§1.2）",
		RiskNote: "网页系无 SLA；升级需 pin 版本回归（§6.5）",
	},
	{
		Name:    SourceTushare,
		Domains: []string{DomainAdjFactor, DomainDailyBar, DomainFinance, DomainHsgtTop10, DomainLhb, DomainMargin, DomainMarginMkt, DomainMoneyFlow, DomainSecurity},
		FeeTier: FeeFreemium, FeeNote: "免费档起步；核心≈2000 积分（约 200 元/年）",
		Official: true, Requires: RequiresToken, Via: ViaGoDirect,
		Notes:    "覆盖面最广的合规付费入口（§1.2）",
		RiskNote: "积分档决定域可用性（§2.5）",
	},
	{
		Name: SourceEastmoney,
		Domains: []string{DomainAnnouncement, DomainBlockTrade, DomainDailyBar, DomainFinance, DomainFlash, DomainGuba,
			DomainHotRank, DomainHsgtTop10, DomainLhb, DomainLimitPool, DomainMargin, DomainMoneyFlow,
			DomainNews, DomainReport, DomainSector, DomainSectorQuote, DomainSecurity, DomainSnapshot, DomainUnlock},
		FeeTier:  FeeFree,
		Official: false, Requires: RequiresNone, Via: ViaGoDirect,
		Notes:    "免费源中覆盖最广：涨跌停/炸板/竞价情绪池为免费界唯一系统级（§1.2/§2.1）",
		RiskNote: "高频短期封 IP；datacenter 报表名会轮换（两融旧表已 404，§2.3）",
	},
	{
		Name:     SourceSina,
		Domains:  []string{DomainSnapshot},
		FeeTier:  FeeFree,
		Official: false, Requires: RequiresNone, Via: ViaGoDirect,
		Notes:    "快照备用源（§1.2）",
		RiskNote: "必须带 Referer，高频 403（§2.1）",
	},
	{
		Name:     SourceTencent,
		Domains:  []string{DomainSnapshot},
		FeeTier:  FeeFree,
		Official: false, Requires: RequiresNone, Via: ViaGoDirect,
		Notes:    "四大门户风控最松（§2.1）",
		RiskNote: "K 线/资金流能力可扩展未启用（§2.1）",
	},
	{
		Name:     SourceCninfo,
		Domains:  []string{DomainAnnouncement},
		FeeTier:  FeeFree,
		Official: true, Requires: RequiresNone, Via: ViaGoDirect,
		Notes: "官方披露平台，沪深北公告一站（§2.2）",
	},
	{
		Name:    SourceTavily,
		Domains: []string{DomainAnnouncement, DomainFlash, DomainNews, DomainReport},
		FeeTier: FeeFreemium, FeeNote: "免费 1000 次/月 + 付费档",
		Official: true, Requires: RequiresToken, Via: ViaGoDirect,
		Notes: "AI 主题式搜索，境内直连正常（§2.2）；凭证键白名单为 app.ds.tavily.token",
	},
	{
		Name:     SourceRSS,
		Domains:  []string{DomainAnnouncement, DomainFlash, DomainNews, DomainReport},
		FeeTier:  FeeFree,
		Official: true, Requires: RequiresNone, Via: ViaGoDirect,
		Notes:    "通用 feed 补充（§1.2）",
		RiskNote: "生态萎缩（cls/见闻官方 RSS 已死），降为补充（§1.2）",
	},
	{
		Name:     SourceTdxArchive,
		Domains:  []string{DomainDailyBar},
		FeeTier:  FeeFree,
		Official: true, Requires: RequiresSidecar, Via: ViaSourceWorker,
		Notes:    "通达信官网盘后包历史回补（§1.2）",
		RiskNote: "已建未启用：N4-02 收尾后配置页启用（§3.1）",
	},
	{
		Name:    SourceOpenBBFred,
		Domains: []string{DomainMacro},
		FeeTier: FeeFree, FeeNote: "免费（需 FRED_API_KEY）",
		Official: true, Requires: RequiresKey, Via: ViaSourceWorker,
		Notes:    "美国宏观 CPI/利率/失业三序列白名单（§2.4）",
		RiskNote: "已建未启用：配 FRED_API_KEY 即启用（§3.1）",
	},
	{
		Name:    SourceSina7x24,
		Domains: []string{DomainFlash},
		FeeTier: FeeFree, FeeNote: "免费（需 Referer）",
		Official: false, Requires: RequiresNone, Via: ViaGoDirect,
		Notes:    "新浪 7×24 快讯副线，按 docid 去重（§2.2/§3.1）",
		RiskNote: "P0 快讯副线：实现仅接 flash（intel 其余域仅接口能力反射）",
	},
	{
		Name:    SourceBaostock,
		Domains: []string{DomainDailyBar, DomainFinance},
		FeeTier: FeeFree, FeeNote: "免费免注册",
		Official: true, Requires: RequiresSidecar, Via: ViaPyworker,
		Notes:    "盘后回补/财务三表，历史至 1990s（§2.1/§3.1）",
		RiskNote: "本批装配（pyworker 装好 baostock 前保持 enabled:false）；晚间批量出数盘中滞后（§2.1）",
	},
}

// SourceManifest 是源墙上一格：单一源的自描述 + 运行态注册/能力反射。
// 缺省字段留空串（前端显「—」），禁止用默认值冒充（没取到≠0）。
type SourceManifest struct {
	Name       string   `json:"name"`
	Registered bool     `json:"registered"`
	Domains    []string `json:"domains"`
	FeeTier    string   `json:"fee_tier"`
	FeeNote    string   `json:"fee_note"`
	Official   bool     `json:"official"`
	Requires   string   `json:"requires"`
	Via        string   `json:"via"`
	Notes      string   `json:"notes"`
	RiskNote   string   `json:"risk_note"`
}

// DomainInfo 是域视图上一行：yaml 默认降级链 + 全部可挂该域的候选源。
// Candidates 排序 = default_chain 顺序优先，其余按 sourceBook 表序；
// 集合 = 已注册源 Supports() 反射全集 ∪ 未注册源声明域（候选永远真支持该域，
// 与 NewPipeline 的 skip-warning 场景互补——目录暴露的候选不会踩「不支持」）。
type DomainInfo struct {
	Name         string   `json:"name"`
	DefaultChain []string `json:"default_chain"`
	Candidates   []string `json:"candidates"`
}

// Catalog 是 GET /v1/datahub/source-catalog 的响应体。
type Catalog struct {
	Version      int              `json:"version"`
	GeneratedAt  string           `json:"generated_at"`
	ServicesNote string           `json:"services_note"`
	Sources      []SourceManifest `json:"sources"`
	Domains      []DomainInfo     `json:"domains"`
}

// supports 返回注册源按 Supports() 反射的域集合（catalogDomains 序；空能力给空切片不给 nil）。
func supportedDomains(s Source) []string {
	domains := make([]string, 0, len(catalogDomains))
	for _, d := range catalogDomains {
		if Supports(d, s) {
			domains = append(domains, d)
		}
	}
	return domains
}

// declaredSupports 返未注册源声明覆盖某域与否。
func declaredSupports(row sourceBookRow, domain string) bool {
	for _, d := range row.Domains {
		if d == domain {
			return true
		}
	}
	return false
}

// BuildCatalog 合成目录：p 的注册源反射为准，静态表补费用/风险标注；
// cfg 只读 domains 默认链（yaml 即配置中心的 yaml 默认，运行态覆盖由 /api/config 快照兜出，目录不管）。
func BuildCatalog(p *Pipeline, cfg *conf.Datahub) Catalog {
	registered := map[string]*sourceState{}
	if p != nil {
		for name, st := range p.sources {
			registered[name] = st
		}
	}

	sources := make([]SourceManifest, 0, len(sourceBook)+4)
	inBook := map[string]bool{}
	for _, row := range sourceBook {
		m := SourceManifest{
			Name: row.Name, FeeTier: row.FeeTier, FeeNote: row.FeeNote,
			Official: row.Official, Requires: row.Requires, Via: row.Via,
			Notes: row.Notes, RiskNote: row.RiskNote,
			Domains: append([]string(nil), row.Domains...),
		}
		if st, ok := registered[row.Name]; ok {
			m.Registered = true
			m.Domains = supportedDomains(st.src)
		}
		sources = append(sources, m)
		inBook[row.Name] = true
	}
	// 表外注册源兜底（静态表漂移时源墙不静默失踪；费用字段留空显「—」）。
	extra := make([]string, 0)
	for name := range registered {
		if !inBook[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(extra)
	for _, name := range extra {
		st := registered[name]
		sources = append(sources, SourceManifest{
			Name: name, Registered: true, Domains: supportedDomains(st.src),
			Official: st.cfg.Official,
		})
	}

	confDomains := map[string]*conf.DataDomain{}
	if cfg != nil {
		confDomains = cfg.GetDomains()
	}
	// 域序 = catalogDomains 里出现过的 cfg 域在前，cfg 里额外的域排序追后。
	ordered := make([]string, 0, len(confDomains))
	seen := map[string]bool{}
	for _, d := range catalogDomains {
		if _, ok := confDomains[d]; ok {
			ordered = append(ordered, d)
			seen[d] = true
		}
	}
	rest := make([]string, 0)
	for name := range confDomains {
		if !seen[name] {
			rest = append(rest, name)
		}
	}
	sort.Strings(rest)
	ordered = append(ordered, rest...)

	domains := make([]DomainInfo, 0, len(ordered))
	for _, name := range ordered {
		def := append([]string(nil), confDomains[name].GetSources()...)
		cands := make([]string, 0, len(def)+4)
		added := map[string]bool{}
		for _, s := range def {
			if !added[s] {
				added[s] = true
				cands = append(cands, s)
			}
		}
		for _, row := range sourceBook {
			if added[row.Name] {
				continue
			}
			if st, ok := registered[row.Name]; ok {
				if Supports(name, st.src) {
					added[row.Name] = true
					cands = append(cands, row.Name)
				}
				continue
			}
			if declaredSupports(row, name) {
				added[row.Name] = true
				cands = append(cands, row.Name)
			}
		}
		// 表外注册源同样进候选（排序最后，确定性）。
		for _, e := range extra {
			if added[e] {
				continue
			}
			if Supports(name, registered[e].src) {
				added[e] = true
				cands = append(cands, e)
			}
		}
		domains = append(domains, DomainInfo{Name: name, DefaultChain: def, Candidates: cands})
	}

	return Catalog{
		Version:      CatalogVersion,
		GeneratedAt:  time.Now().Format(time.RFC3339),
		ServicesNote: CatalogServicesNote,
		Sources:      sources,
		Domains:      domains,
	}
}
