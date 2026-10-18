package params

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/url"
	"server/pkg/market"
	"server/pkg/risk"
	"strconv"
	"strings"
)

func Secret(key string) bool {
	return strings.HasSuffix(key, ".token") || strings.HasSuffix(key, ".key") || key == "app.notify.webhook" || key == "app.notify.secret"
}
func Target(key string) (service, effective string) {
	switch {
	case strings.HasPrefix(key, "risk."):
		return "risk", "服务轮询后原子安装；最长约3秒，加载回执为准"
	case strings.HasPrefix(key, "m06."):
		return "strategy", "下一次扫描加载"
	case strings.HasPrefix(key, "market."):
		return "market", "下一次行情预热；盘中保留已加载版本"
	case strings.HasPrefix(key, "app.ds."):
		return "datahub", "下一次采集读取；配置缓存最长5秒"
	case strings.HasPrefix(key, "app.brain.") || strings.HasPrefix(key, "app.agent."):
		return "brain", "下一次研判读取；配置缓存最长5秒"
	case strings.HasPrefix(key, "app.intel.") || strings.HasPrefix(key, "app.kb."):
		return "intel", "下一次处理读取；配置缓存最长5秒"
	case strings.HasPrefix(key, "app.notify."):
		return "notify", "下一次通知读取；配置缓存最长5秒"
	default:
		return "unknown", "加载点未登记"
	}
}

type numberRule struct {
	min, max float64
	integer  bool
}

var numericRules = func() map[string]numberRule {
	m := map[string]numberRule{}
	add := func(keys string, min, max float64, integer bool) {
		for _, k := range strings.Fields(keys) {
			m["m06."+k] = numberRule{min, max, integer}
		}
	}
	add("min_list_days fb.no_limit_days", 0, 10000, true)
	add("rule_top_n ai_top_n max_daily_signals", 0, 10000, true)
	add("signal_ttl_minutes buy_cooldown_minutes sell_cooldown_minutes", 1, 10080, true)
	add("atr_period pd.lookback fb.hold_days pd.hold_days", 1, 1000, true)
	add("min_amount fb.big_amount", 0, 1e14, false)
	add("rule_weight ai_weight stage_coef.ICE stage_coef.RECOVER stage_coef.WARM stage_coef.HOT stage_coef.FADE stop_atr take_atr entry_band_atr fb.amount_ratio pd.strong_gain pd.shrink_max", 0, 100, false)
	add("select_score high_score", 0, 100, false)
	add("max_stop_pct sell_slip_pct position_per_stock normal_position_factor fb.gap_min fb.gap_max fb.strength_min pd.dd_min pd.dd_max pd.dd_ideal pd.ma_band pd.rebound_min pd.floor_pct", 0, 1, false)
	return m
}()
var appAllowed = func() map[string]string {
	m := map[string]string{}
	add := func(kind, keys string) {
		for _, k := range strings.Fields(keys) {
			m["app."+k] = kind
		}
	}
	add("bool", "ds.tushare.on ds.tavily.on ds.tdx_local.on ds.tdx_file.on ds.tdx_tcp.on ds.akshare.on ds.eastmoney.on ds.sina.on ds.tencent.on ds.cninfo.on ds.rss.on intel.enabled")
	add("text", "ds.tushare.token ds.tavily.token ds.tavily.feeds ds.rss.feeds brain.large.model brain.large.key brain.small.model brain.small.key intel.key intel.small intel.large kb.model kb.key notify.webhook notify.secret")
	add("url", "ds.pyworker brain.large.url brain.small.url intel.url kb.endpoint")
	add("tier", "agent.technical agent.capital agent.news agent.external agent.synthesis agent.briefing agent.explain agent.ask")
	add("weight", "agent.w.technical agent.w.news agent.w.capital agent.w.external")
	add("provider", "kb.provider")
	return m
}()

