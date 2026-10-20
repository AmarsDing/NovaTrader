package biz

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/log"

	"server/pkg/events"
)

func intv(v int) *int { return &v }

func reportIntelFixture(at time.Time) Intel {
	return Intel{
		Source: SourceEastmoney, SourceID: "AP001", Kind: KindReport,
		Title:       "贵州茅台：三季报点评",
		PublishTime: at.UTC().Format(time.RFC3339),
		Codes:       []string{"600519"},
		Forecast: &ReportForecast{
			EPS: []EPSPoint{
				{Year: 2026, EPS: f64v(0.64), PE: f64v(20.18)},
				{Year: 2027, EPS: f64v(1.10)},
				{Year: 2028}, // 双 nil 年度点整条丢弃——没取到≠0
			},
			AimPriceLow: f64v(12.5), AimPriceHigh: f64v(14),
			Rating: "买入", RatingChange: intv(2),
			OrgSName: "东吴证券", OrgName: "东吴证券研究所", Industry: "白酒",
		},
	}
}

func TestForecastToEstimateRowsGuardAndMapping(t *testing.T) {
	at := time.Date(2026, 10, 19, 10, 0, 0, 0, shanghai())

	rows := forecastToEstimateRows(reportIntelFixture(at))
	if len(rows) != 2 {
		t.Fatalf("双 nil 年度点应丢弃，rows=%d", len(rows))
	}
	r0 := rows[0]
	if r0.Symbol != "600519" || r0.InfoCode != "AP001" || r0.OrgSname != "东吴证券" {
		t.Fatalf("锚列映射错: %+v", r0)
	}
	if got := r0.PublishDate.UTC(); !got.Equal(at.UTC()) {
		t.Fatalf("PublishDate=%v", got)
	}
	if r0.EstYear != 2026 || r0.Eps == nil || *r0.Eps != 0.64 || r0.Pe == nil || *r0.Pe != 20.18 {
		t.Fatalf("首年点映射错: %+v", r0)
	}
	if r0.Rating == nil || *r0.Rating != "买入" || r0.RatingChange == nil || *r0.RatingChange != 2 {
		t.Fatalf("评级映射错: %+v", r0)
	}
	// 缺披露列保持 nil：Researcher/LastRating/OrgCode 空串→NULL，ReportType 缺席→NULL。
	if r0.Researcher != nil || r0.LastRating != nil || r0.OrgCode != nil || r0.ReportType != nil {
		t.Fatalf("缺披露不得冒有: %+v", r0)
	}
	if rows[1].EstYear != 2027 || rows[1].Eps == nil || rows[1].Pe != nil {
		t.Fatalf("次年点（PE 缺失）映射错: %+v", rows[1])
	}
	// 给值列直断言（对抗复核盲区）：目标价上下限与 Industry 不得写反/丢失。
	if r0.AimPriceLow == nil || *r0.AimPriceLow != 12.5 || r0.AimPriceHigh == nil || *r0.AimPriceHigh != 14 {
		t.Fatalf("目标价区间映射错: %+v", r0)
	}
	if r0.Industry == nil || *r0.Industry != "白酒" {
		t.Fatalf("Industry 映射错: %+v", r0)
	}

	// PE-only 年度点保留（EPS=nil 不丢行——只有双 nil 才丢）。
	peOnly := reportIntelFixture(at)
	peOnly.Forecast.EPS = []EPSPoint{{Year: 2026, PE: f64v(30)}}
	got := forecastToEstimateRows(peOnly)
	if len(got) != 1 || got[0].Eps != nil || got[0].Pe == nil || *got[0].Pe != 30 {
		t.Fatalf("PE-only 年度点应保留: %+v", got)
	}

	// 多票研报只落首票（Codes[0] 为归一主码；reportapi 实测单票，此钉住现状语义）。
	multi := reportIntelFixture(at)
	multi.Codes = []string{"600519", "000001"}
	got = forecastToEstimateRows(multi)
	if len(got) != 2 || got[0].Symbol != "600519" || got[1].Symbol != "600519" {
		t.Fatalf("多票应只取 Codes[0]: %+v", got)
	}

	// Rating 空串 → nil 直测（sp0 路径不得靠旁证）。
	noRating := reportIntelFixture(at)
	noRating.Forecast.Rating = ""
	if got = forecastToEstimateRows(noRating); len(got) != 2 || got[0].Rating != nil {
		t.Fatalf("Rating 空串应为 nil: %+v", got)
	}

	// clip：超长展示标签列截到 MaxLen（防单行毒化整 chunk 楔形）；超长幂等锚丢行保锚。
	longOrg := reportIntelFixture(at)
	longOrg.Forecast.OrgSName = "东吴证券股份有限公司研究所国际金融衍生品与量化策略超级特别研究团队"
	if got = forecastToEstimateRows(longOrg); len(got) != 2 || len([]rune(got[0].OrgSname)) != clipMaxOrgSname {
		t.Fatalf("org_sname 应截 %d 字: %+v", clipMaxOrgSname, got)
	}
	longCode := reportIntelFixture(at)
	longCode.SourceID = "AP" + string(make([]byte, 40))
	if got = forecastToEstimateRows(longCode); got != nil {
		t.Fatalf("info_code 超 %d 应丢行保锚: %+v", clipMaxInfoCode, got)
	}

	// 五重回退哨兵：任一缺锚整条无行。
	cases := map[string]func(*Intel){
		"Forecast 空":    func(it *Intel) { it.Forecast = nil },
		"Codes 空":       func(it *Intel) { it.Codes = nil },
		"SourceID 空":    func(it *Intel) { it.SourceID = "" },
		"OrgSName 空":    func(it *Intel) { it.Forecast.OrgSName = "" },
		"PublishTime 坏": func(it *Intel) { it.PublishTime = "not-a-time" },
	}
	for name, spoil := range cases {
		it := reportIntelFixture(at)
		spoil(&it)
		if got := forecastToEstimateRows(it); got != nil {
			t.Fatalf("%s 应无行, got %d", name, len(got))
		}
	}

	// 仅评级无 EPS 的研报：EPS 列表为空 → 无行（预测锚缺失不建空壳行，同 dataset_record 则）。
	bare := reportIntelFixture(at)
	bare.Forecast.EPS = nil
	if got := forecastToEstimateRows(bare); len(got) != 0 {
		t.Fatalf("EPS 空应无行, got %d", len(got))
	}
}

