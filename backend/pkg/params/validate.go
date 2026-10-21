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
	case strings.HasPrefix(key, "app.ds."), strings.HasPrefix(key, "app.domain."):
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

// --- 数据源自助配置键族（运行期盖写） ----------------------------------------
// 同步纪律：以下三张静态表与 app/datahub/internal/biz/model.go 的 Source*/Domain*
// 常量及 source.go 的 Supports 真值表同源；params 不能 import biz（biz→appcfg→params
// 成环），故在此静态登记。新增源/域时 model.go、本表、一致性测试三处必须同改；
// app/datahub/internal/data/params_conformance_test.go 是唯一的漂移哨兵。

// dsSourceNames 数据源白名单：13 个既有源 + 本批装配落地的 sina7x24、baostock（W5/W9）。
var dsSourceNames = []string{
	"tdx_local", "tdx_file", "tdx_tcp", "akshare", "tushare", "eastmoney", "sina", "tencent",
	"cninfo", "tavily", "rss", "tdx_http_archive", "openbb_fred",
	"sina7x24", "baostock",
}

// dsDomainNames 数据域白名单：biz/model.go 的 24 个 Domain* 常量（含本批落地的
// block_trade、guba）。params 不能 import biz（见头部环注解），故保持字符串字面量；
// app/datahub/internal/data/params_conformance_test.go 已收紧为与 biz 常量纯等值比对。
var dsDomainNames = []string{
	"security", "daily_bar", "adj_factor", "minute_bar", "snapshot", "limit_pool", "money_flow",
	"lhb", "unlock", "margin", "margin_mkt", "hsgt_top10", "sector", "sector_quote", "finance",
	"flash", "news", "announcement", "report", "overseas", "macro", "hot_rank",
	"block_trade", "guba",
}

// dsCapability 源 → 声明支持的数据域，按接口实现逐源对齐 biz.Supports：
// money_flow 取 MoneyFlowSource 或 IntradayFlowSource；guba 与其余 intel 四域同取
// IntelSource（接口能力口径，不代表源对每类 kind 都有真实内容）。
var dsCapability = map[string][]string{
	"tdx_local":        {"security", "daily_bar", "minute_bar", "snapshot"},
	"tdx_file":         {"daily_bar", "minute_bar", "sector"},
	"tdx_tcp":          {"adj_factor", "daily_bar", "minute_bar", "security", "snapshot"},
	"akshare":          {"limit_pool", "macro", "minute_bar", "overseas"},
	"tushare":          {"adj_factor", "daily_bar", "finance", "hsgt_top10", "lhb", "margin", "margin_mkt", "money_flow", "security"},
	"eastmoney":        {"announcement", "block_trade", "daily_bar", "finance", "flash", "guba", "hot_rank", "hsgt_top10", "lhb", "limit_pool", "margin", "money_flow", "news", "report", "sector", "sector_quote", "security", "snapshot", "unlock"},
	"sina":             {"snapshot"},
	"tencent":          {"snapshot"},
	"cninfo":           {"announcement", "flash", "guba", "news", "report"},
	"tavily":           {"announcement", "flash", "guba", "news", "report"},
	"rss":              {"announcement", "flash", "guba", "news", "report"},
	"tdx_http_archive": {"daily_bar"},
	"openbb_fred":      {"macro"},
	"sina7x24":         {"announcement", "flash", "guba", "news", "report"},
	"baostock":         {"daily_bar", "finance"},
}

var dsSourceSet = func() map[string]bool {
	m := make(map[string]bool, len(dsSourceNames))
	for _, s := range dsSourceNames {
		m[s] = true
	}
	return m
}()

// SupportsDataSource 判断 source 是否声明支持 domain，供降级链校验与一致性测试使用。
func SupportsDataSource(domain, source string) bool {
	for _, d := range dsCapability[source] {
		if d == domain {
			return true
		}
	}
	return false
}

// DataSourceNames 返回数据源白名单副本，供跨包一致性测试比对。
func DataSourceNames() []string { return append([]string(nil), dsSourceNames...) }

