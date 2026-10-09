package biz

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"server/conf"
	"server/pkg/events"
	"server/pkg/metrics"
	"server/pkg/symbol"
	"server/pkg/tradecal"

	"github.com/go-kratos/kratos/v2/log"
)

const eventSource = "intel"

var (
	mIngested   = metrics.Counter("novatrader_intel_ingested_total", "intel items received")
	mDuplicate  = metrics.Counter("novatrader_intel_duplicate_total", "exact duplicates dropped")
	mNearDup    = metrics.Counter("novatrader_intel_near_duplicate_total", "items merged into an existing cluster")
	mFiltered   = metrics.Counter("novatrader_intel_filtered_total", "low quality items filtered")
	mScored     = metrics.Counter("novatrader_intel_scored_total", "items scored")
	mDegraded   = metrics.Counter("novatrader_intel_degraded_total", "cluster heads scored by rules only")
	mAlerts     = metrics.Counter("novatrader_intel_alert_total", "intel.alert decided")
	mLLMFailure = metrics.Counter("novatrader_intel_llm_failure_total", "model scoring failures")
)

// Config 是 conf.Intel 补上默认值后的结果。
type Config struct {
	LinkThreshold   float64
	AlertImportance int
	AlertShift      float64
	DedupWindow     time.Duration
	DedupHamming    int
	BlockedSources  []string
	AdWords         []string
}

func ConfigFrom(c *conf.Intel) Config {
	out := Config{LinkThreshold: 0.6, AlertImportance: 4, AlertShift: 0.5, DedupWindow: 48 * time.Hour, DedupHamming: 6}
	if c == nil {
		return out
	}
	if c.LinkThreshold > 0 {
		out.LinkThreshold = c.LinkThreshold
	}
	if c.AlertImportance > 0 {
		out.AlertImportance = int(c.AlertImportance)
	}
	if c.AlertShift > 0 {
		out.AlertShift = c.AlertShift
	}
	if c.DedupWindow != nil && c.DedupWindow.AsDuration() > 0 {
		out.DedupWindow = c.DedupWindow.AsDuration()
	}
	if c.DedupHamming > 0 {
		out.DedupHamming = int(c.DedupHamming)
	}
	out.BlockedSources = c.BlockedSources
	out.AdWords = c.AdKeywords
	return out
}

// Result 是一次录入的结果。
type Result struct {
	NewsID    int
	ClusterID int
	Status    string
	Reason    string
}

// Usecase 是情报流水线。
type Usecase struct {
	repo   Repo
	cfg    Config
	llm    *LLMScorer
	filter *Filter
	index  *Index
	dict   atomic.Pointer[Dictionary]
	admit  sync.Mutex
	log    *log.Helper
	Now    func() time.Time
}

func NewUsecase(repo Repo, model Model, c *conf.Intel, logger log.Logger) *Usecase {
	cfg := ConfigFrom(c)
	u := &Usecase{
		repo:   repo,
		cfg:    cfg,
		filter: NewFilter(cfg.BlockedSources, cfg.AdWords),
		index:  NewIndex(cfg.DedupWindow, cfg.DedupHamming),
		log:    log.NewHelper(log.With(logger, "module", "intel/biz")),
		Now:    time.Now,
	}
	if l := c.GetLlm(); l.GetEnabled() && model != nil {
		u.llm = NewLLMScorer(model, l.GetSmallModel() != "", l.GetLargeModel() != "", int(l.GetReviewImportance()))
	}
	u.dict.Store(BuildDictionary(nil, nil))
	return u
}

// Warm 预热去重索引和关联词典，服务启动时调用。
func (u *Usecase) Warm(ctx context.Context) error {
	now := u.Now()
	fps, err := u.repo.Fingerprints(ctx, now.Add(-u.cfg.DedupWindow))
	if err != nil {
		return err
	}
	for _, fp := range fps {
		u.index.Add(fp, now)
	}
	_, err = u.ReloadDictionary(ctx)
	return err
}

