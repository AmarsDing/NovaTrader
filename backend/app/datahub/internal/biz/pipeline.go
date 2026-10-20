package biz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"server/pkg/appcfg"
)

// SourceConfig 是一个源的开关和配额。
type SourceConfig struct {
	Enabled    bool
	Official   bool
	DailyQuota int64
	Timeout    time.Duration
}

// DomainConfig 是一个数据域的开关与源优先级。
type DomainConfig struct {
	Enabled  bool
	Sources  []string
	Interval time.Duration
}

// Config 是流水线配置，由 conf.Datahub 转换而来。
type Config struct {
	AllowUnofficial bool
	Sources         map[string]SourceConfig
	Domains         map[string]DomainConfig
	BreakerFailures int
	BreakerCooldown time.Duration
}

// Alerter 发切源、回切、过期等告警。实现方发 notify.request。
type Alerter interface {
	Alert(ctx context.Context, level, title, body string)
}

type sourceState struct {
	src     Source
	cfg     SourceConfig
	enabled atomic.Bool
	quota   *Quota
	// timeout 运行时单次超时盖写（纳秒，0=用 cfg.Timeout）；rate 运行时 Run 粒度节流（nil=不节流）。
	timeout atomic.Int64
	rate    atomic.Pointer[Limiter]
}

type entry struct {
	state   *sourceState
	breaker *Breaker
}

type domainState struct {
	enabled bool
	cfg     DomainConfig
	// chain 当前生效链（原子快照）；base 是 yaml 基线链副本，清除盖写时回落；
	// entriesAll 为全部支持该域的注册源各建一份持久 entry，断路器随 entry 跨换链连续。
	chain      atomic.Pointer[[]*entry]
	base       []*entry
	entriesAll map[string]*entry
}

// activeChain 返回当前生效链快照：未装盖写时是 yaml 基线。
func (ds *domainState) activeChain() []*entry {
	if ch := ds.chain.Load(); ch != nil {
		return *ch
	}
	return ds.base
}

// Pipeline 按 registry → quota → breaker → fetch → validate 的顺序调用源。
// 限频在源客户端里按单个 HTTP 请求做；写库和发事件由调用方在拿到结果后完成。
type Pipeline struct {
	allowUnofficial atomic.Bool
	sources         map[string]*sourceState
	domains         map[string]*domainState
	health          *healthBook
	alert           Alerter
	now             func() time.Time
	get             func(context.Context, string) string // 运行时键读取缝：生产为 appcfg.Get，单测注入
}

