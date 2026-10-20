package data

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"google.golang.org/protobuf/types/known/durationpb"

	"server/app/datahub/internal/biz"
	"server/conf"
	"server/ent"
	"server/pkg/dbinit"
	"server/pkg/entclient"
	"server/pkg/params"
)

// 反射一致性集成钉死：走生产装配链路（sourceOpts.build() 全量实例 → NewCollector → Pipeline），
// 断言 ①每个注册源在目录表内有行且 Supports 反射域集 ⊇ 参数镜像表 dsCapability 的声明；
// ②yaml 形配置里每条 default_chain ⊆ 目录输出该域的 candidates（与 NewPipeline 的
// skip-warning 场景互补：目录接口暴露的候选源永远真支持该域）；
// ③tdx_http_archive/openbb_fred 两条 build 开关分支各断言注册态与 declared 透传。
// NT_TEST_PG_PORT 哨兵模式：NewCollector 需要真实 PG（LoadHealth 落库回放）。
func catalogAdapterPG(t *testing.T) *ent.Client {
	t.Helper()
	raw := os.Getenv("NT_TEST_PG_PORT")
	if raw == "" {
		t.Skip("set isolated NT_TEST_PG_PORT")
	}
	port, e := strconv.Atoi(raw)
	if e != nil || port == 5432 || port < 1 || port > 65535 {
		t.Fatal("requires non-production isolated PG port")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, e := entclient.Open(ctx, entclient.Config{DB: dbinit.Config{Host: "127.0.0.1", Port: int32(port), User: "postgres", Password: "postgres", Database: "novatrader_catalog_test", SSLMode: "disable", Timeout: 3 * time.Second}})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { c.Close() })
	return c.Client
}

func catalogConf(optional bool) *conf.Datahub {
	cfg := &conf.Datahub{
		AllowUnofficial: true,
		BreakerFailures: 3,
		BreakerCooldown: durationpb.New(time.Minute),
		Sources:         map[string]*conf.DataSource{},
		Domains:         map[string]*conf.DataDomain{},
	}
	cfg.Domains[biz.DomainDailyBar] = &conf.DataDomain{Enabled: true, Sources: []string{biz.SourceTushare, biz.SourceTdxFile, biz.SourceEastmoney, biz.SourceTdxArchive}}
	cfg.Domains[biz.DomainMacro] = &conf.DataDomain{Enabled: true, Sources: []string{biz.SourceAkshare}}
	cfg.Domains[biz.DomainSnapshot] = &conf.DataDomain{Enabled: true, Sources: []string{biz.SourceTdxLocal, biz.SourceTdxTCP, biz.SourceEastmoney, biz.SourceSina, biz.SourceTencent}}
	cfg.Domains[biz.DomainUnlock] = &conf.DataDomain{Enabled: true, Sources: []string{biz.SourceEastmoney}}
	cfg.Domains[biz.DomainFlash] = &conf.DataDomain{Enabled: true, Sources: []string{biz.SourceEastmoney, biz.SourceTavily, biz.SourceRSS}}
	if optional {
		// 两个条件装配源的开支线（sourceOpts.build 只在 enabled 时实例化）。
		cfg.Sources[biz.SourceTdxArchive] = &conf.DataSource{Enabled: true, Official: true, Url: "http://127.0.0.1:10800"}
		cfg.Sources[biz.SourceOpenBBFred] = &conf.DataSource{Enabled: true, Official: true, Url: "http://127.0.0.1:10800"}
	}
	return cfg
}

