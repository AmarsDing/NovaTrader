package data

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"time"

	"server/app/datahub/internal/biz"
	"server/conf"
	"server/pkg/secret"

	"github.com/go-kratos/kratos/v2/log"
	"google.golang.org/protobuf/types/known/durationpb"
)

const defaultPyworker = "http://127.0.0.1:18080"

// NewCollector 按配置注册源、建流水线和采集器。
func NewCollector(cfg *conf.Datahub, repo *Repo, snap *SnapshotStore, bus biz.Publisher, logger log.Logger) (*biz.Collector, error) {
	if cfg == nil {
		return nil, fmt.Errorf("data: datahub config is missing")
	}
	alert := NewAlerter(bus, logger)
	opt := sourceOpts{cfg: cfg, repo: repo, log: log.NewHelper(log.With(logger, "module", "datahub/source"))}
	if cfg.GetRawEnabled() {
		opt.raw = func(ctx context.Context, r biz.RawRecord) {
			go func() { _ = repo.SaveRaw(context.WithoutCancel(ctx), r) }()
		}
	}
	srcs := opt.build()
	p, warnings := biz.NewPipeline(toBizConfig(cfg), srcs, alert)
	for _, w := range warnings {
		opt.log.Warn(w)
	}
	if rows, err := repo.LoadHealth(context.Background()); err == nil {
		p.SeedHealth(rows, time.Now())
	}
	col := biz.NewCollector(p, repo, snap, bus, nil, alert, biz.Options{
		StaleAfter:      dur(cfg.GetSnapshot().GetStaleAfter(), 10*time.Second),
		SnapshotTimeout: dur(cfg.GetSnapshot().GetTimeout(), 3*time.Second),
	}, logger)
	return col, nil
}

type sourceOpts struct {
	cfg  *conf.Datahub
	repo *Repo
	log  *log.Helper
	raw  RawSink
}

func (o sourceOpts) src(name string) *conf.DataSource {
	if o.cfg.GetSources() == nil {
		return nil
	}
	return o.cfg.GetSources()[name]
}

func (o sourceOpts) httpOpt(name string) HTTPOptions {
	s := o.src(name)
	opt := HTTPOptions{Raw: o.raw, Concurrency: 4}
	if s != nil {
		opt.QPS = s.GetQps()
		if s.GetConcurrency() > 0 {
			opt.Concurrency = int(s.GetConcurrency())
		}
	}
	return opt
}

func (o sourceOpts) url(name string) string {
	if s := o.src(name); s != nil && s.GetUrl() != "" {
		return s.GetUrl()
	}
	if s := o.src(biz.SourceTdxFile); s != nil && s.GetUrl() != "" {
		return s.GetUrl()
	}
	return defaultPyworker
}

func (o sourceOpts) token(name string) string {
	s := o.src(name)
	if s == nil {
		return ""
	}
	v, err := resolveToken(s.GetToken(), o.cfg.GetSecretKeyFile())
	if err != nil {
		o.log.Warnf("%s token: %v", name, err)
		return ""
	}
	return v
}

func (o sourceOpts) feeds(name string) []string {
	if s := o.src(name); s != nil {
		return s.GetFeeds()
	}
	return nil
}

func (o sourceOpts) build() []biz.Source {
	universe := func(ctx context.Context) ([]string, error) {
		return o.repo.ActiveSymbols(ctx, time.Now())
	}
	return []biz.Source{
		NewTushare("", o.token(biz.SourceTushare), o.httpOpt(biz.SourceTushare)),
		NewTdxLocal(o.url(biz.SourceTdxLocal), o.httpOpt(biz.SourceTdxLocal)),
		NewTdxFile(o.url(biz.SourceTdxFile), o.httpOpt(biz.SourceTdxFile)),
		NewTdxTCP(o.url(biz.SourceTdxTCP), o.httpOpt(biz.SourceTdxTCP)),
		NewAkshare(o.url(biz.SourceAkshare), o.httpOpt(biz.SourceAkshare)),
		NewEastmoney(o.httpOpt(biz.SourceEastmoney)),
		NewSina(o.httpOpt(biz.SourceSina), universe),
		NewTencent(o.httpOpt(biz.SourceTencent), universe),
		NewCninfo(o.httpOpt(biz.SourceCninfo)),
		NewTavily(o.token(biz.SourceTavily), o.feeds(biz.SourceTavily), o.httpOpt(biz.SourceTavily)),
		NewRSS(o.feeds(biz.SourceRSS), o.httpOpt(biz.SourceRSS)),
	}
}

func toBizConfig(cfg *conf.Datahub) biz.Config {
	out := biz.Config{
		AllowUnofficial: cfg.GetAllowUnofficial(),
		Sources:         map[string]biz.SourceConfig{},
		Domains:         map[string]biz.DomainConfig{},
		BreakerFailures: int(cfg.GetBreakerFailures()),
		BreakerCooldown: dur(cfg.GetBreakerCooldown(), time.Minute),
	}
	for name, s := range cfg.GetSources() {
		sc := biz.SourceConfig{Enabled: s.GetEnabled(), Official: s.GetOfficial(), DailyQuota: s.GetDailyQuota(), Timeout: dur(s.GetTimeout(), 10*time.Second)}
		out.Sources[name] = sc
	}
	for name, d := range cfg.GetDomains() {
		out.Domains[name] = biz.DomainConfig{Enabled: d.GetEnabled(), Sources: d.GetSources(), Interval: dur(d.GetInterval(), 0)}
	}
	return out
}

func dur(p *durationpb.Duration, def time.Duration) time.Duration {
	if p == nil {
		return def
	}
	if v := p.AsDuration(); v > 0 {
		return v
	}
	return def
}

func resolveToken(raw, keyFile string) (string, error) {
	raw = strings.TrimSpace(raw)
	switch {
	case raw == "":
		return "", nil
	case strings.HasPrefix(raw, "env:"):
		return os.Getenv(strings.TrimPrefix(raw, "env:")), nil
	case strings.HasPrefix(raw, "enc:"):
		if keyFile == "" {
			return "", fmt.Errorf("enc: token needs secret_key_file")
		}
		key, err := secret.LoadKey(keyFile)
		if err != nil {
			return "", err
		}
		b, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(raw, "enc:"))
		if err != nil {
			return "", err
		}
		plain, err := secret.Decrypt(key, b)
		if err != nil {
			return "", err
		}
		return string(plain), nil
	default:
		return raw, nil
	}
}
