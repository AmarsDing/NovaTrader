package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