// NewPipeline 注册源。配置里写了但没注册、或不支持该域的源会被跳过，并在 warnings 里说明。
func NewPipeline(cfg Config, srcs []Source, alert Alerter) (*Pipeline, []string) {
	p := &Pipeline{
		sources: map[string]*sourceState{},
		domains: map[string]*domainState{},
		health:  newHealthBook(),
		alert:   alert,
		now:     time.Now,
		get:     appcfg.Get,
	}
	p.allowUnofficial.Store(cfg.AllowUnofficial)
	for _, s := range srcs {
		sc, ok := cfg.Sources[s.Name()]
		if !ok {
			sc = SourceConfig{Enabled: false}
		}
		st := &sourceState{src: s, cfg: sc, quota: NewQuota(sc.DailyQuota)}
		st.enabled.Store(sc.Enabled)
		p.sources[s.Name()] = st
	}
	var warnings []string
	names := make([]string, 0, len(cfg.Domains))
	for name := range cfg.Domains {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, domain := range names {
		dc := cfg.Domains[domain]
		ds := &domainState{enabled: dc.Enabled, cfg: dc, entriesAll: map[string]*entry{}}
		// 先为支持该域的全部注册源建持久 entry，运行时盖写换链只切快照不清断路器。
		for name, st := range p.sources {
			if Supports(domain, st.src) {
				ds.entriesAll[name] = &entry{state: st, breaker: NewBreaker(cfg.BreakerFailures, cfg.BreakerCooldown)}
			}
		}
		base := make([]*entry, 0, len(dc.Sources))
		for i, name := range dc.Sources {
			st, ok := p.sources[name]
			if !ok {
				warnings = append(warnings, fmt.Sprintf("%s: source %s is not registered", domain, name))
				continue
			}
			e, ok := ds.entriesAll[name]
			if !ok {
				warnings = append(warnings, fmt.Sprintf("%s: source %s does not support this domain", domain, name))
				continue
			}
			base = append(base, e)
			p.health.ensure(domain, name, i, st.cfg.Official)
			if scope, ok := st.src.(interface{ CoverageUnknown() bool }); ok && scope.CoverageUnknown() {
				p.health.unknownCoverage(domain, name)
			}
		}
		ds.base = base
		ds.chain.Store(&ds.base)
		p.domains[domain] = ds
	}
	return p, warnings
}

// DomainEnabled 判断数据域是否开启且至少有一个源。
func (p *Pipeline) DomainEnabled(domain string) bool {
	ds := p.domains[domain]
	return ds != nil && ds.enabled && len(ds.activeChain()) > 0
}

// DomainConfig 返回数据域配置。
func (p *Pipeline) DomainConfig(domain string) (DomainConfig, bool) {
	ds := p.domains[domain]
	if ds == nil {
		return DomainConfig{}, false
	}
	return ds.cfg, true
}

// SetSourceEnabled 运行时开关一个源。返回 false 表示没有这个源。
func (p *Pipeline) SetSourceEnabled(name string, on bool) bool {
	st, ok := p.sources[name]
	if !ok {
		return false
	}
	st.enabled.Store(on)
	return true
}

// SetAllowUnofficial 运行时开关全部非官方源。
func (p *Pipeline) SetAllowUnofficial(on bool) { p.allowUnofficial.Store(on) }

// Source 按名字取源，补数指定源时使用。
func (p *Pipeline) Source(name string) (Source, bool) {
	st, ok := p.sources[name]
	if !ok {
		return nil, false
	}
	return st.src, true
}

// Health 返回全部健康统计。
func (p *Pipeline) Health() []Health {
	now := p.now()
	return p.health.list(func(h *Health) {
		st := p.sources[h.Source]
		ds := p.domains[h.Domain]
		if st == nil || ds == nil {
			return
		}
		h.Enabled = st.enabled.Load() && ds.enabled
		h.QuotaUsed, h.QuotaLimit = st.quota.Usage(now)
		if e := ds.entriesAll[h.Source]; e != nil {
			h.State, h.Failures = e.breaker.State()
		}
		if !h.Enabled || (!st.cfg.Official && !p.allowUnofficial.Load()) {
			h.State = StateDisabled
		}
	})
}

// SeedHealth 用库里的记录恢复统计和当日配额。
func (p *Pipeline) SeedHealth(rows []Health, quotaDay time.Time) {
	for _, h := range rows {
		p.health.seed(h)
		if st := p.sources[h.Source]; st != nil && h.QuotaUsed > 0 {
			used, _ := st.quota.Usage(quotaDay)
			if h.QuotaUsed > used {
				st.quota.Seed(quotaDay, h.QuotaUsed)
			}
		}
	}
}

type domainKey struct{}

// DomainOf 返回 Run 放进 ctx 的数据域名，源客户端记录原始响应时使用。
func DomainOf(ctx context.Context) string {
	s, _ := ctx.Value(domainKey{}).(string)
	return s
}

// RunOptions 控制一次调用。Only 非空时只用该源（补数指定源）；Timeout 为单次请求超时上限。
type RunOptions struct {
	Only    string
	Timeout time.Duration
}

// Run 按优先级依次调用源，直到有一个成功并通过校验。返回结果和实际使用的源名。
func Run[T any](ctx context.Context, p *Pipeline, domain string, opt RunOptions,
	fetch func(ctx context.Context, s Source) (T, error), check func(T) error) (T, string, error) {
	var zero T
	ds := p.domains[domain]
	if ds == nil || !ds.enabled {
		return zero, "", fmt.Errorf("datahub: domain %s is disabled", domain)
	}
	ctx = context.WithValue(ctx, domainKey{}, domain)
	applySourceSwitches(ctx, p, p.get)
	applySourceLimits(ctx, p, p.get)
	applyDomainChains(ctx, p, p.get)
	failed := map[string]string{}
	for i, e := range ds.activeChain() {
		st := e.state
		name := st.src.Name()
		if opt.Only != "" && name != opt.Only {
			continue
		}
		if !st.enabled.Load() {
			continue
		}
		if !st.cfg.Official && !p.allowUnofficial.Load() {
			continue
		}
		now := p.now()
		if !e.breaker.Allow(now) {
			failed[name] = "断路打开"
			continue
		}
		if rate := st.rate.Load(); rate != nil {
			if werr := rate.Wait(ctx); werr != nil {
				e.breaker.Release()
				return zero, "", werr
			}
		}
		if !st.quota.Take(now) {
			e.breaker.Release()
			p.health.note(domain, name, "当日配额已用完")
			failed[name] = "当日配额已用完"
			continue
		}
		v, err := call(ctx, st, opt.Timeout, fetch)
		if errors.Is(err, ErrUnsupported) {
			e.breaker.Release()
			continue
		}
		if err == nil && check != nil {
			err = check(v)
		}
		latency := p.now().Sub(now)
		if err != nil && ctx.Err() != nil {
			e.breaker.Release()
			return zero, "", ctx.Err()
		}
		p.health.record(domain, name, latency, err, p.now())
		if tr, changed := e.breaker.Record(err == nil, p.now()); changed {
			p.notifyTransition(ctx, domain, name, i, tr, err)
		}
		if err == nil {
			return v, name, nil
		}
		failed[name] = err.Error()
	}
	return zero, "", &NoSourceError{Domain: domain, Errors: failed}
}

func applySourceSwitches(ctx context.Context, p *Pipeline, get func(context.Context, string) string) {
	if p == nil || get == nil {
		return
	}
	for name, st := range p.sources {
		switch strings.TrimSpace(get(ctx, "app.ds."+name+".on")) {
		case "true":
			st.enabled.Store(true)
		case "false":
			st.enabled.Store(false)
		}
	}
}

// applySourceLimits 读取运行时源级限频/配额/超时盖写（app.ds.<source>.qps/quota/timeout_sec）。
// 空串=清盖写回落配置文件；值在 params 层已校验，解析失败防御性保留上次生效值。
func applySourceLimits(ctx context.Context, p *Pipeline, get func(context.Context, string) string) {
	if p == nil || get == nil {
		return
	}
	for name, st := range p.sources {
		if raw := strings.TrimSpace(get(ctx, "app.ds."+name+".qps")); raw != "" {
			if qps, err := strconv.ParseFloat(raw, 64); err == nil && qps > 0 {
				rate := st.rate.Load()
				if rate == nil {
					rate = &Limiter{}
					st.rate.Store(rate)
				}
				rate.SetQPS(qps)
			}
		} else {
			st.rate.Store(nil)
		}
		if raw := strings.TrimSpace(get(ctx, "app.ds."+name+".quota")); raw != "" {
			if limit, err := strconv.ParseInt(raw, 10, 64); err == nil && limit >= 0 {
				st.quota.SetLimit(limit)
			}
		} else {
			st.quota.SetLimit(st.cfg.DailyQuota)
		}
		if raw := strings.TrimSpace(get(ctx, "app.ds."+name+".timeout_sec")); raw != "" {
			if sec, err := strconv.ParseInt(raw, 10, 64); err == nil && sec >= 1 && sec <= 120 {
				st.timeout.Store(int64(time.Duration(sec) * time.Second))
			}
		} else {
			st.timeout.Store(0)
		}
	}
}

// applyDomainChains 读取运行时域降级链盖写（app.domain.<domain>.sources，JSON 字符串数组）。
// 空串=当前链非基线则回落 yaml 基线；链中任一源未注册或不支持该域时整条不落地、保持现链。
// 换链只切原子快照：在途 Run 用头部快照走完，断路器随持久 entry 连续。
func applyDomainChains(ctx context.Context, p *Pipeline, get func(context.Context, string) string) {
	if p == nil || get == nil {
		return
	}
	for domain, ds := range p.domains {
		raw := strings.TrimSpace(get(ctx, "app.domain."+domain+".sources"))
		if raw == "" {
			if cur := ds.chain.Load(); cur == nil || !sameChain(*cur, ds.base) {
				ds.chain.Store(&ds.base)
			}
			continue
		}
		var chain []string
		if err := json.Unmarshal([]byte(raw), &chain); err != nil || len(chain) == 0 {
			continue
		}
		next := make([]*entry, 0, len(chain))
		ok := true
		for _, name := range chain {
			e, exists := ds.entriesAll[name]
			if !exists {
				ok = false
				break
			}
			next = append(next, e)
		}
		if !ok {
			continue
		}
		if sameChain(ds.activeChain(), next) {
			continue
		}
		for i, e := range next {
			p.health.ensure(domain, e.state.src.Name(), i, e.state.cfg.Official)
		}
		ds.chain.Store(&next)
	}
}

// sameChain 判断两份链快照按源名同序。
func sameChain(a, b []*entry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].state.src.Name() != b[i].state.src.Name() {
			return false
		}
	}
	return true
}

