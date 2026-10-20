package biz

import (
	"context"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/log"
)

/* B1 全市场两融（margin_mkt）域链测试：源→校验→仓储→ready 事件。 */

type fakeMarginMktSrc struct {
	name  string
	items []MarginMkt
	err   error
	calls int
}

func (f *fakeMarginMktSrc) Name() string { return f.name }
func (f *fakeMarginMktSrc) MarginMkts(context.Context, time.Time) ([]MarginMkt, error) {
	f.calls++
	return f.items, f.err
}

func f64v(v float64) *float64 { return &v }
func i64v(v int64) *int64     { return &v }

type marginMktRepo struct {
	Repo
	called int
	rows   int
	day    time.Time
	source string
	rd     Ready
}

func (r *marginMktRepo) UpsertMarginMkt(_ context.Context, day time.Time, items []MarginMkt, source string, rd Ready) (int, error) {
	r.called++
	r.rows = len(items)
	r.day = day
	r.source = source
	r.rd = rd
	return len(items), nil
}

func marginMktPipe(t *testing.T, srcs []Source, names ...string) *Pipeline {
	t.Helper()
	cfg := Config{
		AllowUnofficial: true, BreakerFailures: 3, BreakerCooldown: time.Minute,
		Sources: map[string]SourceConfig{},
		Domains: map[string]DomainConfig{DomainMarginMkt: {Enabled: true, Sources: names}},
	}
	for _, n := range names {
		cfg.Sources[n] = SourceConfig{Enabled: true, Official: true, Timeout: time.Second}
	}
	p, warn := NewPipeline(cfg, srcs, nil)
	if len(warn) > 0 {
		t.Fatalf("warnings %v", warn)
	}
	return p
}

func TestCollectMarginMktUpsertsExchangeRows(t *testing.T) {
	day := time.Date(2026, 10, 17, 0, 0, 0, 0, shanghai())
	src := &fakeMarginMktSrc{name: SourceTushare, items: []MarginMkt{
		{Exchange: "SSE", Rzye: f64v(1.5), Rzmre: f64v(2), Rqye: f64v(3)},
		{Exchange: "SZSE", Rzye: f64v(4), Rzrqye: f64v(5), Rqmcl: i64v(6)},
		{Exchange: "BSE"}, // 整行缺列合法：Nillable 指针全 nil 落 NULL
	}}
	p := marginMktPipe(t, []Source{src}, SourceTushare)
	repo := &marginMktRepo{}
	c := NewCollector(p, repo, &snapshotMemoryStore{}, nil, nil, nil, Options{}, log.DefaultLogger)
	sum, err := c.CollectMarginMkt(context.Background(), day, "")
	if err != nil || sum.Domain != DomainMarginMkt || sum.Source != SourceTushare || sum.Rows != 3 {
		t.Fatalf("sum=%+v err=%v", sum, err)
	}
	if repo.called != 1 || repo.rows != 3 || !repo.day.Equal(day) || repo.source != SourceTushare {
		t.Fatalf("repo=%+v", repo)
	}
	if repo.rd.Domain != DomainMarginMkt || repo.rd.TradeDate != "2026-10-17" || repo.rd.Count != 3 {
		t.Fatalf("ready=%+v", repo.rd)
	}
}

func TestCollectMarginMktEmptyRejected(t *testing.T) {
	day := time.Date(2026, 10, 17, 0, 0, 0, 0, shanghai())
	src := &fakeMarginMktSrc{name: SourceTushare}
	p := marginMktPipe(t, []Source{src}, SourceTushare)
	repo := &marginMktRepo{}
	c := NewCollector(p, repo, &snapshotMemoryStore{}, nil, nil, nil, Options{}, log.DefaultLogger)
	_, err := c.CollectMarginMkt(context.Background(), day, "")
	if err == nil {
		t.Fatal("空结果必须报错（nonEmpty 校验），不能静默落 0 行")
	}
	if repo.called != 0 {
		t.Fatal("校验不过不得写库")
	}
}

func TestSupportsMarginMkt(t *testing.T) {
	if Supports(DomainMarginMkt, &fakeSnap{name: SourceEastmoney}) {
		t.Fatal("无 MarginMktSource 的源不得宣称支持 margin_mkt")
	}
	if !Supports(DomainMarginMkt, &fakeMarginMktSrc{name: SourceTushare}) {
		t.Fatal("实现 MarginMktSource 的源应支持 margin_mkt")
	}
}