// ReloadDictionary 从 stock_basic 与 stock_alias 重建词典。
func (u *Usecase) ReloadDictionary(ctx context.Context) (*Dictionary, error) {
	stocks, aliases, err := u.repo.Dictionary(ctx)
	if err != nil {
		return nil, err
	}
	d := BuildDictionary(stocks, aliases)
	u.dict.Store(d)
	return d, nil
}

// Process 走完整条流水线。参数错误返回 error；重复、过滤用 Result.Status 表达。
func (u *Usecase) Process(ctx context.Context, raw Raw) (Result, error) {
	mIngested.Inc()
	it, err := u.prepare(raw)
	if err != nil {
		return Result{}, err
	}
	res, done, err := u.admitItem(ctx, it)
	if err != nil || done {
		return res, err
	}
	if err := u.scoreAndSave(ctx, it); err != nil {
		return Result{}, err
	}
	res = Result{NewsID: it.ID, ClusterID: it.ClusterID, Status: ResultScored}
	if !it.Head {
		res.Status = ResultNearDuplicate
	}
	return res, nil
}

// ErrInvalid 表示录入参数不合法，消费端不应重试。
var ErrInvalid = errors.New("intel: invalid item")

func (u *Usecase) prepare(raw Raw) (*Item, error) {
	now := u.Now()
	kind := strings.ToLower(strings.TrimSpace(raw.Kind))
	if !kinds[kind] {
		kind = KindNews
	}
	it := &Item{
		Source:     strings.TrimSpace(raw.Source),
		SourceID:   strings.TrimSpace(raw.SourceID),
		Kind:       kind,
		Title:      Normalize(raw.Title),
		Content:    Normalize(raw.Content),
		URL:        strings.TrimSpace(raw.URL),
		ReceivedAt: now,
	}
	if it.Source == "" {
		return nil, fmt.Errorf("%w: source is empty", ErrInvalid)
	}
	if it.Title == "" && it.Content == "" {
		return nil, fmt.Errorf("%w: title and content are empty", ErrInvalid)
	}
	if t, ok := parseTime(raw.PublishTime); ok {
		it.PublishTime = t
	} else {
		it.PublishTime, it.TimeGuessed = now, true
	}
	for _, c := range raw.Codes {
		sym, err := symbol.Parse(c)
		if err != nil {
			continue
		}
		it.Codes = appendUnique(it.Codes, sym.Tongdaxin())
	}
	it.Hash = ContentHash(it.Title, it.Content)
	it.SimHash, it.RuneLen = SimHash(it.Title + it.Content)
	return it, nil
}

func parseTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, tradecal.Shanghai()); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// admitItem 串行完成去重和归簇，并先落一条记录。done 为真表示流程到此结束。
func (u *Usecase) admitItem(ctx context.Context, it *Item) (Result, bool, error) {
	u.admit.Lock()
	defer u.admit.Unlock()
	found, status, err := u.repo.Find(ctx, it.Source, it.SourceID, it.Hash)
	if err != nil {
		return Result{}, true, err
	}
	if found != nil {
		if status == StatusPending {
			*it = *found
			return Result{}, false, nil
		}
		mDuplicate.Inc()
		return Result{Status: ResultDuplicate, Reason: "内容重复"}, true, nil
	}
	if reason := u.filter.Check(it); reason != "" {
		err := u.repo.CreateItem(ctx, it, StatusFiltered, reason)
		if errors.Is(err, ErrDuplicate) {
			mDuplicate.Inc()
			return Result{Status: ResultDuplicate, Reason: "内容重复"}, true, nil
		}
		if err != nil {
			return Result{}, true, err
		}
		mFiltered.Inc()
		return Result{NewsID: it.ID, Status: ResultFiltered, Reason: reason}, true, nil
	}
	now := u.Now()
	it.ClusterID = u.index.Find(it.SimHash, it.RuneLen, it.Codes, now)
	if err := u.repo.CreateItem(ctx, it, StatusPending, ""); err != nil {
		if errors.Is(err, ErrDuplicate) {
			found, status, ferr := u.repo.Find(ctx, it.Source, it.SourceID, it.Hash)
			if ferr != nil {
				return Result{}, true, ferr
			}
			if found != nil && status == StatusPending {
				*it = *found
				return Result{}, false, nil
			}
			mDuplicate.Inc()
			return Result{Status: ResultDuplicate, Reason: "内容重复"}, true, nil
		}
		return Result{}, true, err
	}
	if !it.Head {
		mNearDup.Inc()
	}
	u.index.Add(Fingerprint{SimHash: it.SimHash, ClusterID: it.ClusterID, RuneLen: it.RuneLen, Codes: it.Codes, At: now}, now)
	return Result{}, false, nil
}