// EachSource 快照遍历已注册源：fn(name, src, enabled, cfg) 锁外调用。
// sources map 构建后不再写、enabled 是 atomic，无需加锁；cfg 为静态源配置
//（Official/Timeout 等，供巡检与 Run 门禁同语义判断）。不暴露内部 map。
func (p *Pipeline) EachSource(fn func(name string, src Source, enabled bool, cfg SourceConfig)) {
	if fn == nil {
		return
	}
	names := make([]string, 0, len(p.sources))
	for name := range p.sources {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		st := p.sources[name]
		fn(name, st.src, st.enabled.Load(), st.cfg)
	}
}

func call[T any](ctx context.Context, st *sourceState, limit time.Duration,
	fetch func(ctx context.Context, s Source) (T, error)) (v T, err error) {
	timeout := st.cfg.Timeout
	if ns := st.timeout.Load(); ns > 0 {
		timeout = time.Duration(ns)
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	if limit > 0 && limit < timeout {
		timeout = limit
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%s panic: %v", st.src.Name(), r)
		}
	}()
	return fetch(cctx, st.src)
}

func (p *Pipeline) notifyTransition(ctx context.Context, domain, source string, priority int, tr Transition, cause error) {
	if p.alert == nil {
		return
	}
	switch tr.To {
	case StateOpen:
		body := fmt.Sprintf("%s 在 %s 域连续失败，已断开并切到下一优先级源。", source, domain)
		if cause != nil {
			body += "最后错误：" + cause.Error()
		}
		level := "warn"
		if priority == 0 {
			level = "error"
		}
		p.alert.Alert(ctx, level, "数据源切换："+domain, body)
	case StateClosed:
		p.alert.Alert(ctx, "info", "数据源恢复："+domain, fmt.Sprintf("%s 探测成功，%s 域已回切。", source, domain))
	}
}
