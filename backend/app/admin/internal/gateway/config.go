package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"server/pkg/params"
	"sort"
	"strconv"
	"strings"
)

// ParamStore retains the legacy type name, with versioning required at runtime.
type ParamStore interface {
	Enabled() bool
	LoadParams(context.Context) (map[string]string, error)
	SaveParams(context.Context, map[string]string) error
}
type RevisionStore interface {
	ReadConfig(context.Context) (params.Snapshot, error)
	SaveConfig(context.Context, int, map[string]string) (params.Snapshot, error)
	VersionConfig(context.Context, int) (params.Snapshot, error)
	RollbackConfig(context.Context, int, int) (params.Snapshot, error)
	ConfigReceipts(context.Context) ([]params.Receipt, error)
	ConfigHistory(context.Context) ([]ConfigVersion, error)
}
type ConfigVersion struct {
	Revision  int    `json:"revision"`
	CreatedAt string `json:"created_at"`
	Active    bool   `json:"active"`
}
type configChange struct {
	Key       string `json:"key"`
	Before    string `json:"before"`
	After     string `json:"after"`
	Secret    bool   `json:"secret"`
	Service   string `json:"service"`
	Effective string `json:"effective"`
}
type configPreview struct {
	SchemaVersion    int            `json:"schema_version"`
	BaseRevision     int            `json:"base_revision"`
	Changes          []configChange `json:"changes"`
	KnownEffects     []string       `json:"known_effects"`
	UnknownEffects   []string       `json:"unknown_effects"`
	Services         []string       `json:"services"`
	InvalidatesPlans bool           `json:"invalidates_plans"`
}

