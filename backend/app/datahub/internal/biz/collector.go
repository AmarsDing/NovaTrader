package biz

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"server/pkg/events"
	"server/pkg/metrics"
	"server/pkg/tradecal"

	"github.com/go-kratos/kratos/v2/log"
)

const serviceName = "datahub"

var (
	snapRounds   = metrics.Counter("datahub_snapshot_rounds_total", "snapshot rounds written to redis")
	snapFailures = metrics.Counter("datahub_snapshot_failures_total", "snapshot rounds with no usable source")
	snapLatency  = metrics.Gauge("datahub_snapshot_latency_ms", "last snapshot round latency in milliseconds")
	snapLag      = metrics.Gauge("datahub_snapshot_lag_ms", "now minus newest quote time of last round")
	snapCount    = metrics.Gauge("datahub_snapshot_count", "symbols in last snapshot round")
)

// Options 是采集参数，零值用默认值。
type Options struct {
	StaleAfter       time.Duration
	SnapshotTimeout  time.Duration
	MinSnapshotCount int
	// MaxBarPct 相对前收的涨跌幅超过它记异常值，默认 0.31（北交所 30% 加容差）
	MaxBarPct float64
	// CompletenessWarn 覆盖率低于它记完整率问题，默认 0.999
	CompletenessWarn float64
	// CompletenessFail 覆盖率低于它认为该源结果不可用，换下一源，默认 0.5
	CompletenessFail float64
}

func (o *Options) defaults() {
	if o.StaleAfter <= 0 {
		o.StaleAfter = 10 * time.Second
	}
	if o.SnapshotTimeout <= 0 {
		o.SnapshotTimeout = 3 * time.Second
	}
	if o.MinSnapshotCount <= 0 {
		o.MinSnapshotCount = 3000
	}
	if o.MaxBarPct <= 0 {
		o.MaxBarPct = 0.31
	}
	if o.CompletenessWarn <= 0 {
		o.CompletenessWarn = 0.999
	}
	if o.CompletenessFail <= 0 {
		o.CompletenessFail = 0.5
	}
}

// Collector 是全部采集用例。
type Collector struct {
	p     *Pipeline
	repo  Repo
	snap  SnapshotStore
	bus   Publisher
	cal   *tradecal.Calendar
	alert Alerter
	opt   Options
	log   *log.Helper
	now   func() time.Time

	seq       atomic.Int64
	staleOnce sync.Map
}

func NewCollector(p *Pipeline, repo Repo, snap SnapshotStore, bus Publisher, cal *tradecal.Calendar,
	alert Alerter, opt Options, logger log.Logger) *Collector {
	opt.defaults()
	if cal == nil {
		cal = tradecal.Default
	}
	return &Collector{p: p, repo: repo, snap: snap, bus: bus, cal: cal, alert: alert, opt: opt,
		log: log.NewHelper(log.With(logger, "module", "datahub/biz")), now: time.Now}
}

// Pipeline 返回底层流水线，供服务层查询健康和开关源。
func (c *Collector) Pipeline() *Pipeline { return c.p }

// Result 是一次采集的结果。
type Result struct {
	Domain  string
	Source  string
	Rows    int
	Stale   bool
	Message string
}

// CollectSnapshot 采一轮全市场快照写 Redis，发 market.snapshot。FR-01-04。
// 只在集合竞价和连续竞价时段调用；调用方用 Session 判断。
func (c *Collector) CollectSnapshot(ctx context.Context) (SnapshotMeta, error) {
	start := c.now()
	sess, err := c.cal.SessionAt(start)
	if err != nil {
		return SnapshotMeta{}, err
	}
	rules := SnapshotRules{Now: start, StaleAfter: c.opt.StaleAfter, Trading: InQuoteSession(sess), MinCount: c.opt.MinSnapshotCount}
	var issues []Issue
	var newest time.Time
	batch, src, err := Run(ctx, c.p, DomainSnapshot, RunOptions{Timeout: c.opt.SnapshotTimeout},
		func(ctx context.Context, s Source) (*SnapshotBatch, error) {
			b, err := s.(SnapshotSource).Snapshots(ctx, nil)
			return &b, err
		},
		func(b *SnapshotBatch) error {
			clean, iss, err := CheckSnapshots(b.Items, rules)
			b.Items = clean
			issues = iss
			return err
		})
	if err != nil {
		snapFailures.Inc()
		meta, _ := c.snap.Meta(ctx)
		if !meta.Stale {
			meta.Stale = true
			_ = c.snap.MarkStale(ctx, meta)
		}
		c.alertOnce(ctx, "snapshot", "error", "盘中快照不可用", "所有快照源都失败或数据不是最新，snap:meta 已标记 stale，M08 将拒绝自动单。"+err.Error())
		return meta, err
	}
	c.staleOnce.Delete("snapshot")
	asOf := c.now()
	for i := range batch.Items {
		batch.Items[i].AsOf = asOf
		batch.Items[i].Source = src
		batch.Items[i].Stale = false
		if batch.Items[i].Time.After(newest) {
			newest = batch.Items[i].Time
		}
	}
	meta := SnapshotMeta{AsOf: asOf, Source: src, Count: len(batch.Items), LatencyMs: asOf.Sub(start).Milliseconds(), Seq: c.seq.Add(1)}
	if err := c.snap.WriteSnapshots(ctx, batch.Items, meta); err != nil {
		return meta, fmt.Errorf("datahub: write snapshots: %w", err)
	}
	snapRounds.Inc()
	snapLatency.Set(float64(meta.LatencyMs))
	snapCount.Set(float64(meta.Count))
	if !newest.IsZero() {
		snapLag.Set(float64(asOf.Sub(newest).Milliseconds()))
	}
	c.publish(ctx, events.SubjectMarketSnapshot, meta)
	c.saveIssues(ctx, DomainSnapshot, src, dateOf(start), issues)
	return meta, nil
}

