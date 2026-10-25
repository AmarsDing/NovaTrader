// readiness.go：/api/ops/readiness 闸门就绪聚合（W3a；UI 宪法 R-UI-30/36 配套）。
//
// 把「风险引擎 / 风控配置 / Kill Switch / SIM 20 日准入 / 文件单通道 / 数据源健康 / 模型配置」七道
// 闸门聚成一次只读聚合：ready=false 时 blockers 逐条给 gate/severity/why/deeplink，前端
// BlockerCard 直接渲染「卡在哪、去哪修」（W3b 消费 deeplink 锚点）。
//
// 纪律：
//   - 只读聚合：不下发任何写操作；解析在本文件完成，助手与 ops.go 同源复用；
//   - 「没取到≠0」：上游不可达 = 该闸门 down/unknown（风险类 fail-closed 记 danger）；字段缺省
//     （旧版服务）按「未证实」处理，不伪造通过；进度型闸门带 progress{done,total}；
//   - deeplink 是前端契约字符串：/ops#services、/trade#admission、/trade#broker、
//     /datahub#sources、/config#models、/config#risk、/risk#killswitch；改动须同提交回写 W3a 卡片。
package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// readinessGate 是一道闸门的当前判定；state∈{ok,warn,down,unknown}。
// 非 ok 的闸门带 severity(danger|warn)/why/deeplink 进 blockers；进度型闸门带 progress。
type readinessGate struct {
	Gate     string             `json:"gate"`
	State    string             `json:"state"`
	Severity string             `json:"severity,omitempty"`
	Why      string             `json:"why,omitempty"`
	Deeplink string             `json:"deeplink,omitempty"`
	Progress *readinessProgress `json:"progress,omitempty"`
}

type readinessProgress struct {
	Done  int `json:"done"`
	Total int `json:"total"`
}

// readinessOrder 是闸门输出序（前端 BlockerCard 按此渲染，不按字母）；risk_config 仅在
// config_ready=false 时出现，不固定占位。
var readinessOrder = []string{
	"risk_engine", "risk_config", "risk_killswitch",
	"sim_admission", "broker_gw", "datahub_sources", "llm_config",
}

func (g *Gate) readiness(w http.ResponseWriter, r *http.Request) {
	if _, ok := g.principal(r); !ok {
		writeErr(w, http.StatusUnauthorized, "未登录")
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 4*time.Second)
	defer cancel()
	client := g.httpClient()

	// 四路上游并发取数：任何一路慢/挂只拖慢自己，不阻塞其余闸门成文。
	results := make(chan readinessGate, 8)
	var wg sync.WaitGroup
	fan := func(collect func() []readinessGate) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, gt := range collect() {
				results <- gt
			}
		}()
	}
	fan(func() []readinessGate { return g.readinessRisk(ctx, client) })
	fan(func() []readinessGate { return g.readinessTrade(ctx, client) })
	fan(func() []readinessGate { return g.readinessSources(ctx, client) })
	fan(func() []readinessGate { return g.readinessLLM(ctx, client) })
	wg.Wait()
	close(results)

	byName := make(map[string]readinessGate, 8)
	for gt := range results {
		byName[gt.Gate] = gt
	}
	gates := make([]readinessGate, 0, len(byName))
	blockers := make([]readinessGate, 0, len(byName))
	for _, name := range readinessOrder {
		gt, ok := byName[name]
		if !ok {
			continue
		}
		gates = append(gates, gt)
		if gt.State != "ok" {
			blockers = append(blockers, gt)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"as_of":    g.now().UTC().Format(time.RFC3339),
		"ready":    len(blockers) == 0,
		"gates":    gates,
		"blockers": blockers,
	})
}

