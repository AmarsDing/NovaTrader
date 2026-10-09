// Package biz 是 M06 选股与信号的业务编排：漏斗、AI 精筛、价格、信号状态、冷却、卖出、后验。
// 规则本身在 pkg/rules，和回测共用；这里只负责取数、调 M05、落库和发事件。
package biz

import (
	"errors"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"server/conf"
	"server/pkg/rules"
	"server/pkg/tradecal"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
)

var ProviderSet = wire.NewSet(NewSettings, NewUsecase)

// ErrBadTransition 表示状态迁移不合法。
var ErrBadTransition = errors.New("strategy: illegal status transition")

// Settings 是 conf.Strategy 补上默认值后的运行参数。
type Settings struct {
	Book             string
	ScanInterval     time.Duration
	FullScanInterval time.Duration
	AIConcurrency    int
	AITimeout        time.Duration
	BuyCutoff        int // 当天第几分钟以后不出买入信号
}

func NewSettings(c *conf.Strategy) Settings {
	s := Settings{
		Book:             "paper",
		ScanInterval:     time.Minute,
		FullScanInterval: 10 * time.Minute,
		AIConcurrency:    4,
		AITimeout:        30 * time.Second,
		BuyCutoff:        14*60 + 50,
	}
	if c == nil {
		return s
	}
	if c.GetBook() != "" {
		s.Book = c.GetBook()
	}
	if d := c.GetScanInterval().AsDuration(); d > 0 {
		s.ScanInterval = d
	}
	if d := c.GetFullScanInterval().AsDuration(); d > 0 {
		s.FullScanInterval = d
	}
	if n := c.GetAiConcurrency(); n > 0 {
		s.AIConcurrency = int(n)
	}
	if d := c.GetAiTimeout().AsDuration(); d > 0 {
		s.AITimeout = d
	}
	if m, ok := parseClock(c.GetBuyCutoff()); ok {
		s.BuyCutoff = m
	}
	return s
}

func parseClock(s string) (int, bool) {
	h, m, ok := strings.Cut(strings.TrimSpace(s), ":")
	if !ok {
		return 0, false
	}
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, false
	}
	return hh*60 + mm, true
}

// Params 是一次扫描用的选股参数，从 strategy_config 读取，缺省用 pkg/rules 的默认值。
type Params struct {
	Funnel       rules.FunnelParams
	Fuse         rules.FuseParams
	Price        rules.PriceParams
	Strategies   []rules.Strategy
	AITopN       int
	MaxDaily     int
	TTL          time.Duration
	BuyCooldown  time.Duration
	SellCooldown time.Duration
	VersionID    *int
}

// LoadParams 把参数表的字符串值转成 Params。解析失败的键忽略并用默认值。
func LoadParams(values map[string]string, version *int) Params {
	get := func(key string) (float64, bool) {
		raw, ok := values[key]
		if !ok {
			return 0, false
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		return v, err == nil
	}
	num := func(key string, def float64) float64 {
		if v, ok := get(key); ok {
			return v
		}
		return def
	}
	minutes := func(key string, def float64) time.Duration {
		return time.Duration(num(key, def) * float64(time.Minute))
	}
	return Params{
		Funnel:       rules.LoadFunnel(get),
		Fuse:         rules.LoadFuse(get),
		Price:        rules.LoadPrice(get),
		Strategies:   rules.Templates(get),
		AITopN:       int(num("m06.ai_top_n", 30)),
		MaxDaily:     int(num("m06.max_daily_signals", 5)),
		TTL:          minutes("m06.signal_ttl_minutes", 30),
		BuyCooldown:  minutes("m06.buy_cooldown_minutes", 240),
		SellCooldown: minutes("m06.sell_cooldown_minutes", 5),
		VersionID:    version,
	}
}

type Usecase struct {
	cfg        Settings
	market     MarketSource
	analyzer   Analyzer
	params     ParamRepo
	signals    SignalRepo
	candidates CandidateRepo
	blacklist  BlacklistRepo
	positions  PositionRepo
	cal        *tradecal.Calendar
	log        *log.Helper

	scanMu sync.Mutex
	killed atomic.Bool
}

func NewUsecase(cfg Settings, market MarketSource, analyzer Analyzer, params ParamRepo, signals SignalRepo,
	candidates CandidateRepo, blacklist BlacklistRepo, positions PositionRepo, logger log.Logger) *Usecase {
	return &Usecase{
		cfg: cfg, market: market, analyzer: analyzer, params: params, signals: signals,
		candidates: candidates, blacklist: blacklist, positions: positions,
		cal: tradecal.Default, log: log.NewHelper(log.With(logger, "module", "strategy/biz")),
	}
}

func (uc *Usecase) Settings() Settings { return uc.cfg }

// SetKillSwitch 记录 Kill Switch 状态。生效时停止出买入信号，卖出不受影响。
func (uc *Usecase) SetKillSwitch(active bool) {
	if uc.killed.Swap(active) != active {
		uc.log.Warnf("kill switch active=%v", active)
	}
}

func (uc *Usecase) KillSwitch() bool { return uc.killed.Load() }

// tradeDay 是 t 在上海时区的日期零点。
func tradeDay(t time.Time) time.Time {
	d := t.In(tradecal.Shanghai())
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, tradecal.Shanghai())
}

func minuteOfDay(t time.Time) int {
	d := t.In(tradecal.Shanghai())
	return d.Hour()*60 + d.Minute()
}

// validUntil 是信号有效期：出信号后 ttl，盘前出的从 09:30 起算，最晚当日 14:57。
func validUntil(asOf time.Time, ttl time.Duration) time.Time {
	day := tradeDay(asOf)
	start := asOf
	if open := day.Add(9*time.Hour + 30*time.Minute); start.Before(open) {
		start = open
	}
	end := start.Add(ttl)
	if last := day.Add(14*time.Hour + 57*time.Minute); end.After(last) {
		end = last
	}
	return end
}
