package biz

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"server/conf"
	"server/pkg/events"

	"github.com/go-kratos/kratos/v2/log"
)

// memRepo 是测试用的内存仓储，行为对齐 data 层的约定。
type memRepo struct {
	mu       sync.Mutex
	items    map[int]*memItem
	clusters map[int]*memCluster
	hashes   map[string]bool
	outbox   []events.Envelope
	stocks   []StockEntry
	aliases  []AliasEntry
	nextID   int
}

type memItem struct {
	it     Item
	status string
	score  Score
	links  []Link
	facts  []Fact
}

type memCluster struct {
	head    int
	count   int
	alerted bool
}

func newMemRepo() *memRepo {
	return &memRepo{items: map[int]*memItem{}, clusters: map[int]*memCluster{}, hashes: map[string]bool{}}
}

func (r *memRepo) Find(_ context.Context, source, sourceID, hash string) (*Item, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, m := range r.items {
		match := hash != "" && m.it.Hash == hash
		if sourceID != "" && m.it.Source == source && m.it.SourceID == sourceID {
			match = true
		}
		if !match {
			continue
		}
		it := m.it
		if c := r.clusters[it.ClusterID]; c != nil {
			it.Head = c.head == it.ID
		}
		return &it, m.status, nil
	}
	return nil, "", nil
}

func (r *memRepo) CreateItem(_ context.Context, it *Item, status, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.hashes[it.Hash] {
		return ErrDuplicate
	}
	r.hashes[it.Hash] = true
	r.nextID++
	it.ID = r.nextID
	if status == StatusPending {
		if it.ClusterID == 0 {
			r.nextID++
			it.ClusterID = r.nextID
			r.clusters[it.ClusterID] = &memCluster{head: it.ID, count: 1}
			it.Head = true
		} else {
			r.clusters[it.ClusterID].count++
		}
	}
	r.items[it.ID] = &memItem{it: *it, status: status}
	return nil
}

func (r *memRepo) SaveScored(_ context.Context, s *Scored) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	m := r.items[s.Item.ID]
	if m == nil {
		return ErrNotFound
	}
	if m.status != StatusPending {
		return nil
	}
	m.status, m.score, m.links, m.facts = StatusScored, s.Score, s.Links, s.Facts
	if s.Event != nil {
		r.outbox = append(r.outbox, *s.Event)
	}
	if s.Alert != nil && !r.clusters[s.Item.ClusterID].alerted {
		r.clusters[s.Item.ClusterID].alerted = true
		r.outbox = append(r.outbox, *s.Alert)
	}
	return nil
}

func (r *memRepo) Fingerprints(context.Context, time.Time) ([]Fingerprint, error) { return nil, nil }

func (r *memRepo) Pending(_ context.Context, limit int) ([]*Item, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*Item
	for _, m := range r.items {
		if m.status == StatusPending && len(out) < limit {
			it := m.it
			it.Head = r.clusters[it.ClusterID].head == it.ID
			out = append(out, &it)
		}
	}
	return out, nil
}

func (r *memRepo) SentimentInputs(_ context.Context, code string, since time.Time) ([]SentimentInput, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []SentimentInput
	for _, m := range r.items {
		if m.status != StatusScored || !m.it.Head || m.it.PublishTime.Before(since) {
			continue
		}
		for _, l := range m.links {
			if l.TargetType == TargetStock && l.Target == code {
				out = append(out, SentimentInput{m.score.Sentiment, m.score.Importance, m.score.HalfLifeMinutes, m.it.PublishTime, l.Confidence})
			}
		}
	}
	return out, nil
}

func (r *memRepo) Dictionary(context.Context) ([]StockEntry, []AliasEntry, error) {
	return r.stocks, r.aliases, nil
}

func (r *memRepo) UpsertAlias(_ context.Context, a AliasInput) (int, error) {
	r.aliases = append(r.aliases, AliasEntry{Alias: a.Alias, Code: a.StockCode, Confidence: a.Confidence})
	return len(r.aliases), nil
}

func (r *memRepo) GetItem(_ context.Context, id int) (*ItemView, error) {
	m, ok := r.items[id]
	if !ok {
		return nil, ErrNotFound
	}
	return &ItemView{ID: id, Sentiment: m.score.Sentiment, HalfLifeMinutes: m.score.HalfLifeMinutes, PublishTime: m.it.PublishTime}, nil
}

func (r *memRepo) Timeline(context.Context, string, time.Time, time.Time, int) ([]*ItemView, error) {
	return nil, nil
}

func (r *memRepo) HotCounts(context.Context, time.Time, time.Time) (map[[2]string]int, error) {
	return map[[2]string]int{}, nil
}

func (r *memRepo) subjects() []string {
	var out []string
	for _, e := range r.outbox {
		out = append(out, e.Subject)
	}
	return out
}