// estCapRepo 记录 UpsertReportEstimates/SetCursor 调用序与载荷，供发布契约断言。
type estCapRepo struct {
	Repo
	cursors   map[string]string
	cursorErr error
	rows      []EstimateRow
	source    string
	rd        Ready
	err       error
	order     *[]string
}

func (r *estCapRepo) Cursor(_ context.Context, name string) (string, error) {
	return r.cursors[name], r.cursorErr
}
func (r *estCapRepo) SetCursor(_ context.Context, name, value string) error {
	*r.order = append(*r.order, "cursor")
	r.cursors[name] = value
	return nil
}
func (r *estCapRepo) UpsertReportEstimates(_ context.Context, items []EstimateRow, source string, rd Ready) (int, error) {
	*r.order = append(*r.order, "upsert")
	r.rows, r.source, r.rd = items, source, rd
	if r.err != nil {
		return 0, r.err
	}
	return len(items), nil
}

type ordBus struct {
	order  *[]string
	envs   []events.Envelope
	failAt int // 第 failAt 次（1 起）Publish 返回错，0=不失败
	err    error
	calls  int
}

func (b *ordBus) Publish(_ context.Context, env events.Envelope) error {
	b.calls++
	if b.failAt > 0 && b.calls == b.failAt {
		return b.err
	}
	*b.order = append(*b.order, "publish")
	b.envs = append(b.envs, env)
	return nil
}

type reportSrc struct{ items []Intel }

func (s *reportSrc) Name() string { return SourceEastmoney }
func (s *reportSrc) Intel(context.Context, string, IntelQuery) ([]Intel, error) {
	return s.items, nil
}

func reportCollector(t *testing.T, repo Repo, bus Publisher, src Source) *Collector {
	t.Helper()
	p, warns := NewPipeline(Config{
		AllowUnofficial: true, BreakerFailures: 3, BreakerCooldown: time.Minute,
		Sources: map[string]SourceConfig{SourceEastmoney: {Enabled: true, Official: false, Timeout: time.Second}},
		Domains: map[string]DomainConfig{DomainReport: {Enabled: true, Sources: []string{SourceEastmoney}}},
	}, []Source{src}, nil)
	if len(warns) > 0 {
		t.Fatal(warns)
	}
	return NewCollector(p, repo, nil, bus, nil, nil, Options{}, log.DefaultLogger)
}

