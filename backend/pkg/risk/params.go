package risk

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"server/pkg/ashare"
)

// KeyPrefix 是风控参数在 strategy_config 里的键前缀。
const KeyPrefix = "risk."

// 程序化交易高频认定线：每秒申报撤单 300 笔、每日 20000 笔。速度参数必须低于这两条线。
const (
	HFTPerSecond = 300
	HFTPerDay    = 20000
)

// Params 是风控参数。默认值见设计文档第 3、4 节。
type Params struct {
	LiveAdmitted     bool
	QuoteMaxDelay    time.Duration
	AccountMaxAge    time.Duration
	AllowSTBuy       bool
	MinListDays      int
	Blacklist        map[string]bool
	MaxSinglePct     float64
	MaxTotalPct      float64
	MaxSectorPct     float64
	MaxDailyBuys     int
	MinTurnover      float64
	MaxParticipation float64
	MaxOrdersPerSec  int
	MaxOrdersPerDay  int
	DailyLossPct     float64
	IndexDropPct     float64
	BurstRateMax     float64
	PhaseScale       map[string]float64
	RiskScale        map[int]float64
	// RiskLevel 是人工设定的风险度下限，和 M02 发布的风险度取高者。
	RiskLevel int
	// BadNewsSentiment 是 intel.alert 判为突发利空的情感阈值。
	BadNewsSentiment float64

	StopPct          float64
	IndexPlunge5m    float64
	TrailStartPct    float64
	TrailDrawdownPct float64
	TimeStopDays     int
	TimeStopMinGain  float64
	ExitSlipTicks    int
	MarketDownSec    int

	// 拒单率告警。RejectAlertMin 为 0 时关闭。
	RejectAlertWindow   time.Duration
	RejectAlertMin      int
	RejectAlertRate     float64
	RejectAlertCooldown time.Duration

	Fee ashare.Fee
}

// Defaults 返回默认参数。
func Defaults() Params {
	return Params{
		QuoteMaxDelay:       10 * time.Second,
		AccountMaxAge:       60 * time.Second,
		MinListDays:         ashare.NoLimitDays,
		Blacklist:           map[string]bool{},
		MaxSinglePct:        0.05,
		MaxTotalPct:         0.80,
		MaxSectorPct:        0.30,
		MaxDailyBuys:        10,
		MinTurnover:         50_000_000,
		MaxParticipation:    0.01,
		MaxOrdersPerSec:     5,
		MaxOrdersPerDay:     1000,
		DailyLossPct:        0.02,
		IndexDropPct:        0.02,
		BurstRateMax:        0.5,
		PhaseScale:          map[string]float64{"ICE": 0.2, "RECOVER": 0.5, "WARM": 0.8, "HOT": 1.0, "FADE": 0.3, "": 0.5},
		RiskScale:           map[int]float64{0: 1.0, 1: 0.7, 2: 0.4, 3: 0},
		BadNewsSentiment:    -0.5,
		StopPct:             0.05,
		IndexPlunge5m:       -0.015,
		TrailStartPct:       0.05,
		TrailDrawdownPct:    0.03,
		TimeStopDays:        2,
		TimeStopMinGain:     0.01,
		ExitSlipTicks:       2,
		MarketDownSec:       30,
		RejectAlertWindow:   5 * time.Minute,
		RejectAlertMin:      20,
		RejectAlertRate:     0.5,
		RejectAlertCooldown: 30 * time.Minute,
		Fee:                 ashare.DefaultFee(),
	}
}

// Scale 是仓位系数 = 情绪系数 × 风险度系数。情绪系数优先用 M02 发布的 position_scale，
// 没有时按阶段查表；未配置的阶段按缺失处理，未配置的风险度按 0。
func (p Params) Scale(m Market) float64 {
	ps := m.PositionScale
	if ps <= 0 || ps > 1 {
		var ok bool
		if ps, ok = p.PhaseScale[strings.ToUpper(m.Phase)]; !ok {
			ps = p.PhaseScale[""]
		}
	}
	rs, ok := p.RiskScale[m.RiskLevel]
	if !ok {
		rs = 0
	}
	return ps * rs
}

type paramField struct {
	set func(*Params, string) error
	get func(*Params) string
}