// readinessRisk 风险引擎就绪 + 配置加载 + Kill Switch 三闸；fail-closed：状态取不到按不可交易处理。
func (g *Gate) readinessRisk(ctx context.Context, client *http.Client) []readinessGate {
	engine := readinessGate{Gate: "risk_engine", Deeplink: "/ops#services"}
	kill := readinessGate{Gate: "risk_killswitch", Deeplink: "/risk#killswitch"}
	body, code, err := getJSON(ctx, client, g.Settings.Upstreams["risk"]+"/risk/v1/state")
	var parsed map[string]any
	if err != nil || code != http.StatusOK {
		engine.State, engine.Severity, engine.Why = "down", "danger", "risk 服务无响应，风控引擎状态未知（fail-closed，不可下单）"
		kill.State, kill.Severity, kill.Why = "unknown", "warn", "Kill Switch 状态随引擎一并未知"
		return []readinessGate{engine, kill}
	}
	if json.Unmarshal(body, &parsed) != nil {
		engine.State, engine.Severity, engine.Why = "down", "danger", "risk 状态报文解析失败（fail-closed）"
		kill.State, kill.Severity, kill.Why = "unknown", "warn", "Kill Switch 状态随引擎一并未知"
		return []readinessGate{engine, kill}
	}
	out := make([]readinessGate, 0, 3)
	ready, known := readinessBool(parsed, "ready")
	switch {
	case !known:
		// 没取到≠0：旧版服务没发就绪位 ≠ 已就绪。
		engine.State, engine.Severity, engine.Why = "unknown", "warn", "引擎就绪位缺省（旧版服务），按未证实处理"
	case !ready:
		engine.State, engine.Severity, engine.Why = "down", "danger", "风控引擎未就绪（ready=false），检查行情/配置后重启链路"
	default:
		engine.State = "ok"
	}
	out = append(out, engine)
	if cr, ok := readinessBool(parsed, "configReady", "config_ready"); ok && !cr {
		out = append(out, readinessGate{Gate: "risk_config", State: "warn", Severity: "warn",
			Why: "风控配置曾加载失败，当前沿用旧参数", Deeplink: "/config#risk"})
	}
	ks, _ := parsed["killSwitch"].(map[string]any)
	if ks == nil {
		ks, _ = parsed["kill_switch"].(map[string]any)
	}
	active, knownActive := false, false
	if ks != nil {
		active, knownActive = readinessBool(ks, "active")
	}
	switch {
	case ks == nil || !knownActive:
		kill.State, kill.Severity, kill.Why = "unknown", "warn", "Kill Switch 状态字段缺省，按未证实处理"
	case active:
		why := "Kill Switch 触发中"
		if reason := firstString(ks, "reason"); reason != "" {
			why = "Kill Switch 触发中：" + reason
		}
		kill.State, kill.Severity, kill.Why = "down", "danger", why
	default:
		kill.State = "ok"
	}
	out = append(out, kill)
	return out
}

// readinessTrade SIM 20 日准入 + 文件单通道两闸（trade /v1/trade/admission）。
// 准入未达标是「倒计时」不是「故障」：记 warn + progress；取证口挂才是 danger（fail-closed）。
func (g *Gate) readinessTrade(ctx context.Context, client *http.Client) []readinessGate {
	admission := readinessGate{Gate: "sim_admission", Deeplink: "/trade#admission"}
	broker := readinessGate{Gate: "broker_gw", Deeplink: "/trade#broker"}
	body, code, err := getJSON(ctx, client, g.Settings.Upstreams["trade"]+"/v1/trade/admission")
	var parsed map[string]any
	if err != nil || code != http.StatusOK || json.Unmarshal(body, &parsed) != nil {
		admission.State, admission.Severity, admission.Why = "down", "danger", "trade 取证口无响应，连续达标证明取不到（fail-closed）"
		broker.State, broker.Severity, broker.Why = "unknown", "warn", "文件单通道状态随取证口一并未知"
		return []readinessGate{admission, broker}
	}
	readyOK, known := readinessBool(parsed, "runEvidenceReady", "run_evidence_ready")
	switch {
	case !known:
		admission.State, admission.Severity, admission.Why = "unknown", "warn", "准入达标位缺省（旧版服务），按未达标处理"
	case !readyOK:
		admission.State, admission.Severity = "warn", "warn"
		consecutive, hasC := readinessInt(parsed, "consecutiveDays", "consecutive_days")
		required, hasR := readinessInt(parsed, "requiredDays", "required_days")
		if hasC && hasR && required > 0 {
			admission.Why = fmt.Sprintf("连续模拟达标 %d/%d 个交易日，未满足准入线", consecutive, required)
			admission.Progress = &readinessProgress{Done: consecutive, Total: required}
		} else {
			admission.Why = "连续模拟证据未达准入线"
		}
	default:
		admission.State = "ok"
	}
	connected, knownC := readinessBool(parsed, "brokerConnected", "broker_connected")
	switch {
	case !knownC:
		broker.State, broker.Severity, broker.Why = "unknown", "warn", "文件单通道状态字段缺省，按未证实处理"
	case !connected:
		broker.State, broker.Severity, broker.Why = "warn", "warn", "东财文件单通道未连接：SIM 不阻塞，实盘放行前必须恢复"
	default:
		broker.State = "ok"
	}
	return []readinessGate{admission, broker}
}