func newTestUsecase(t *testing.T, repo *memRepo, model Model, c *conf.Intel) *Usecase {
	t.Helper()
	repo.stocks = []StockEntry{
		{Code: "600519.SH", Name: "贵州茅台", Concepts: []string{"白酒"}},
		{Code: "300750.SZ", Name: "宁德时代"},
	}
	u := NewUsecase(repo, model, c, log.DefaultLogger)
	if err := u.Warm(context.Background()); err != nil {
		t.Fatal(err)
	}
	return u
}

const buybackBody = "贵州茅台公告称，公司拟以自有资金回购股份，回购金额不低于10亿元且不超过20亿元，回购价格不超过1800元每股，用于注销并减少注册资本，回购期限为12个月。"

func TestPipelineDedupClusterAndEvents(t *testing.T) {
	repo := newMemRepo()
	u := newTestUsecase(t, repo, nil, nil)
	ctx := context.Background()

	first, err := u.Process(ctx, Raw{Source: "cninfo", SourceID: "1", Kind: "announcement", Title: "贵州茅台：关于回购股份方案的公告", Content: buybackBody, Codes: []string{"SHSE.600519"}})
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != ResultScored || first.ClusterID == 0 {
		t.Fatalf("first = %+v", first)
	}
	again, _ := u.Process(ctx, Raw{Source: "cninfo", SourceID: "1", Title: "x", Content: "y"})
	if again.Status != ResultDuplicate {
		t.Fatalf("same source id = %+v", again)
	}
	copyOther, _ := u.Process(ctx, Raw{Source: "eastmoney", Kind: "announcement", Title: "贵州茅台：关于回购股份方案的公告", Content: buybackBody + "（来源：东方财富）"})
	if copyOther.Status != ResultDuplicate {
		t.Fatalf("exact copy from another outlet = %+v", copyOther)
	}
	near, err := u.Process(ctx, Raw{Source: "sina", Kind: "news", Title: "【快讯】贵州茅台：关于回购股份方案的公告", Content: buybackBody})
	if err != nil {
		t.Fatal(err)
	}
	if near.Status != ResultNearDuplicate || near.ClusterID != first.ClusterID {
		t.Fatalf("near duplicate = %+v, first cluster %d", near, first.ClusterID)
	}
	if got := repo.clusters[first.ClusterID].count; got != 2 {
		t.Fatalf("cluster size = %d", got)
	}

	subs := repo.subjects()
	if len(subs) != 1 || subs[0] != events.SubjectIntelScored {
		t.Fatalf("outbox = %v (one scored event for the head; a routine buyback does not alert)", subs)
	}

	penalty, err := u.Process(ctx, Raw{Source: "cninfo", SourceID: "2", Kind: "announcement", Codes: []string{"600519.SH"},
		Title: "贵州茅台：关于收到中国证监会立案告知书的公告", Content: "公司因涉嫌信息披露违规，被中国证监会立案调查。"})
	if err != nil {
		t.Fatal(err)
	}
	subs = repo.subjects()
	sort.Strings(subs)
	if len(subs) != 3 || subs[0] != events.SubjectIntelAlert {
		t.Fatalf("outbox after penalty = %v", subs)
	}
	if !repo.clusters[penalty.ClusterID].alerted {
		t.Fatal("cluster not marked alerted")
	}
	var ev ScoredEvent
	_ = json.Unmarshal(repo.outbox[0].Payload, &ev)
	if ev.EventType != EventBuyback || !ev.Degraded || ev.Scorer != ScorerRule || len(ev.Links) == 0 || ev.Links[0].Target != "600519.SH" {
		t.Fatalf("scored event = %+v", ev)
	}
	head := repo.items[first.NewsID]
	if len(head.facts) == 0 {
		t.Fatal("announcement facts not extracted")
	}
	if repo.items[near.NewsID].score.Degraded {
		t.Fatal("near duplicates skip the model and are not marked degraded")
	}
}

