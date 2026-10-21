package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type memRecorder struct {
	mu   sync.Mutex
	rows []CallRecord
}

func (m *memRecorder) Record(_ context.Context, rec CallRecord) error {
	m.mu.Lock()
	m.rows = append(m.rows, rec)
	m.mu.Unlock()
	return nil
}

func (m *memRecorder) statuses() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.rows))
	for i, r := range m.rows {
		out[i] = r.Status
	}
	return out
}

func fakeVLLM(t *testing.T, handle func(body chatBody) (int, string)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metrics" {
			fmt.Fprintln(w, "# HELP vllm:gpu_cache_usage_perc x")
			fmt.Fprintln(w, `vllm:gpu_cache_usage_perc{model_name="qwen"} 0.42`)
			fmt.Fprintln(w, `vllm:num_requests_running{model_name="qwen"} 3`)
			return
		}
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		var body chatBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode: %v", err)
		}
		code, content := handle(body)
		if code != http.StatusOK {
			http.Error(w, content, code)
			return
		}
		rep := map[string]any{
			"model":   body.Model + "-served",
			"choices": []any{map[string]any{"message": map[string]any{"content": content}}},
			"usage":   map[string]any{"prompt_tokens": 11, "completion_tokens": 7},
		}
		_ = json.NewEncoder(w).Encode(rep)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestChatRecordsAndValidates(t *testing.T) {
	srv := fakeVLLM(t, func(body chatBody) (int, string) {
		if body.ResponseFormat["type"] != "json_object" {
			t.Errorf("response_format = %v", body.ResponseFormat)
		}
		return 200, "```json\n{\"score\": 70}\n```"
	})
	rec := &memRecorder{}
	g := New(map[Tier]ModelConfig{Small: {BaseURL: srv.URL + "/v1", Model: "qwen"}}, rec)
	id := 9
	resp, err := g.Chat(context.Background(), Request{
		Task: "technical", Tier: Small, TraceID: "t1", PromptVersion: "technical@v1", DecisionID: &id,
		Messages: []Message{{Role: "system", Content: "sys"}, {Role: "user", Content: "facts"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ExtractJSON(resp.Content) != `{"score": 70}` {
		t.Fatalf("extract = %q", ExtractJSON(resp.Content))
	}
	_, err = g.Chat(context.Background(), Request{
		Task: "technical", Tier: Small, Messages: []Message{{Role: "user", Content: "x"}},
		Validate: func(string) error { return errors.New("score missing") },
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("want ErrInvalid, got %v", err)
	}
	got := rec.statuses()
	if len(got) != 2 || got[0] != "ok" || got[1] != "invalid" {
		t.Fatalf("statuses = %v", got)
	}
	r := rec.rows[0]
	if r.Model != "qwen-served" || r.PromptTokens != 11 || r.InputDigest == "" || r.InputExcerpt != "facts\n" || *r.DecisionID != 9 {
		t.Fatalf("record = %+v", r)
	}
}

func TestUnconfiguredAndBreaker(t *testing.T) {
	rec := &memRecorder{}
	var hits atomic.Int32
	srv := fakeVLLM(t, func(chatBody) (int, string) {
		hits.Add(1)
		return 503, "loading"
	})
	g := New(map[Tier]ModelConfig{Large: {BaseURL: srv.URL + "/v1", Model: "big"}, Small: {}}, rec)
	if _, err := g.Chat(context.Background(), Request{Task: "x", Tier: Small}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unconfigured: %v", err)
	}
	for i := 0; i < breakerThreshold; i++ {
		if _, err := g.Chat(context.Background(), Request{Task: "x", Tier: Large}); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("503 should be unavailable: %v", err)
		}
	}
	if _, err := g.Chat(context.Background(), Request{Task: "x", Tier: Large}); err == nil || !strings.Contains(err.Error(), "circuit open") {
		t.Fatalf("want circuit open, got %v", err)
	}
	if hits.Load() != breakerThreshold {
		t.Fatalf("server hits = %d", hits.Load())
	}
	st := g.Stats(context.Background())
	if len(st) != 2 || st[0].Tier != Large || !st[0].CircuitOpen || st[0].KVCacheUsage != 0.42 || st[0].Running != 3 || st[0].Waiting != -1 {
		t.Fatalf("stats = %+v", st)
	}
	if st[1].Configured || st[1].KVCacheUsage != -1 {
		t.Fatalf("small stats = %+v", st[1])
	}
}

func TestResolveAPIKey(t *testing.T) {
	t.Setenv("NT_LLM_TEST_KEY", "sk-env-real")
	t.Setenv("NT_LLM_TEST_EMPTY", "")
	cases := []struct {
		name    string
		raw     string
		want    string
		wantErr string // 为空表示不应报错
	}{
		{"空密钥保持无鉴权", "", "", ""},
		{"明文照旧", "sk-plain", "sk-plain", ""},
		{"env 取真值", "env:NT_LLM_TEST_KEY", "sk-env-real", ""},
		{"env 变量名允许空格", "env: NT_LLM_TEST_KEY", "sk-env-real", ""},
		{"env 变量未设置显式报错", "env:NT_LLM_MISSING", "", "ErrUnavailable"},
		{"env 变量为空显式报错", "env:NT_LLM_TEST_EMPTY", "", "ErrUnavailable"},
		{"env 缺变量名报错", "env:", "", "ErrUnavailable"},
		{"enc 识别但解密器未配置", "enc:Y2lwaGVydGV4dA==", "", "enc"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ResolveAPIKey(c.raw)
			switch c.wantErr {
			case "":
				if err != nil {
					t.Fatalf("err = %v", err)
				}
				if got != c.want {
					t.Fatalf("got = %q, want %q", got, c.want)
				}
			case "ErrUnavailable":
				if !errors.Is(err, ErrUnavailable) {
					t.Fatalf("want ErrUnavailable, got %v", err)
				}
			case "enc":
				if !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "解密器未配置") {
					t.Fatalf("want 解密器未配置, got %v", err)
				}
				// 错误会进 llm_call_log，绝不回显密文本身。
				if strings.Contains(err.Error(), "Y2lwaGVydGV4dA==") {
					t.Fatalf("error must not echo the ciphertext: %v", err)
				}
			}
		})
	}
}

func TestPostResolvesKeyAndJSONMode(t *testing.T) {
	t.Setenv("NT_LLM_TEST_KEY", "sk-env-real")
	type captured struct {
		auth string
		body string
	}
	var mu sync.Mutex
	var got []captured
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, captured{auth: r.Header.Get("Authorization"), body: string(raw)})
		mu.Unlock()
		rep := map[string]any{
			"model":   "m-served",
			"choices": []any{map[string]any{"message": map[string]any{"content": "{}"}}},
		}
		_ = json.NewEncoder(w).Encode(rep)
	}))
	t.Cleanup(srv.Close)
	chat := func(cfg ModelConfig) {
		t.Helper()
		g := New(map[Tier]ModelConfig{Small: cfg}, nil)
		if _, err := g.Chat(context.Background(), Request{Task: "x", Tier: Small, Messages: []Message{{Role: "user", Content: "hi"}}}); err != nil {
			t.Fatal(err)
		}
	}
	url := srv.URL + "/v1"
	chat(ModelConfig{BaseURL: url, Model: "qwen", APIKey: "env:NT_LLM_TEST_KEY"})
	chat(ModelConfig{BaseURL: url, Model: "qwen", APIKey: "sk-plain", DisableJSONMode: true})
	chat(ModelConfig{BaseURL: url, Model: "qwen"})
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 3 {
		t.Fatalf("calls = %d", len(got))
	}
	// 默认路径：env: 解析出真值鉴权，且带 response_format。
	if got[0].auth != "Bearer sk-env-real" {
		t.Fatalf("auth = %q", got[0].auth)
	}
	if !strings.Contains(got[0].body, `"response_format"`) {
		t.Fatalf("default body missing response_format: %s", got[0].body)
	}
	// DisableJSONMode：请求体不带 response_format，明文密钥照旧。
	if got[1].auth != "Bearer sk-plain" {
		t.Fatalf("auth = %q", got[1].auth)
	}
	if strings.Contains(got[1].body, "response_format") {
		t.Fatalf("disable_json_mode body must drop response_format: %s", got[1].body)
	}
	// 空密钥：无鉴权头，JSON 模式默认开。
	if got[2].auth != "" {
		t.Fatalf("empty key must not set auth header, got %q", got[2].auth)
	}
	if !strings.Contains(got[2].body, `"response_format"`) {
		t.Fatalf("body missing response_format: %s", got[2].body)
	}
}