var fields = map[string]paramField{
	"live_admitted":       boolField(func(p *Params) *bool { return &p.LiveAdmitted }),
	"quote_max_delay_ms":  durField(func(p *Params) *time.Duration { return &p.QuoteMaxDelay }, time.Millisecond, 100, 60_000),
	"account_max_age_sec": durField(func(p *Params) *time.Duration { return &p.AccountMaxAge }, time.Second, 5, 600),
	"allow_st_buy":        boolField(func(p *Params) *bool { return &p.AllowSTBuy }),
	"min_list_days":       intField(func(p *Params) *int { return &p.MinListDays }, 0, 250),
	"blacklist": {
		set: func(p *Params, v string) error {
			m := map[string]bool{}
			for _, s := range strings.Split(v, ",") {
				if s = strings.ToUpper(strings.TrimSpace(s)); s != "" {
					m[s] = true
				}
			}
			p.Blacklist = m
			return nil
		},
		get: func(p *Params) string {
			out := make([]string, 0, len(p.Blacklist))
			for s := range p.Blacklist {
				out = append(out, s)
			}
			sort.Strings(out)
			return strings.Join(out, ",")
		},
	},
	"max_single_pct":     floatField(func(p *Params) *float64 { return &p.MaxSinglePct }, 0, 1),
	"max_total_pct":      floatField(func(p *Params) *float64 { return &p.MaxTotalPct }, 0, 1),
	"max_sector_pct":     floatField(func(p *Params) *float64 { return &p.MaxSectorPct }, 0, 1),
	"max_daily_buys":     intField(func(p *Params) *int { return &p.MaxDailyBuys }, 0, 1000),
	"min_turnover":       floatField(func(p *Params) *float64 { return &p.MinTurnover }, 0, 1e12),
	"max_participation":  floatField(func(p *Params) *float64 { return &p.MaxParticipation }, 0, 1),
	"max_orders_per_sec": intField(func(p *Params) *int { return &p.MaxOrdersPerSec }, 1, HFTPerSecond-1),
	"max_orders_per_day": intField(func(p *Params) *int { return &p.MaxOrdersPerDay }, 1, HFTPerDay-1),
	"daily_loss_pct":     floatField(func(p *Params) *float64 { return &p.DailyLossPct }, 0, 1),
	"index_drop_pct":     floatField(func(p *Params) *float64 { return &p.IndexDropPct }, 0, 1),
	"burst_rate_max":     floatField(func(p *Params) *float64 { return &p.BurstRateMax }, 0, 1),
	"phase_scale": {
		set: func(p *Params, v string) error {
			var m map[string]float64
			if err := json.Unmarshal([]byte(v), &m); err != nil {
				return err
			}
			out := map[string]float64{}
			for k, s := range m {
				if s < 0 || s > 1 {
					return fmt.Errorf("phase_scale %s=%v out of [0,1]", k, s)
				}
				out[strings.ToUpper(k)] = s
			}
			if _, ok := out[""]; !ok {
				out[""] = p.PhaseScale[""]
			}
			p.PhaseScale = out
			return nil
		},
		get: func(p *Params) string { b, _ := json.Marshal(p.PhaseScale); return string(b) },
	},
	"risk_scale": {
		set: func(p *Params, v string) error {
			var m map[string]float64
			if err := json.Unmarshal([]byte(v), &m); err != nil {
				return err
			}
			out := map[int]float64{}
			for k, s := range m {
				lv, err := strconv.Atoi(k)
				if err != nil || lv < 0 || lv > 3 || s < 0 || s > 1 {
					return fmt.Errorf("risk_scale %s=%v invalid", k, s)
				}
				out[lv] = s
			}
			p.RiskScale = out
			return nil
		},
		get: func(p *Params) string { b, _ := json.Marshal(p.RiskScale); return string(b) },
	},
	"risk_level":                intField(func(p *Params) *int { return &p.RiskLevel }, 0, 3),
	"bad_news_sentiment":        floatField(func(p *Params) *float64 { return &p.BadNewsSentiment }, -1, 0),
	"stop_pct":                  floatField(func(p *Params) *float64 { return &p.StopPct }, 0, 0.5),
	"index_plunge_5m":           floatField(func(p *Params) *float64 { return &p.IndexPlunge5m }, -0.2, 0),
	"trail_start_pct":           floatField(func(p *Params) *float64 { return &p.TrailStartPct }, 0, 1),
	"trail_drawdown_pct":        floatField(func(p *Params) *float64 { return &p.TrailDrawdownPct }, 0, 1),
	"time_stop_days":            intField(func(p *Params) *int { return &p.TimeStopDays }, 1, 60),
	"time_stop_min_gain":        floatField(func(p *Params) *float64 { return &p.TimeStopMinGain }, -1, 1),
	"exit_slip_ticks":           intField(func(p *Params) *int { return &p.ExitSlipTicks }, 0, 50),
	"market_down_sec":           intField(func(p *Params) *int { return &p.MarketDownSec }, 5, 600),
	"reject_alert_window_sec":   durField(func(p *Params) *time.Duration { return &p.RejectAlertWindow }, time.Second, 60, 3600),
	"reject_alert_min":          intField(func(p *Params) *int { return &p.RejectAlertMin }, 0, 10000),
	"reject_alert_rate":         floatField(func(p *Params) *float64 { return &p.RejectAlertRate }, 0, 1),
	"reject_alert_cooldown_sec": durField(func(p *Params) *time.Duration { return &p.RejectAlertCooldown }, time.Second, 60, 86400),
}