// Kept for existing callers; new saves validate against their exact base snapshot.
func normalizeConfig(values map[string]string) (map[string]string, error) {
	return params.ValidatePatch(nil, values)
}
func configPublic(s params.Snapshot) map[string]any {
	values := map[string]string{}
	secret := map[string]bool{}
	for k, v := range s.Values {
		if service, _ := params.Target(k); service == "unknown" {
			continue
		}
		if params.Secret(k) {
			secret[k] = v != ""
		} else {
			values[k] = v
		}
	}
	return map[string]any{"schema_version": 1, "revision": s.Revision, "created_at": s.CreatedAt, "values": values, "secret_set": secret, "note": "保存版本和服务加载版本分别展示；无回执表示尚未观测，不代表已生效。"}
}
func previewConfig(current params.Snapshot, values map[string]string, replace bool) configPreview {
	out := configPreview{SchemaVersion: 1, BaseRevision: current.Revision, Changes: []configChange{}, Services: []string{}, KnownEffects: []string{}, UnknownEffects: []string{"未执行策略回测；收益、信号数量、外部服务连通性及在途任务影响尚未测定。"}}
	keys := map[string]bool{}
	for k := range values {
		keys[k] = true
	}
	if replace {
		for k := range current.Values {
			keys[k] = true
		}
	}
	services := map[string]bool{}
	for k := range keys {
		old := current.Values[k]
		next, ok := values[k]
		if !ok && !replace {
			continue
		}
		if old == next {
			continue
		}
		svc, when := params.Target(k)
		c := configChange{Key: k, Before: old, After: next, Secret: params.Secret(k), Service: svc, Effective: when}
		if c.Secret {
			c.Before = "未设置"
			if old != "" {
				c.Before = "已设置"
			}
			c.After = "清除覆盖值"
			if next != "" {
				c.After = "设置新值"
			}
		}
		out.Changes = append(out.Changes, c)
		services[svc] = true
	}
	sort.Slice(out.Changes, func(i, j int) bool { return out.Changes[i].Key < out.Changes[j].Key })
	for svc := range services {
		out.Services = append(out.Services, svc)
	}
	sort.Strings(out.Services)
	out.InvalidatesPlans = len(out.Changes) > 0
	if out.InvalidatesPlans {
		out.KnownEffects = append(out.KnownEffects, "保存会产生新配置版本；绑定旧配置版本的条件计划需重新核验。")
	}
	if services["risk"] {
		out.KnownEffects = append(out.KnownEffects, "风控规则将在服务实际加载后用于新检查；关闭准入会使高于L1的模式收缩。")
	}
	if services["market"] {
		out.KnownEffects = append(out.KnownEffects, "盘中行情引擎保留当前参数，下一次预热才加载；保存不会触发进程重启。")
	}
	return out
}
func (g *Gate) config(w http.ResponseWriter, r *http.Request) {
	principal, ok := g.principal(r)
	if !ok {
		writeErr(w, 401, "未登录")
		return
	}
	if g.Params == nil || !g.Params.Enabled() {
		writeErr(w, 503, "参数表不可用")
		return
	}
	store, ok := g.Params.(RevisionStore)
	if !ok {
		writeErr(w, 503, "配置版本存储未就绪")
		return
	}
	current, err := store.ReadConfig(r.Context())
	if err != nil {
		writeErr(w, 503, "参数读取失败")
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/api/config" {
		g.writeConfig(w, r, store, current)
		return
	}
	if principal.Role == "viewer" {
		writeErr(w, 403, "当前是查看角色，不能改配置")
		return
	}
	isPreview := r.URL.Path == "/api/config/preview" && r.Method == http.MethodPost
	isRollback := r.URL.Path == "/api/config/rollback" && r.Method == http.MethodPost
	if !isPreview && !isRollback && !(r.URL.Path == "/api/config" && r.Method == http.MethodPut) {
		writeErr(w, 405, "不支持的方法")
		return
	}
	var req struct {
		Base   *int              `json:"base_revision"`
		Values map[string]string `json:"values"`
		Target *int              `json:"target_revision"`
	}
	d := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	d.DisallowUnknownFields()
	if d.Decode(&req) != nil {
		writeErr(w, 400, "请求不是有效配置JSON")
		return
	}
	// Legacy clients may use If-Match from GET's ETag. Omitting both is never blind-write.
	if req.Base == nil {
		if raw := strings.Trim(r.Header.Get("If-Match"), "\""); raw != "" {
			if v, e := strconv.Atoi(raw); e == nil {
				req.Base = &v
			}
		}
	}
	if req.Base == nil {
		writeErr(w, 428, "保存需base_revision，请重新读取配置后提交")
		return
	}
	if *req.Base != current.Revision {
		writeErr(w, 409, "配置已被其他窗口或服务修改，请重新读取并核对变更")
		return
	}
	values := req.Values
	replace := req.Target != nil
	if replace {
		if *req.Target <= 0 {
			writeErr(w, 400, "回滚目标版本无效")
			return
		}
		target, e := store.VersionConfig(r.Context(), *req.Target)
		if e != nil {
			writeErr(w, 404, "历史版本不存在")
			return
		}
		values = target.Values
	}
	if isRollback && !replace {
		writeErr(w, 400, "缺少回滚目标版本")
		return
	}
	// Validate only changed registered keys; unchanged historical keys are preserved.
	patch := map[string]string{}
	for k, v := range values {
		if current.Values[k] != v {
			patch[k] = v
		}
	}
	if replace {
		for k := range current.Values {
			if _, ok := values[k]; !ok {
				patch[k] = ""
			}
		}
	}
	if len(patch) > 0 {
		if patch, err = params.ValidatePatch(current.Values, patch); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
	}
	if !replace {
		values = patch
	}
	preview := previewConfig(current, values, replace)
	if isPreview {
		writeJSON(w, 200, preview)
		return
	}
	if len(preview.Changes) == 0 {
		writeErr(w, 400, "没有配置变更")
		return
	}
	keys := make([]string, 0, len(patch))
	for k := range patch {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if err = g.audit(r.Context(), Entry{Actor: principal.Name, Role: principal.Role, Action: "config.save", Target: clip(strings.Join(keys, ","), 256), OK: true, Detail: "attempt base_revision=" + strconv.Itoa(*req.Base)}); err != nil {
		writeErr(w, 503, "审计写入失败")
		return
	}
	var saved params.Snapshot
	if isRollback {
		saved, err = store.RollbackConfig(r.Context(), *req.Base, *req.Target)
	} else if replace {
		writeErr(w, 400, "回滚须使用rollback接口")
		return
	} else {
		saved, err = store.SaveConfig(r.Context(), *req.Base, patch)
	}
	if errors.Is(err, params.ErrConflict) {
		writeErr(w, 409, "配置版本冲突，请重新读取")
		return
	}
	if err != nil {
		writeErr(w, 503, "配置保存失败")
		return
	}
	g.writeConfig(w, r, store, saved)
}
func (g *Gate) writeConfig(w http.ResponseWriter, r *http.Request, store RevisionStore, s params.Snapshot) {
	out := configPublic(s)
	// Every registered reader observes the same global snapshot version. Include
	// readers whose last override was removed, so clear/rollback still waits for
	// their real load acknowledgement instead of silently dropping the target.
	targets := map[string]bool{"datahub": true, "market": true, "intel": true, "brain": true, "strategy": true, "risk": true, "notify": true}
	for key := range s.Values {
		if target, _ := params.Target(key); target != "unknown" {
			targets[target] = true
		}
	}
	names := make([]string, 0, len(targets))
	for target := range targets {
		names = append(names, target)
	}
	sort.Strings(names)
	out["target_services"] = names
	receipts, err := store.ConfigReceipts(r.Context())
	if err == nil {
		out["receipts"] = receipts
	} else {
		out["receipt_error"] = "回执读取失败"
	}
	history, err := store.ConfigHistory(r.Context())
	if err == nil {
		out["history"] = history
	}
	w.Header().Set("ETag", strconv.Quote(strconv.Itoa(s.Revision)))
	writeJSON(w, 200, out)
}