func TestEncKeyFailsBeforeRequest(t *testing.T) {
	var hits atomic.Int32
	srv := fakeVLLM(t, func(chatBody) (int, string) {
		hits.Add(1)
		return 200, "{}"
	})
	rec := &memRecorder{}
	g := New(map[Tier]ModelConfig{Small: {BaseURL: srv.URL + "/v1", Model: "qwen", APIKey: "enc:ciphertext"}}, rec)
	_, err := g.Chat(context.Background(), Request{Task: "x", Tier: Small, Messages: []Message{{Role: "user", Content: "hi"}}})
	if !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "解密器未配置") {
		t.Fatalf("want unavailable+解密器未配置, got %v", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("enc: key was sent as plaintext, server hits = %d", hits.Load())
	}
	if st := rec.statuses(); len(st) != 1 || st[0] != "unavailable" {
		t.Fatalf("statuses = %v", st)
	}
}

func TestTimeoutIsUnavailable(t *testing.T) {
	srv := fakeVLLM(t, func(chatBody) (int, string) {
		time.Sleep(200 * time.Millisecond)
		return 200, "{}"
	})
	g := New(map[Tier]ModelConfig{Small: {BaseURL: srv.URL + "/v1", Timeout: 50 * time.Millisecond}}, nil)
	if _, err := g.Chat(context.Background(), Request{Task: "x", Tier: Small}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("want unavailable, got %v", err)
	}
}