func TestCatalogReflectionConsistency(t *testing.T) {
	c := catalogAdapterPG(t)
	repo := NewRepo(c)
	logger := log.NewStdLogger(os.Stdout)

	// 场景一：tdx_http_archive/openbb_fred 配置启用 → 两者注册态为真，反射域与表声明一致。
	t.Run("optional enabled", func(t *testing.T) {
		col, err := NewCollector(catalogConf(true), repo, nil, nil, logger)
		if err != nil {
			t.Fatal(err)
		}
		cat := biz.BuildCatalog(col.Pipeline(), catalogConf(true))
		rows := map[string]biz.SourceManifest{}
		for _, m := range cat.Sources {
			rows[m.Name] = m
		}
		for _, name := range params.DataSourceNames() {
			m, ok := rows[name]
			if !ok {
				t.Fatalf("白名单源 %s 目录缺行", name)
			}
			for _, d := range catalogTestDomains(t) {
				if !params.SupportsDataSource(d, name) {
					continue
				}
				if !m.Registered {
					// 未装配源（sina7x24/baostock：装配属 N4-02）的镜像表暂无能力声明，跳过反射断言。
					continue
				}
				found := false
				for _, have := range m.Domains {
					if have == d {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("注册源 %s 反射域缺 %s（参数镜像表声明）：%v", name, d, m.Domains)
				}
			}
		}
		arc, fre := rows[biz.SourceTdxArchive], rows[biz.SourceOpenBBFred]
		if !arc.Registered || len(arc.Domains) != 1 || arc.Domains[0] != biz.DomainDailyBar {
			t.Fatalf("tdx_http_archive 启用态反射错位：%+v", arc)
		}
		if !fre.Registered || len(fre.Domains) != 1 || fre.Domains[0] != biz.DomainMacro {
			t.Fatalf("openbb_fred 启用态反射错位：%+v", fre)
		}
		// sina7x24/baostock 本批装配落地：必须注册，且反射域恰等于其接口能力集
		// （sina7x24 实现 IntelSource → 5 个 intel 域；baostock 实现 daily_bar+finance）。
		wantReflect := map[string][]string{
			biz.SourceSina7x24: {biz.DomainFlash, biz.DomainNews, biz.DomainAnnouncement, biz.DomainReport, biz.DomainGuba},
			biz.SourceBaostock: {biz.DomainDailyBar, biz.DomainFinance},
		}
		for name, want := range wantReflect {
			m := rows[name]
			if !m.Registered {
				t.Fatalf("%s 装配后应注册：%+v", name, m)
			}
			if len(m.Domains) != len(want) {
				t.Fatalf("%s 反射域 %v ≠ 能力集 %v", name, m.Domains, want)
			}
			for i, d := range want {
				if m.Domains[i] != d {
					t.Fatalf("%s 反射域[%d] %s ≠ %s（%v）", name, i, m.Domains[i], d, m.Domains)
				}
			}
		}
		// 每条 default_chain ⊆ 该域 candidates。
		domains := map[string]biz.DomainInfo{}
		for _, d := range cat.Domains {
			domains[d.Name] = d
		}
		for name, dc := range catalogConf(true).GetDomains() {
			di, ok := domains[name]
			if !ok {
				t.Fatalf("cfg 域 %s 目录缺行", name)
			}
			cand := map[string]bool{}
			for _, s := range di.Candidates {
				cand[s] = true
			}
			for _, s := range dc.GetSources() {
				if !cand[s] {
					t.Fatalf("域 %s 默认链源 %s 不在 candidates：%v", name, s, di.Candidates)
				}
			}
		}
	})

	// 场景二：条件源未启用 → registered:false 且 declared 域透传（源墙可见可配而不消失）。
	t.Run("optional disabled", func(t *testing.T) {
		col, err := NewCollector(catalogConf(false), repo, nil, nil, logger)
		if err != nil {
			t.Fatal(err)
		}
		cat := biz.BuildCatalog(col.Pipeline(), catalogConf(false))
		rows := map[string]biz.SourceManifest{}
		for _, m := range cat.Sources {
			rows[m.Name] = m
		}
		if m := rows[biz.SourceTdxArchive]; m.Registered || len(m.Domains) != 1 || m.Domains[0] != biz.DomainDailyBar {
			t.Fatalf("tdx_http_archive 未启用态 declared 错位：%+v", m)
		}
		if m := rows[biz.SourceOpenBBFred]; m.Registered || len(m.Domains) != 1 || m.Domains[0] != biz.DomainMacro {
			t.Fatalf("openbb_fred 未启用态 declared 错位：%+v", m)
		}
		// 常驻源（无 token 也能实例化：token 解析失败只告警）必须全部注册。
		for _, name := range []string{biz.SourceTushare, biz.SourceTdxLocal, biz.SourceTdxFile, biz.SourceTdxTCP, biz.SourceAkshare, biz.SourceEastmoney, biz.SourceSina, biz.SourceTencent, biz.SourceCninfo, biz.SourceTavily, biz.SourceRSS, biz.SourceSina7x24, biz.SourceBaostock} {
			if m := rows[name]; !m.Registered {
				t.Fatalf("常驻源 %s 应注册：%+v", name, m)
			}
		}
	})
}

func catalogTestDomains(t *testing.T) []string {
	t.Helper()
	return params.DataDomainNames()
}
