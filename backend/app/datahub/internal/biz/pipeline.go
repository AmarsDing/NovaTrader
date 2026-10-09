package biz

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync/atomic"
	"time"
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
}

type entry struct {
	state    *sourceState
	breaker  *Breaker
	priority int
}

type domainState struct {
	enabled bool
	entries []*entry
	cfg     DomainConfig
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
}

// NewPipeline 注册源。配置里写了但没注册、或不支持该域的源会被跳过，并在 warnings 里说明。
func NewPipeline(cfg Config, srcs []Source, alert Alerter) (*Pipeline, []string) {
	p := &Pipeline{
		sources: map[string]*sourceState{},
		domains: map[string]*domainState{},
		health:  newHealthBook(),
		alert:   alert,
		now:     time.Now,
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
		ds := &domainState{enabled: dc.Enabled, cfg: dc}
		for i, name := range dc.Sources {
			st, ok := p.sources[name]
			if !ok {
				warnings = append(warnings, fmt.Sprintf("%s: source %s is not registered", domain, name))
				continue
			}
			if !Supports(domain, st.src) {
				warnings = append(warnings, fmt.Sprintf("%s: source %s does not support this domain", domain, name))
				continue
			}
			ds.entries = append(ds.entries, &entry{state: st, breaker: NewBreaker(cfg.BreakerFailures, cfg.BreakerCooldown), priority: i})
			p.health.ensure(domain, name, i, st.cfg.Official)
		}
		p.domains[domain] = ds
	}
	return p, warnings
}

// DomainEnabled 判断数据域是否开启且至少有一个源。
func (p *Pipeline) DomainEnabled(domain string) bool {
	ds := p.domains[domain]
	return ds != nil && ds.enabled && len(ds.entries) > 0
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
		for _, e := range ds.entries {
			if e.state == st {
				h.State, h.Failures = e.breaker.State()
			}
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
	failed := map[string]string{}
	for _, e := range ds.entries {
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
			p.notifyTransition(ctx, domain, name, e.priority, tr, err)
		}
		if err == nil {
			return v, name, nil
		}
		failed[name] = err.Error()
	}
	return zero, "", &NoSourceError{Domain: domain, Errors: failed}
}

func call[T any](ctx context.Context, st *sourceState, limit time.Duration,
	fetch func(ctx context.Context, s Source) (T, error)) (v T, err error) {
	timeout := st.cfg.Timeout
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