// Recover 重新评分上次没做完的 pending 条目。
func (u *Usecase) Recover(ctx context.Context) (int, error) {
	n := 0
	for {
		items, err := u.repo.Pending(ctx, 100)
		if err != nil {
			return n, err
		}
		if len(items) == 0 {
			return n, nil
		}
		for _, it := range items {
			if err := u.scoreAndSave(ctx, it); err != nil {
				return n, err
			}
			n++
		}
	}
}

// ScoredEvent 是 intel.news.scored 的载荷。
type ScoredEvent struct {
	NewsID          int         `json:"news_id"`
	ClusterID       int         `json:"cluster_id"`
	Kind            string      `json:"kind"`
	EventType       string      `json:"event_type"`
	Title           string      `json:"title"`
	PublishTime     time.Time   `json:"publish_time"`
	Sentiment       float64     `json:"sentiment"`
	Importance      int         `json:"importance"`
	HalfLifeMinutes int         `json:"half_life_minutes"`
	Scorer          string      `json:"scorer"`
	Degraded        bool        `json:"degraded"`
	Links           []EventLink `json:"links"`
}

type EventLink struct {
	Type       string  `json:"type"`
	Target     string  `json:"target"`
	Confidence float64 `json:"confidence"`
}

func (u *Usecase) scoreAndSave(ctx context.Context, it *Item) error {
	links := u.dict.Load().LinkItem(it, u.cfg.LinkThreshold)
	s := RuleScore(it)
	if it.Head {
		s = u.modelScore(ctx, it, s)
	}
	out := &Scored{Item: it, Score: s, Links: links, Stock: topStock(links)}
	if it.Kind == KindAnnouncement || it.Kind == KindReport {
		out.Facts = ExtractFacts(it.Content)
	}
	if it.Head {
		if err := u.headEvents(ctx, it, s, links, out); err != nil {
			return err
		}
	}
	if err := u.repo.SaveScored(ctx, out); err != nil {
		return err
	}
	mScored.Inc()
	return nil
}

func (u *Usecase) modelScore(ctx context.Context, it *Item, rule Score) Score {
	if u.llm == nil {
		rule.Degraded = true
		mDegraded.Inc()
		return rule
	}
	got, err := u.llm.Score(ctx, it, rule)
	if err != nil {
		mLLMFailure.Inc()
		mDegraded.Inc()
		u.log.Warnf("model scoring failed, keep rule score: news=%d err=%v", it.ID, err)
		rule.Degraded = true
		rule.Reason += "；模型不可用"
		return rule
	}
	return got
}