// ValidatePatch accepts only the existing product catalogue. It returns canonical
// values and never includes submitted values in errors (they may be secrets).
func ValidatePatch(current, patch map[string]string) (map[string]string, error) {
	if len(patch) == 0 || len(patch) > 256 {
		return nil, fmt.Errorf("一次需提交1到256项")
	}
	out := map[string]string{}
	rp, _ := risk.FromConfig(current)
	for raw, value := range patch {
		key := strings.TrimSpace(raw)
		value = strings.TrimSpace(value)
		if len(value) > 8192 {
			return nil, fmt.Errorf("%s: 值过长", key)
		}
		if number, err := strconv.ParseFloat(value, 64); err == nil && (math.IsNaN(number) || math.IsInf(number, 0)) {
			return nil, fmt.Errorf("参数不得为非有限数值")
		}
		if _, exists := out[key]; exists {
			return nil, fmt.Errorf("重复参数键")
		}
		if value == "" {
			_, a := numericRules[key]
			_, b := appAllowed[key]
			defaults := risk.Defaults()
			_, c := defaults.Get(strings.TrimPrefix(key, "risk."))
			if a || b || key == market.RulesKey || key == market.SectorWeightsKey || (strings.HasPrefix(key, "risk.") && c) {
				out[key] = ""
				continue
			}
			return nil, fmt.Errorf("未登记的参数键")
		}
		switch {
		case strings.HasPrefix(key, "risk."):
			if key == "risk.live_admitted" && value == "true" && current[key] != "true" {
				return nil, fmt.Errorf("实盘准入只能通过准入流程开启")
			}
			if err := rp.Set(strings.TrimPrefix(key, "risk."), value); err != nil {
				return nil, fmt.Errorf("%s: 风控类型或范围无效", key)
			}
		case strings.HasPrefix(key, "m06."):
			rule, ok := numericRules[key]
			n, err := strconv.ParseFloat(value, 64)
			if !ok || err != nil || math.IsInf(n, 0) || math.IsNaN(n) || n < rule.min || n > rule.max || (rule.integer && math.Trunc(n) != n) {
				return nil, fmt.Errorf("%s: 不在登记类型或范围内", key)
			}
		case key == market.RulesKey:
			r := market.DefaultRules()
			if err := strictObject(value, &r); err != nil {
				return nil, fmt.Errorf("情绪参数字段或类型无效")
			}
			if r.IceMaxUp < 0 || r.IceMinDown < 0 || r.HotMinUp < 0 || r.HotMinHeight < 0 || r.WarmMinUp < 0 || r.SurgeMinutes < 1 || r.SurgeMinutes > 240 || r.SurgePct < 0 || r.Risk3IndexPct > r.Risk2IndexPct || r.Risk2IndexPct > r.Risk1IndexPct || r.Risk3Index5m > r.Risk2Index5m {
				return nil, fmt.Errorf("情绪参数范围或风险度顺序无效")
			}
			for _, n := range []float64{r.FadeMinBrokenRate, r.HotMaxBrokenRate, r.WarmMaxBrokenRate, r.Risk1BrokenRate} {
				if n < 0 || n > 1 {
					return nil, fmt.Errorf("炸板率需在0到1")
				}
			}
			for p, v := range r.Phases {
				if !strings.Contains(" ICE FADE HOT WARM RECOVER ", " "+string(p)+" ") || v.ScoreCoef < 0 || v.ScoreCoef > 10 || v.PositionScale < 0 || v.PositionScale > 1 {
					return nil, fmt.Errorf("情绪阶段系数无效")
				}
			}
		case key == market.SectorWeightsKey:
			r := market.DefaultSectorWeights()
			if err := strictObject(value, &r); err != nil {
				return nil, fmt.Errorf("板块参数字段或类型无效")
			}
			if r.Pct < 0 || r.Limit < 0 || r.Breadth < 0 || r.Overseas < 0 || r.MinMembers < 1 || r.MinMembers > 10000 || r.LeaderMinAmount < 0 {
				return nil, fmt.Errorf("板块参数超出范围")
			}
		default:
			kind, ok := appAllowed[key]
			if !ok {
				return nil, fmt.Errorf("未登记的参数键")
			}
			switch kind {
			case "bool":
				if value != "true" && value != "false" {
					return nil, fmt.Errorf("%s: 开关需为true/false", key)
				}
			case "tier":
				if value != "small" && value != "large" {
					return nil, fmt.Errorf("%s: 档位无效", key)
				}
			case "provider":
				if value != "none" && value != "openai" && value != "hash" {
					return nil, fmt.Errorf("嵌入方式无效")
				}
			case "weight":
				n, e := strconv.ParseFloat(value, 64)
				if e != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 10 {
					return nil, fmt.Errorf("权重需在0到10")
				}
			case "url":
				if value != "" {
					u, e := url.Parse(value)
					if e != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || u.RawQuery != "" || u.Fragment != "" {
						return nil, fmt.Errorf("%s: 需为不含凭据/查询串的HTTP地址", key)
					}
				}
			}
		}
		out[key] = value
	}
	merged := map[string]string{}
	for k, v := range current {
		merged[k] = v
	}
	for k, v := range out {
		merged[k] = v
	}
	for _, pair := range [][2]string{{"m06.fb.gap_min", "m06.fb.gap_max"}, {"m06.pd.dd_min", "m06.pd.dd_max"}, {"m06.select_score", "m06.high_score"}} {
		lo, a := strconv.ParseFloat(merged[pair[0]], 64)
		hi, b := strconv.ParseFloat(merged[pair[1]], 64)
		if a == nil && b == nil && lo > hi {
			return nil, fmt.Errorf("上下界顺序无效")
		}
	}
	return out, nil
}
func strictObject(raw string, v any) error {
	if !strings.HasPrefix(strings.TrimSpace(raw), "{") {
		return fmt.Errorf("object required")
	}
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	var trailing any
	if err := d.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("JSON必须是单个对象")
	}
	return nil
}
