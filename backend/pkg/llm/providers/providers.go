// Package providers 是 LLM 供应商坑位登记册（vibe providers/*.json 模板法移植，gap ㊘ / S9）。
//
// 纪律：
//   - 每家供应商一个 JSON：id/base_url/env_key/default_model + known_incompatibilities
//   - notes + verified_at/verified_scope 分级——verified_at 为空即「未实测」，
//     未实测档不得进配置默认、不得进生产启用档。
//   - 坑只登记在这（disable_json_mode、reasoning_content 回传、json_object 兼容性、
//     代理层 stage2 等），禁止散在代码注释与记忆里——pkg/llm/llm.go 的
//     DisableJSONMode 注释与本册相互引用，改一边必须同步另一边。
//   - 实测口径：usage 记账 + llm_call_log 全字段 + 降级标注可见；只看
//     stats.configured 不算数（S5 修复前它只读启动配置）。
package providers

import (
	"embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

//go:embed *.json
var files embed.FS

// Provider 是一家供应商的坑位登记。VerifiedAt 为空的条目属未实测分级。
type Provider struct {
	ID                     string   `json:"id"`
	Name                   string   `json:"name"`
	BaseURL                string   `json:"base_url"`
	EnvKey                 string   `json:"env_key"`
	DefaultModel           string   `json:"default_model"`
	ContextLimitTokens     *int     `json:"context_limit_tokens"`
	VerifiedAt             string   `json:"verified_at"`
	VerifiedScope          string   `json:"verified_scope"`
	KnownIncompatibilities []string `json:"known_incompatibilities"`
	Notes                  []string `json:"notes"`
}

// Verified 表示该供应商已经过了活验收（VerifiedAt 非空且含实测日期）。
func (p Provider) Verified() bool { return strings.TrimSpace(p.VerifiedAt) != "" }

var registry = mustLoad()

func mustLoad() map[string]Provider {
	ents, err := files.ReadDir(".")
	if err != nil {
		panic(fmt.Errorf("llm/providers: read dir: %w", err))
	}
	out := map[string]Provider{}
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := files.ReadFile(e.Name())
		if err != nil {
			panic(fmt.Errorf("llm/providers: read %s: %w", e.Name(), err))
		}
		var p Provider
		if err := json.Unmarshal(raw, &p); err != nil {
			panic(fmt.Errorf("llm/providers: parse %s: %w", e.Name(), err))
		}
		p.ID = strings.TrimSpace(p.ID)
		if p.ID == "" {
			panic(fmt.Errorf("llm/providers: %s missing id", e.Name()))
		}
		if _, dup := out[p.ID]; dup {
			panic(fmt.Errorf("llm/providers: duplicate id %q", p.ID))
		}
		out[p.ID] = p
	}
	return out
}

// All 返回全量登记（按 id 排序，只读视图）。
func All() []Provider {
	ids := make([]string, 0, len(registry))
	for id := range registry {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]Provider, 0, len(ids))
	for _, id := range ids {
		out = append(out, registry[id])
	}
	return out
}

// Lookup 按供应商 id 查登记。
func Lookup(id string) (Provider, bool) {
	p, ok := registry[strings.TrimSpace(id)]
	return p, ok
}

// MatchURL 按 base_url（忽略大小写与末尾斜杠）反查登记，用于配置页/网关按地址侦坑。
func MatchURL(baseURL string) (Provider, bool) {
	norm := strings.ToLower(strings.TrimRight(strings.TrimSpace(baseURL), "/"))
	if norm == "" {
		return Provider{}, false
	}
	for _, p := range registry {
		if strings.ToLower(strings.TrimRight(p.BaseURL, "/")) == norm {
			return p, true
		}
	}
	return Provider{}, false
}

// Warns 返回该 base_url 对应的坑位提示：未实测或无登记都算「不知道」，
// 已实测且无坑返回空切片。调用方用来在启动/配置校验时打日志，不作硬拒。
func Warns(baseURL string) []string {
	p, ok := MatchURL(baseURL)
	if !ok {
		return []string{"供应商未进坑位登记册（pkg/llm/providers/）；启用前必须登记并按实测口径验收"}
	}
	if !p.Verified() {
		return append([]string{"供应商未实测（verified_at 为空），不得进生产启用档"}, p.KnownIncompatibilities...)
	}
	return p.KnownIncompatibilities
}