func (u *Usecase) headEvents(ctx context.Context, it *Item, s Score, links []Link, out *Scored) error {
	now := u.Now()
	var shifts []Shift
	for _, l := range links {
		if l.TargetType != TargetStock {
			continue
		}
		inputs, err := u.repo.SentimentInputs(ctx, l.Target, now.Add(-CompositeWindow))
		if err != nil {
			return err
		}
		before := Composite(inputs, now)
		after := Composite(append(inputs, SentimentInput{
			Sentiment: s.Sentiment, Importance: s.Importance, HalfLifeMinutes: s.HalfLifeMinutes,
			PublishTime: it.PublishTime, Confidence: l.Confidence,
		}), now)
		shifts = append(shifts, Shift{Stock: l.Target, Before: before, After: after})
	}
	trace := events.TraceID(ctx)
	ev := ScoredEvent{
		NewsID: it.ID, ClusterID: it.ClusterID, Kind: it.Kind, EventType: s.EventType, Title: it.Title,
		PublishTime: it.PublishTime, Sentiment: s.Sentiment, Importance: s.Importance,
		HalfLifeMinutes: s.HalfLifeMinutes, Scorer: s.Scorer, Degraded: s.Degraded,
		Links: make([]EventLink, 0, len(links)),
	}
	for _, l := range links {
		ev.Links = append(ev.Links, EventLink{Type: l.TargetType, Target: l.Target, Confidence: l.Confidence})
	}
	env, err := events.New(eventSource, events.SubjectIntelScored, trace, ev)
	if err != nil {
		return err
	}
	out.Event = &env
	if a := DecideAlert(it, s, links, shifts, u.cfg.AlertImportance, u.cfg.AlertShift); a != nil {
		alert, err := events.New(eventSource, events.SubjectIntelAlert, trace, a)
		if err != nil {
			return err
		}
		out.Alert = &alert
		mAlerts.Inc()
	}
	return nil
}

// GetItem 返回条目详情，附当前有效情感。
func (u *Usecase) GetItem(ctx context.Context, id int) (*ItemView, error) {
	v, err := u.repo.GetItem(ctx, id)
	if err != nil {
		return nil, err
	}
	v.Effective = round4(Effective(v.Sentiment, v.HalfLifeMinutes, v.PublishTime, u.Now()))
	return v, nil
}

// Timeline 返回个股时间线和近 72 小时综合情感。
func (u *Usecase) Timeline(ctx context.Context, code string, since, until time.Time, limit int) (string, float64, []*ItemView, error) {
	sym, err := symbol.Parse(code)
	if err != nil {
		return "", 0, nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	code = sym.Tongdaxin()
	now := u.Now()
	if until.IsZero() {
		until = now
	}
	if since.IsZero() {
		since = until.Add(-CompositeWindow)
	}
	if limit <= 0 {
		limit = 50
	}
	limit = min(limit, 500)
	items, err := u.repo.Timeline(ctx, code, since, until, limit)
	if err != nil {
		return "", 0, nil, err
	}
	for _, v := range items {
		v.Effective = round4(Effective(v.Sentiment, v.HalfLifeMinutes, v.PublishTime, now))
	}
	inputs, err := u.repo.SentimentInputs(ctx, code, now.Add(-CompositeWindow))
	if err != nil {
		return "", 0, nil, err
	}
	return code, Composite(inputs, now), items, nil
}

// HotWords 比较本窗口与上一窗口。kind 为空时不按类型过滤。
func (u *Usecase) HotWords(ctx context.Context, window time.Duration, limit int, kind string) ([]HotWord, error) {
	if window <= 0 {
		window = 2 * time.Hour
	}
	if limit <= 0 {
		limit = 20
	}
	now := u.Now()
	cur, err := u.repo.HotCounts(ctx, now.Add(-window), now)
	if err != nil {
		return nil, err
	}
	prev, err := u.repo.HotCounts(ctx, now.Add(-2*window), now.Add(-window))
	if err != nil {
		return nil, err
	}
	if kind != "" {
		for k := range cur {
			if k[0] != kind {
				delete(cur, k)
			}
		}
	}
	return RankHotWords(cur, prev, limit), nil
}

// UpsertAlias 维护人工别名，并立即刷新词典。
func (u *Usecase) UpsertAlias(ctx context.Context, a AliasInput) (int, error) {
	a.Alias = strings.TrimSpace(foldWidth(a.Alias))
	if a.Alias == "" {
		return 0, fmt.Errorf("%w: alias is empty", ErrInvalid)
	}
	sym, err := symbol.Parse(a.StockCode)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	a.StockCode = sym.Tongdaxin()
	if a.Kind == "" {
		a.Kind = "manual"
	}
	if a.Confidence <= 0 || a.Confidence > 1 {
		a.Confidence = 0.8
	}
	id, err := u.repo.UpsertAlias(ctx, a)
	if err != nil {
		return 0, err
	}
	if _, err := u.ReloadDictionary(ctx); err != nil {
		return id, err
	}
	return id, nil
}