// DataDomainNames 返回数据域白名单副本，供跨包一致性测试比对。
func DataDomainNames() []string { return append([]string(nil), dsDomainNames...) }

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
	add("bool", "ds.tushare.on ds.tavily.on ds.tdx_local.on ds.tdx_file.on ds.tdx_tcp.on ds.akshare.on ds.eastmoney.on ds.sina.on ds.tencent.on ds.cninfo.on ds.rss.on intel.enabled ds.openbb_fred.on ds.tdx_http_archive.on ds.sina7x24.on ds.baostock.on")
	// LLM 底座治理（M13-T06）：关闭 JSON 模式以适配不支持 response_format 的模型网关。
	add("bool", "brain.large.disable_json_mode brain.small.disable_json_mode intel.disable_json_mode")
	add("text", "ds.tushare.token ds.tavily.token ds.tavily.feeds ds.rss.feeds ds.eastmoney.margin_reports ds.openbb_fred.key brain.large.model brain.large.key brain.small.model brain.small.key intel.key intel.small intel.large kb.model kb.key notify.webhook notify.secret")
	add("url", "ds.pyworker brain.large.url brain.small.url intel.url kb.endpoint")
	add("tier", "agent.technical agent.capital agent.news agent.external agent.synthesis agent.briefing agent.explain agent.ask")
	add("weight", "agent.w.technical agent.w.news agent.w.capital agent.w.external")
	add("provider", "kb.provider")
	// 数据源键族：源级 qps/quota(0=不限)/timeout_sec 与域级降级链 sources。
	for _, s := range dsSourceNames {
		m["app.ds."+s+".qps"] = "qps"
		m["app.ds."+s+".quota"] = "quota"
		m["app.ds."+s+".timeout_sec"] = "timeout_sec"
	}
	for _, d := range dsDomainNames {
		m["app.domain."+d+".sources"] = "sourcesjson"
	}
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
			case "text":
				// M13-T06：密钥类键允许 env:VAR（运行期由 pkg/llm 解析）与 enc:（M13 预留密文）
				// 前缀值；裸 env: 缺变量名在任何 text 键上都不完整，配置页即拦下。
				if strings.HasPrefix(value, "env:") && strings.TrimSpace(strings.TrimPrefix(value, "env:")) == "" {
					return nil, fmt.Errorf("%s: env: 后需跟环境变量名", key)
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
			case "qps":
				n, e := strconv.ParseFloat(value, 64)
				if e != nil || math.Trunc(n) != n || n < 1 || n > 50 {
					return nil, fmt.Errorf("%s: 每秒请求需在1到50的整数", key)
				}
			case "quota":
				n, e := strconv.ParseFloat(value, 64)
				if e != nil || math.Trunc(n) != n || n < 0 || n > 100000 {
					return nil, fmt.Errorf("%s: 每日配额需在0到100000的整数（0表示不限）", key)
				}
			case "timeout_sec":
				n, e := strconv.ParseFloat(value, 64)
				if e != nil || math.Trunc(n) != n || n < 1 || n > 120 {
					return nil, fmt.Errorf("%s: 超时需在1到120秒的整数", key)
				}
			case "sourcesjson":
				if err := checkSourceChain(key, value); err != nil {
					return nil, err
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

// checkSourceChain 校验 app.domain.<domain>.sources：严格 JSON 字符串数组（拒绝尾随垃圾），
// 长度 1..6、去重、元素在源白名单且都声明支持该域。错误文案不携带提交值。
// 空串清除覆盖走 appAllowed 既有空值分支，不经这里。
func checkSourceChain(key, raw string) error {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "[") {
		return fmt.Errorf("%s: 降级链需为JSON字符串数组", key)
	}
	var names []string
	d := json.NewDecoder(strings.NewReader(trimmed))
	if err := d.Decode(&names); err != nil {
		return fmt.Errorf("%s: 降级链需为JSON字符串数组", key)
	}
	var trailing any
	if err := d.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("%s: 降级链必须是单个JSON数组", key)
	}
	domain := strings.TrimSuffix(strings.TrimPrefix(key, "app.domain."), ".sources")
	if len(names) < 1 || len(names) > 6 {
		return fmt.Errorf("%s: 降级链长度需在1到6", key)
	}
	seen := map[string]bool{}
	for _, n := range names {
		if !dsSourceSet[n] {
			return fmt.Errorf("%s: 存在未登记的数据源", key)
		}
		if seen[n] {
			return fmt.Errorf("%s: 降级链不得重复", key)
		}
		seen[n] = true
		if !SupportsDataSource(domain, n) {
			return fmt.Errorf("%s: 存在不支持该域的数据源", key)
		}
	}
	return nil
}