func TestLimiterPriorityAndQueueFull(t *testing.T) {
	l := newLimiter(1, 3)
	ctx := context.Background()
	if err := l.acquire(ctx, Review); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var order []Priority
	var wg sync.WaitGroup
	start := func(p Priority) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := l.acquire(ctx, p); err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			order = append(order, p)
			mu.Unlock()
			l.release()
		}()
	}
	for i, p := range []Priority{NewsBatch, Briefing, PositionRisk} {
		start(p)
		waitQueued(t, l, i+1)
	}
	if err := l.acquire(ctx, Intraday); !errors.Is(err, errQueueFull) {
		t.Fatalf("want queue full, got %v", err)
	}
	l.release()
	wg.Wait()
	want := []Priority{PositionRisk, Briefing, NewsBatch}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
	if in, q := l.load(); in != 0 || q != 0 {
		t.Fatalf("load = %d %d", in, q)
	}
}

func waitQueued(t *testing.T, l *limiter, n int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, q := l.load(); q == n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("queue did not reach %d", n)
}

func TestLimiterCancelWhileQueued(t *testing.T) {
	l := newLimiter(1, 4)
	if err := l.acquire(context.Background(), Review); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := l.acquire(ctx, Intraday); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want deadline, got %v", err)
	}
	l.release()
	if in, q := l.load(); in != 0 || q != 0 {
		t.Fatalf("load = %d %d", in, q)
	}
}

func TestParseMetrics(t *testing.T) {
	m := parseMetrics(strings.NewReader("# c\nvllm:kv_cache_usage_perc{model_name=\"a b\",x=\"1\"} 0.25\nvllm:kv_cache_usage_perc{model_name=\"c\"} 0.25\nbad line\n"))
	if m["vllm:kv_cache_usage_perc"] != 0.5 {
		t.Fatalf("metrics = %v", m)
	}
}