func TestCollectReportPersistsEstimatesBeforePublish(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, shanghai())
	order := []string{}
	repo := &estCapRepo{cursors: map[string]string{}, order: &order}
	bus := &ordBus{order: &order}
	src := &reportSrc{items: []Intel{reportIntelFixture(now)}}
	c := reportCollector(t, repo, bus, src)
	c.now = func() time.Time { return now }

	res, err := c.CollectIntel(context.Background(), DomainReport)
	if err != nil || res.Rows != 1 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if len(order) != 3 || order[0] != "upsert" || order[1] != "publish" || order[2] != "cursor" {
		t.Fatalf("契约序=落库→发事件→推水位, order=%v", order)
	}
	if len(repo.rows) != 2 || repo.source != SourceEastmoney {
		t.Fatalf("repo=%+v", repo)
	}
	if repo.rd.Domain != "report_estimate" || repo.rd.TradeDate != "" || repo.rd.Count != 2 || repo.rd.Stale {
		t.Fatalf("ready=%+v", repo.rd)
	}
	if repo.rd.Source != SourceEastmoney {
		t.Fatalf("ready.source=%q（ready 载荷必须带采集源）", repo.rd.Source)
	}
	if repo.rows[0].OrgSname != "东吴证券" || repo.rows[0].EstYear != 2026 {
		t.Fatalf("rows=%+v", repo.rows)
	}
	if len(bus.envs) != 1 {
		t.Fatalf("envs=%d", len(bus.envs))
	}
	env := bus.envs[0]
	if env.Subject != "intel.raw.report" {
		t.Fatalf("subject=%q", env.Subject)
	}
	var got Intel
	if err := json.Unmarshal(env.Payload, &got); err != nil || got.SourceID != "AP001" ||
		got.Forecast == nil || got.Forecast.OrgSName != "东吴证券" {
		t.Fatalf("payload=%s err=%v", env.Payload, err)
	}
	if repo.cursors["intel:report"] != now.UTC().Format(time.RFC3339) {
		t.Fatalf("水位应=条目发布时刻: %q", repo.cursors["intel:report"])
	}
}

func TestCollectReportEstimateWriteFailureBlocksPublish(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, shanghai())
	order := []string{}
	repo := &estCapRepo{cursors: map[string]string{}, order: &order, err: errors.New("db down")}
	bus := &ordBus{order: &order}
	src := &reportSrc{items: []Intel{reportIntelFixture(now)}}
	c := reportCollector(t, repo, bus, src)
	c.now = func() time.Time { return now }

	_, err := c.CollectIntel(context.Background(), DomainReport)
	if err == nil {
		t.Fatal("落库失败必须整体失败")
	}
	if len(order) != 1 || order[0] != "upsert" {
		t.Fatalf("失败不得发事件, order=%v", order)
	}
	if len(bus.envs) != 0 {
		t.Fatalf("envs=%d", len(bus.envs))
	}
	if _, ok := repo.cursors["intel:report"]; ok {
		t.Fatal("失败水位不得前移")
	}
}

// 中途发布失败：水位停在最后一条成功发出的条目时刻，且错误上抛（下轮重发）。
func TestCollectReportMidPublishFailureKeepsWatermark(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, shanghai())
	at1 := now.Add(-time.Hour)
	order := []string{}
	repo := &estCapRepo{cursors: map[string]string{}, order: &order}
	bus := &ordBus{order: &order, failAt: 2, err: errors.New("nats down")}
	first := reportIntelFixture(at1)
	first.SourceID = "IC-MID-A"
	second := reportIntelFixture(now)
	second.SourceID = "IC-MID-B"
	c := reportCollector(t, repo, bus, &reportSrc{items: []Intel{second, first}})
	c.now = func() time.Time { return now }

	res, err := c.CollectIntel(context.Background(), DomainReport)
	if err == nil {
		t.Fatal("发布失败必须上抛错误")
	}
	if res.Rows != 1 || len(bus.envs) != 1 {
		t.Fatalf("只应发出 1 条: res=%+v envs=%d", res, len(bus.envs))
	}
	var got Intel
	if err := json.Unmarshal(bus.envs[0].Payload, &got); err != nil || got.SourceID != "IC-MID-A" {
		t.Fatalf("按时间升序应先发早条目: %s err=%v", bus.envs[0].Payload, err)
	}
	if repo.cursors["intel:report"] != at1.UTC().Format(time.RFC3339) {
		t.Fatalf("水位应停在最后成功条目: %q", repo.cursors["intel:report"])
	}
	if len(order) != 3 || order[0] != "upsert" || order[1] != "publish" || order[2] != "cursor" {
		t.Fatalf("order=%v", order)
	}
}

// Cursor 读失败：回退 24h 全量拉取，落库/发事件/写水位照常进行（水位不可读≠停摆）。
func TestCollectReportCursorReadFailureFallsBack(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, shanghai())
	order := []string{}
	repo := &estCapRepo{cursors: map[string]string{}, order: &order, cursorErr: errors.New("pg down")}
	bus := &ordBus{order: &order}
	it := reportIntelFixture(now)
	it.SourceID = "IC-CUR-A" // intelSeen 进程级去重，不得复用他测已发过的 SourceID
	c := reportCollector(t, repo, bus, &reportSrc{items: []Intel{it}})
	c.now = func() time.Time { return now }

	res, err := c.CollectIntel(context.Background(), DomainReport)
	if err != nil || res.Rows != 1 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if len(order) != 3 || order[0] != "upsert" || order[1] != "publish" || order[2] != "cursor" {
		t.Fatalf("回退全量照常走全链, order=%v", order)
	}
	if repo.cursors["intel:report"] != now.UTC().Format(time.RFC3339) {
		t.Fatalf("水位仍应前移: %q", repo.cursors["intel:report"])
	}
}