// readinessSources 数据源健康闸（datahub /v1/datahub/sources），状态归级与 ops.go 同源：
// 任何源 down → 闸门 down/danger；仅 warn 源 → warn；没登记源也是 warn（结构性缺口）。
func (g *Gate) readinessSources(ctx context.Context, client *http.Client) []readinessGate {
	row := readinessGate{Gate: "datahub_sources", Deeplink: "/datahub#sources"}
	body, code, err := getJSON(ctx, client, g.Settings.Upstreams["datahub"]+"/v1/datahub/sources")
	var parsed struct {
		Sources []map[string]any `json:"sources"`
	}
	if err != nil || code != http.StatusOK || json.Unmarshal(body, &parsed) != nil {
		row.State, row.Severity, row.Why = "down", "danger", "datahub 无响应，数据源健康未知（采集与复盘按缺数处理）"
		return []readinessGate{row}
	}
	if len(parsed.Sources) == 0 {
		row.State, row.Severity, row.Why = "warn", "warn", "没有登记任何数据源"
		return []readinessGate{row}
	}
	var downNames, warnNames []string
	for _, src := range parsed.Sources {
		name := firstString(src, "source", "domain")
		if name == "" {
			name = "未命名源"
		}
		switch sourceNodeStatus(firstString(src, "state", "status")) {
		case "down":
			downNames = append(downNames, name)
		case "warn":
			warnNames = append(warnNames, name)
		}
	}
	switch {
	case len(downNames) > 0:
		row.State, row.Severity, row.Why = "down", "danger", "数据源不可用："+joinNames(downNames)
		if len(warnNames) > 0 {
			row.Why += "；恢复中："+joinNames(warnNames)
		}
	case len(warnNames) > 0:
		row.State, row.Severity, row.Why = "warn", "warn", "数据源恢复中/覆盖率未证实："+joinNames(warnNames)
	default:
		row.State = "ok"
	}
	return []readinessGate{row}
}

// readinessLLM 模型配置闸（brain /v1/brain/stats）：configured 是结构性配置位、circuitOpen 是
// 运行态熔断（同 ops.go models 拆态）。无可用档 / 已配置档全熔断 = danger；部分熔断 = warn；
// 上游挂 = unknown/warn（AI 降级不挡规则管线）。
func (g *Gate) readinessLLM(ctx context.Context, client *http.Client) []readinessGate {
	row := readinessGate{Gate: "llm_config", Deeplink: "/config#models"}
	body, code, err := getJSON(ctx, client, g.Settings.Upstreams["brain"]+"/v1/brain/stats")
	var parsed struct {
		Tiers []map[string]any `json:"tiers"`
	}
	if err != nil || code != http.StatusOK || json.Unmarshal(body, &parsed) != nil {
		row.State, row.Severity, row.Why = "unknown", "warn", "脑服务无响应，模型配置态未知（AI 分析或降级）"
		return []readinessGate{row}
	}
	var configured, openNames []string
	for _, tier := range parsed.Tiers {
		if cfg, ok := tier["configured"].(bool); !ok || !cfg {
			continue
		}
		name := firstString(tier, "model", "tier")
		if name == "" {
			name = "model"
		}
		configured = append(configured, name)
		if open, ok := tier["circuitOpen"].(bool); ok && open {
			openNames = append(openNames, name)
			continue
		}
		if open, ok := tier["circuit_open"].(bool); ok && open {
			openNames = append(openNames, name)
		}
	}
	switch {
	case len(configured) == 0:
		row.State, row.Severity, row.Why = "down", "danger", "没有任何一档模型完成配置，AI 分析/复盘不可用"
	case len(openNames) == len(configured):
		row.State, row.Severity, row.Why = "down", "danger", "已配置模型档全部熔断："+joinNames(openNames)
	case len(openNames) > 0:
		row.State, row.Severity, row.Why = "warn", "warn", "部分模型档熔断中："+joinNames(openNames)
	default:
		row.State = "ok"
	}
	return []readinessGate{row}
}

// joinNames 名单压缩：前 3 名点名，其余计数为「等 N 个」。
func joinNames(names []string) string {
	const maxShow = 3
	if len(names) > maxShow {
		return fmt.Sprintf("%s 等 %d 个", strings.Join(names[:maxShow], "、"), len(names))
	}
	return strings.Join(names, "、")
}

// readinessBool 双键读 bool（protojson 骆驼/蛇形归一）；第二返回=字段是否存在且是 bool。
func readinessBool(m map[string]any, keys ...string) (bool, bool) {
	for _, k := range keys {
		if v, ok := m[k].(bool); ok {
			return v, true
		}
	}
	return false, false
}

// readinessInt 双键读整数（protojson int32 落 float64；数字字符串容错）。
func readinessInt(m map[string]any, keys ...string) (int, bool) {
	for _, k := range keys {
		switch v := m[k].(type) {
		case float64:
			return int(math.Round(v)), true
		case string:
			if n, err := strconv.Atoi(v); err == nil {
				return n, true
			}
		}
	}
	return 0, false
}