// InQuoteSession 判断这个时段是否有实时行情：集合竞价、竞价撮合、连续竞价。
func InQuoteSession(s tradecal.Session) bool {
	return s.Trading || s.Phase == tradecal.CallAuction || s.Phase == tradecal.PreMatch
}

// CollectSectorQuotes 采板块实时行情写 Redis snap:sector。
func (c *Collector) CollectSectorQuotes(ctx context.Context) (Result, error) {
	items, src, err := Run(ctx, c.p, DomainSectorQuote, RunOptions{},
		func(ctx context.Context, s Source) ([]SectorQuote, error) {
			return s.(SectorQuoteSource).SectorQuotes(ctx)
		}, nonEmpty[SectorQuote]("板块行情"))
	if err != nil {
		return Result{Domain: DomainSectorQuote}, err
	}
	now := c.now()
	for i := range items {
		items[i].AsOf = now
		items[i].Source = src
	}
	if err := c.snap.WriteSectorQuotes(ctx, items); err != nil {
		return Result{}, err
	}
	return Result{Domain: DomainSectorQuote, Source: src, Rows: len(items)}, nil
}

// CollectIntradayFlow 采盘中个股主力资金写 Redis snap:flow。
func (c *Collector) CollectIntradayFlow(ctx context.Context) (Result, error) {
	items, src, err := Run(ctx, c.p, DomainMoneyFlow, RunOptions{},
		func(ctx context.Context, s Source) ([]IntradayFlow, error) {
			fs, ok := s.(IntradayFlowSource)
			if !ok {
				return nil, ErrUnsupported
			}
			return fs.IntradayFlows(ctx)
		}, nonEmpty[IntradayFlow]("盘中资金"))
	if err != nil {
		return Result{Domain: DomainMoneyFlow}, err
	}
	now := c.now()
	for i := range items {
		items[i].AsOf = now
		items[i].Source = src
	}
	if err := c.snap.WriteIntradayFlows(ctx, items); err != nil {
		return Result{}, err
	}
	return Result{Domain: DomainMoneyFlow, Source: src, Rows: len(items)}, nil
}

func nonEmpty[T any](what string) func([]T) error {
	return func(v []T) error {
		if len(v) == 0 {
			return fmt.Errorf("%s为空", what)
		}
		return nil
	}
}

// publish 直接发事件。快照元信息走普通 NATS；失败只记日志，下游会按间隔自己读 Redis。
func (c *Collector) publish(ctx context.Context, subject string, payload any) {
	if c.bus == nil {
		return
	}
	env, err := events.New(serviceName, subject, events.TraceID(ctx), payload)
	if err != nil {
		c.log.Warnf("event %s: %v", subject, err)
		return
	}
	if err := c.bus.Publish(ctx, env); err != nil {
		c.log.Warnf("publish %s: %v", subject, err)
	}
}

// alertOnce 对持续性故障只告警一次，恢复后（staleOnce 被删）才会再告警。
func (c *Collector) alertOnce(ctx context.Context, key, level, title, body string) {
	if _, loaded := c.staleOnce.LoadOrStore(key, true); loaded {
		return
	}
	if c.alert != nil {
		c.alert.Alert(ctx, level, title, body)
	}
}

func (c *Collector) saveIssues(ctx context.Context, domain, source string, day time.Time, issues []Issue) {
	if len(issues) == 0 {
		return
	}
	for i := range issues {
		if issues[i].Domain == "" {
			issues[i].Domain = domain
		}
		if issues[i].Source == "" {
			issues[i].Source = source
		}
		if issues[i].TradeDate.IsZero() {
			issues[i].TradeDate = day
		}
		if issues[i].Severity == "" {
			issues[i].Severity = "warn"
		}
	}
	if err := c.repo.SaveIssues(ctx, issues); err != nil {
		c.log.Warnf("save %d issues: %v", len(issues), err)
	}
	errorsFound := 0
	for _, is := range issues {
		if is.Severity == "error" || is.Symbol == "" {
			errorsFound++
		}
	}
	if errorsFound > 0 {
		c.publish(ctx, events.SubjectMDQuality, map[string]any{"domain": domain, "source": source,
			"trade_date": day.Format("2006-01-02"), "issues": len(issues), "summary": firstMessages(issues, 3)})
	}
}

func firstMessages(issues []Issue, n int) string {
	var parts []string
	for _, is := range issues {
		if len(parts) >= n {
			break
		}
		if is.Symbol != "" {
			parts = append(parts, is.Symbol+" "+is.Message)
		} else {
			parts = append(parts, is.Message)
		}
	}
	return strings.Join(parts, "；")
}

// staleReason 从流水线错误里找出「不是最新」的原因。
func staleReason(err error) (string, bool) {
	var se *StaleError
	if errors.As(err, &se) {
		return se.Error(), true
	}
	var ns *NoSourceError
	if errors.As(err, &ns) {
		for src, msg := range ns.Errors {
			if strings.Contains(msg, "不是最新") {
				return src + "：" + msg, true
			}
		}
	}
	return "", false
}

func ready(domain string, day time.Time, n int, source string, stale bool) Ready {
	d := ""
	if !day.IsZero() {
		d = day.In(shanghai()).Format("2006-01-02")
	}
	return Ready{Domain: domain, TradeDate: d, Count: n, Source: source, Stale: stale}
}