// Keys 返回全部参数名（不带前缀），已排序。
func Keys() []string {
	out := make([]string, 0, len(fields))
	for k := range fields {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Set 校验并修改一个参数。key 可以带或不带 risk. 前缀。
func (p *Params) Set(key, value string) error {
	key = strings.TrimPrefix(key, KeyPrefix)
	f, ok := fields[key]
	if !ok {
		return fmt.Errorf("risk: unknown param %q", key)
	}
	if err := f.set(p, strings.TrimSpace(value)); err != nil {
		return fmt.Errorf("risk: param %s: %w", key, err)
	}
	return nil
}

// Get 读取一个参数的字符串值。
func (p *Params) Get(key string) (string, bool) {
	f, ok := fields[strings.TrimPrefix(key, KeyPrefix)]
	if !ok {
		return "", false
	}
	return f.get(p), true
}

// Values 返回全部参数，键带 risk. 前缀。
func (p *Params) Values() map[string]string {
	out := make(map[string]string, len(fields))
	for k, f := range fields {
		out[KeyPrefix+k] = f.get(p)
	}
	return out
}

// FromConfig 在默认值上叠加 strategy_config 里的 risk.* 行。非 risk. 前缀的键忽略；
// 无效值不生效并返回错误列表，其余参数照常生效。
func FromConfig(rows map[string]string) (Params, []error) {
	p := Defaults()
	var errs []error
	keys := make([]string, 0, len(rows))
	for k := range rows {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !strings.HasPrefix(k, KeyPrefix) {
			continue
		}
		if err := p.Set(k, rows[k]); err != nil {
			errs = append(errs, err)
		}
	}
	return p, errs
}

// Clone 深拷贝 map 字段，供热更新时在副本上修改。
func (p Params) Clone() Params {
	q := p
	q.Blacklist = make(map[string]bool, len(p.Blacklist))
	for k, v := range p.Blacklist {
		q.Blacklist[k] = v
	}
	q.PhaseScale = make(map[string]float64, len(p.PhaseScale))
	for k, v := range p.PhaseScale {
		q.PhaseScale[k] = v
	}
	q.RiskScale = make(map[int]float64, len(p.RiskScale))
	for k, v := range p.RiskScale {
		q.RiskScale[k] = v
	}
	return q
}

func boolField(ptr func(*Params) *bool) paramField {
	return paramField{
		set: func(p *Params, v string) error {
			b, err := strconv.ParseBool(v)
			if err != nil {
				return err
			}
			*ptr(p) = b
			return nil
		},
		get: func(p *Params) string { return strconv.FormatBool(*ptr(p)) },
	}
}

func intField(ptr func(*Params) *int, lo, hi int) paramField {
	return paramField{
		set: func(p *Params, v string) error {
			n, err := strconv.Atoi(v)
			if err != nil {
				return err
			}
			if n < lo || n > hi {
				return fmt.Errorf("%d out of [%d,%d]", n, lo, hi)
			}
			*ptr(p) = n
			return nil
		},
		get: func(p *Params) string { return strconv.Itoa(*ptr(p)) },
	}
}

func floatField(ptr func(*Params) *float64, lo, hi float64) paramField {
	return paramField{
		set: func(p *Params, v string) error {
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return err
			}
			if f < lo || f > hi {
				return fmt.Errorf("%v out of [%v,%v]", f, lo, hi)
			}
			*ptr(p) = f
			return nil
		},
		get: func(p *Params) string { return strconv.FormatFloat(*ptr(p), 'f', -1, 64) },
	}
}

func durField(ptr func(*Params) *time.Duration, unit time.Duration, lo, hi int) paramField {
	return paramField{
		set: func(p *Params, v string) error {
			n, err := strconv.Atoi(v)
			if err != nil {
				return err
			}
			if n < lo || n > hi {
				return fmt.Errorf("%d out of [%d,%d]", n, lo, hi)
			}
			*ptr(p) = time.Duration(n) * unit
			return nil
		},
		get: func(p *Params) string { return strconv.FormatInt(int64(*ptr(p)/unit), 10) },
	}
}
