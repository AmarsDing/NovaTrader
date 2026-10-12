package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ParamStore 读写 strategy_config。库没连上时 Enabled 为 false。
type ParamStore interface {
	Enabled() bool
	LoadParams(ctx context.Context) (map[string]string, error)
	SaveParams(ctx context.Context, values map[string]string) error
}

var m06Key = regexp.MustCompile(`^m06\.[A-Za-z0-9._]{1,60}$`)

// normalizeConfig 只放行配置页会写的键：选股标量，以及情绪、板块两份 JSON。
func normalizeConfig(values map[string]string) (map[string]string, error) {
	if len(values) == 0 {
		return nil, errString("没有要保存的参数")
	}
	if len(values) > 64 {
		return nil, errString("一次最多保存 64 项")
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, errString("参数值不能为空")
		}
		if len(value) > 8192 {
			return nil, errString("参数值过长")
		}
		switch {
		case strings.HasPrefix(key, "risk."):
			return nil, errString("风控参数在风控分类里保存")
		case key == "market.sentiment.rules" || key == "market.sector.weights":
			if !jsonObject(value) {
				return nil, errString("情绪和板块参数要是 JSON 对象")
			}
		case m06Key.MatchString(key) && len(key) <= 64:
			if _, err := strconv.ParseFloat(value, 64); err != nil {
				return nil, errString("选股参数要是数字")
			}
		case strings.HasPrefix(key, "app."):
			if err := checkApp(key, value); err != nil {
				return nil, err
			}
		default:
			return nil, errString("参数键无效")
		}
		out[key] = value
	}
	return out, nil
}

func jsonObject(raw string) bool {
	var body map[string]json.RawMessage
	return json.Unmarshal([]byte(raw), &body) == nil
}

const (
	appText appKind = iota
	appBool
	appTier
	appProvider
	appWeight
)

type appKind int

var appFields = map[string]appKind{
	"app.ds.tushare.token":  appText,
	"app.ds.tushare.on":     appBool,
	"app.ds.tavily.token":   appText,
	"app.ds.tavily.on":      appBool,
	"app.ds.tavily.feeds":   appText,
	"app.ds.pyworker":       appText,
	"app.ds.tdx_local.on":   appBool,
	"app.ds.tdx_file.on":    appBool,
	"app.ds.tdx_tcp.on":     appBool,
	"app.ds.akshare.on":     appBool,
	"app.ds.eastmoney.on":   appBool,
	"app.ds.sina.on":        appBool,
	"app.ds.tencent.on":     appBool,
	"app.ds.cninfo.on":      appBool,
	"app.ds.rss.on":         appBool,
	"app.ds.rss.feeds":      appText,
	"app.brain.large.url":   appText,
	"app.brain.large.model": appText,
	"app.brain.large.key":   appText,
	"app.brain.small.url":   appText,
	"app.brain.small.model": appText,
	"app.brain.small.key":   appText,
	"app.intel.enabled":     appBool,
	"app.intel.url":         appText,
	"app.intel.key":         appText,
	"app.intel.small":       appText,
	"app.intel.large":       appText,
	"app.kb.provider":       appProvider,
	"app.kb.endpoint":       appText,
	"app.kb.model":          appText,
	"app.kb.key":            appText,
	"app.agent.technical":   appTier,
	"app.agent.capital":     appTier,
	"app.agent.news":        appTier,
	"app.agent.external":    appTier,
	"app.agent.synthesis":   appTier,
	"app.agent.briefing":    appTier,
	"app.agent.explain":     appTier,
	"app.agent.ask":         appTier,
	"app.agent.w.technical": appWeight,
	"app.agent.w.news":      appWeight,
	"app.agent.w.capital":   appWeight,
	"app.agent.w.external":  appWeight,
	"app.notify.webhook":    appText,
	"app.notify.secret":     appText,
}

func checkApp(key, value string) error {
	kind, ok := appFields[key]
	if !ok || len(key) > 64 {
		return errString("参数键无效")
	}
	switch kind {
	case appBool:
		if value != "true" && value != "false" {
			return errString("开关只能是 true 或 false")
		}
	case appTier:
		if value != "small" && value != "large" {
			return errString("档位只能是 small 或 large")
		}
	case appProvider:
		if value != "none" && value != "openai" && value != "hash" {
			return errString("嵌入方式只能是 none、openai 或 hash")
		}
	case appWeight:
		f, err := strconv.ParseFloat(value, 64)
		if err != nil || f < 0 || f > 10 {
			return errString("权重要是 0 到 10 的数字")
		}
	}
	return nil
}

func (g *Gate) config(w http.ResponseWriter, r *http.Request) {
	principal, ok := g.principal(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "未登录")
		return
	}
	if g.Params == nil || !g.Params.Enabled() {
		writeErr(w, http.StatusServiceUnavailable, "参数表不可用")
		return
	}
	switch r.Method {
	case http.MethodGet:
		values, err := g.Params.LoadParams(r.Context())
		if err != nil {
			if g.Log != nil {
				g.Log.Errorf("config load: %v", err)
			}
			writeErr(w, http.StatusServiceUnavailable, "参数读取失败")
			return
		}
		if values == nil {
			values = map[string]string{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"values": values})
	case http.MethodPut:
		if principal.Role == "viewer" {
			writeErr(w, http.StatusForbidden, "当前是查看角色，不能改配置")
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			writeErr(w, http.StatusBadRequest, "请求读不了")
			return
		}
		var req struct {
			Values map[string]string `json:"values"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			writeErr(w, http.StatusBadRequest, "请求不是 JSON")
			return
		}
		values, err := normalizeConfig(req.Values)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		keys := make([]string, 0, len(values))
		for key := range values {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		if err := g.audit(r.Context(), Entry{
			Actor: principal.Name, Role: principal.Role, Action: "config.save",
			Target: clip(strings.Join(keys, ","), 256), OK: true, Detail: "配置页",
		}); err != nil {
			writeErr(w, http.StatusServiceUnavailable, "审计写入失败")
			return
		}
		if err := g.Params.SaveParams(r.Context(), values); err != nil {
			if g.Log != nil {
				g.Log.Errorf("config save: %v", err)
			}
			writeErr(w, http.StatusServiceUnavailable, "参数保存失败")
			return
		}
		stored, err := g.Params.LoadParams(r.Context())
		if err != nil || stored == nil {
			stored = values
		}
		writeJSON(w, http.StatusOK, map[string]any{"values": stored})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "不支持的方法")
	}
}
