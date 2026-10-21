package data

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"server/pkg/llm"
)

// TestLiveOverlayDisableJSONMode 钉测试（M13-T06b）：appcfg 置 disable_json_mode=true 后发出的
// 请求体不含 response_format；false 显式盖回 conf；未设定（空串）保持 conf；非法值保持现状。
func TestLiveOverlayDisableJSONMode(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(raw))
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":   "qwen-served",
			"choices": []any{map[string]any{"message": map[string]any{"content": "{}"}}},
		})
	}))
	defer srv.Close()

	chat := func(tier llm.Tier, confDisable bool, appValue string) {
		t.Helper()
		gw := llm.New(map[llm.Tier]llm.ModelConfig{
			tier: {BaseURL: srv.URL + "/v1", Model: "qwen", DisableJSONMode: confDisable},
		}, nil)
		get := func(_ context.Context, key string) string {
			prefix := "app.brain.small."
			if tier == llm.Large {
				prefix = "app.brain.large."
			}
			if key == prefix+"disable_json_mode" {
				return appValue
			}
			return ""
		}
		gw.SetLive(liveOverlay(get))
		if _, err := gw.Chat(context.Background(), llm.Request{
			Task: "technical", Tier: tier, Messages: []llm.Message{{Role: "user", Content: "facts"}},
		}); err != nil {
			t.Fatal(err)
		}
	}

	chat(llm.Small, false, "true") // appcfg 关 JSON 模式 → 请求体不得带 response_format
	chat(llm.Small, true, "false") // appcfg=false 显式盖回 conf=true → 恢复默认带 response_format
	chat(llm.Small, true, "")      // 未设定（空=清除）→ 保持 conf=true → 不带
	chat(llm.Small, true, "1")     // 非法值保持现状 → 保持 conf=true → 不带
	chat(llm.Large, false, "true") // large 档读 app.brain.large.disable_json_mode → 不带

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 5 {
		t.Fatalf("calls = %d", len(bodies))
	}
	for i, wantOff := range []bool{true, false, true, true, true} {
		has := strings.Contains(bodies[i], "response_format")
		if has == wantOff {
			t.Fatalf("body[%d] response_format present=%v wantOff=%v: %s", i, has, wantOff, bodies[i])
		}
	}
}
