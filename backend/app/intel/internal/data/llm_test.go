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
	"time"

	"server/conf"
	"server/pkg/events"
	"server/pkg/llm"
)

func TestNewModelDisabled(t *testing.T) {
	if NewModel(nil, nil) != nil {
		t.Fatal("nil config should not build a model")
	}
	if NewModel(&conf.Intel{Llm: &conf.IntelLLM{Enabled: false, BaseUrl: "http://127.0.0.1"}}, nil) != nil {
		t.Fatal("disabled llm should not build a model")
	}
}

func TestGatewayModelChat(t *testing.T) {
	var got struct {
		Model    string `json:"model"`
		Messages []struct {
			Role string `json:"role"`
		} `json:"messages"`
		Format map[string]string `json:"response_format"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("auth = %q", r.Header.Get("Authorization"))
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got)
		_, _ = w.Write([]byte(`{"model":"qwen-small","choices":[{"message":{"content":"{\"sentiment\":0.2}"}}]}`))
	}))
	defer srv.Close()

	m := &gatewayModel{gw: llm.New(map[llm.Tier]llm.ModelConfig{
		llm.Small: {BaseURL: srv.URL + "/v1", Model: "qwen-small", APIKey: "k", Timeout: time.Second, MaxTokens: 300},
	}, nil)}
	ctx := events.WithTraceID(context.Background(), "trace-1")
	out, err := m.Complete(ctx, "small", "system", "user")
	if err != nil {
		t.Fatal(err)
	}
	if out == "" || got.Model != "qwen-small" || len(got.Messages) != 2 || got.Format["type"] != "json_object" {
		t.Fatalf("reply %q request %+v", out, got)
	}
}

// TestLiveOverlayDisableJSONMode 钉测试（M13-T06b）：appcfg 置 app.intel.disable_json_mode=true
// 后发出的请求体不含 response_format；false 显式盖回 conf；未设定（空串）保持 conf 值。
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
		gw.SetLive(liveOverlay(func(_ context.Context, key string) string {
			if key == "app.intel.disable_json_mode" {
				return appValue
			}
			return ""
		}))
		if _, err := gw.Chat(context.Background(), llm.Request{
			Task: "intel.score", Tier: tier, Messages: []llm.Message{{Role: "user", Content: "news"}},
		}); err != nil {
			t.Fatal(err)
		}
	}

	chat(llm.Small, false, "true") // appcfg 关 JSON 模式 → 请求体不得带 response_format
	chat(llm.Small, true, "false") // appcfg=false 显式盖回 conf=true → 恢复默认带
	chat(llm.Large, true, "")      // 未设定（空=清除）→ 保持 conf=true → 不带

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 3 {
		t.Fatalf("calls = %d", len(bodies))
	}
	for i, wantOff := range []bool{true, false, true} {
		has := strings.Contains(bodies[i], "response_format")
		if has == wantOff {
			t.Fatalf("body[%d] response_format present=%v wantOff=%v: %s", i, has, wantOff, bodies[i])
		}
	}
}