func TestPipelineFilterAndInvalid(t *testing.T) {
	repo := newMemRepo()
	u := newTestUsecase(t, repo, nil, &conf.Intel{BlockedSources: []string{"spam"}})
	ctx := context.Background()
	res, err := u.Process(ctx, Raw{Source: "spam", Title: "宁德时代大涨消息", Content: "内容"})
	if err != nil || res.Status != ResultFiltered {
		t.Fatalf("blocked = %+v %v", res, err)
	}
	if len(repo.outbox) != 0 {
		t.Fatal("filtered item published an event")
	}
	if _, err := u.Process(ctx, Raw{Title: "无来源"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing source err = %v", err)
	}
}

type fakeModel struct {
	reply string
	err   error
	calls []string
}

func (f *fakeModel) Complete(_ context.Context, model, _, _ string) (string, error) {
	f.calls = append(f.calls, model)
	return f.reply, f.err
}

func TestPipelineModelReviewAndFallback(t *testing.T) {
	c := &conf.Intel{Llm: &conf.IntelLLM{Enabled: true, SmallModel: "qwen-small", LargeModel: "qwen-large"}}
	m := &fakeModel{reply: `{"event_type":"contract","sentiment":0.6,"importance":4,"reason":"大单"}`}
	repo := newMemRepo()
	u := newTestUsecase(t, repo, m, c)
	res, err := u.Process(context.Background(), Raw{Source: "news", Kind: "news", Title: "宁德时代中标海外储能大单", Content: "宁德时代宣布获得欧洲储能项目订单，合同金额约50亿元。"})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.calls) != 2 || m.calls[0] != "small" || m.calls[1] != "large" {
		t.Fatalf("model calls = %v", m.calls)
	}
	s := repo.items[res.NewsID].score
	if s.Scorer != ScorerLarge || s.EventType != EventContract || s.Degraded {
		t.Fatalf("score = %+v", s)
	}

	m2 := &fakeModel{err: errors.New("connection refused")}
	repo2 := newMemRepo()
	u2 := newTestUsecase(t, repo2, m2, c)
	res, err = u2.Process(context.Background(), Raw{Source: "news", Title: "宁德时代中标海外储能大单", Content: "合同金额约50亿元。"})
	if err != nil {
		t.Fatal(err)
	}
	s = repo2.items[res.NewsID].score
	if s.Scorer != ScorerRule || !s.Degraded || s.EventType != EventContract {
		t.Fatalf("fallback = %+v", s)
	}
}

func TestSentimentShiftAlert(t *testing.T) {
	repo := newMemRepo()
	u := newTestUsecase(t, repo, nil, &conf.Intel{AlertImportance: 5, AlertShift: 0.4})
	ctx := context.Background()
	if _, err := u.Process(ctx, Raw{Source: "a", Kind: "news", Title: "宁德时代签订合同", Content: "宁德时代与客户签订合同，订单稳定。"}); err != nil {
		t.Fatal(err)
	}
	before := len(repo.outbox)
	if _, err := u.Process(ctx, Raw{Source: "b", Kind: "announcement", Codes: []string{"300750.SZ"}, Title: "宁德时代：关于控股股东减持股份计划的公告", Content: "控股股东拟减持不超过3%股份，存在股价下滑风险。"}); err != nil {
		t.Fatal(err)
	}
	var alert Alert
	for _, e := range repo.outbox[before:] {
		if e.Subject == events.SubjectIntelAlert {
			_ = json.Unmarshal(e.Payload, &alert)
		}
	}
	if alert.Reason != AlertReasonShift || alert.Shift == nil || alert.Shift.After >= alert.Shift.Before {
		t.Fatalf("alert = %+v", alert)
	}
}

func TestRetryResumesPending(t *testing.T) {
	repo := newMemRepo()
	u := newTestUsecase(t, repo, nil, nil)
	raw := Raw{Source: "cninfo", SourceID: "p1", Kind: "announcement", Codes: []string{"600519.SH"}, Title: "贵州茅台：关于回购股份的公告", Content: buybackBody}
	it, err := u.prepare(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateItem(context.Background(), it, StatusPending, ""); err != nil {
		t.Fatal(err)
	}
	before := len(repo.items)
	res, err := u.Process(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != ResultScored || res.NewsID != it.ID || len(repo.items) != before {
		t.Fatalf("retry = %+v items %d", res, len(repo.items))
	}
	if repo.items[it.ID].status != StatusScored || len(repo.outbox) != 1 {
		t.Fatalf("status %s events %d", repo.items[it.ID].status, len(repo.outbox))
	}
	again, err := u.Process(context.Background(), raw)
	if err != nil || again.Status != ResultDuplicate || len(repo.outbox) != 1 {
		t.Fatalf("second retry = %+v events %d err %v", again, len(repo.outbox), err)
	}
}

func TestRecoverPending(t *testing.T) {
	repo := newMemRepo()
	u := newTestUsecase(t, repo, nil, nil)
	it, err := u.prepare(Raw{Source: "x", Kind: "announcement", Codes: []string{"600519.SH"}, Title: "贵州茅台：关于回购股份的公告", Content: buybackBody})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateItem(context.Background(), it, StatusPending, ""); err != nil {
		t.Fatal(err)
	}
	n, err := u.Recover(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("recover = %d %v", n, err)
	}
	m := repo.items[it.ID]
	if m.status != StatusScored || len(m.links) == 0 || m.links[0].Method != MethodSource {
		t.Fatalf("recovered = %+v", m)
	}
}
