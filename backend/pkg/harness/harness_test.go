package harness

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ⚠️ 永不删除：恒阻断契约测试（对齐 HCF ADR-AGT-01 同构纪律）。
// blockedClient 是「契约未决≠可用」的最后防线——它保证 token 未配置、
// driver 未切 http、底座契约未实机验证时，任何调用都以哨兵错误如实呈现，
// 而不是静默返回空数据。删除本测试=拆除防线，评审必须拦截。
func TestBlockedClient_ContractNeverRemoved(t *testing.T) {
	c := &blockedClient{reason: "driver=blocked"}
	ctx := context.Background()

	checks := map[string]error{}
	_, errH := c.Health(ctx)
	checks["Health"] = errH
	_, errA := c.ListAgents(ctx)
	checks["ListAgents"] = errA
	_, errS := c.SubmitRun(ctx, SubmitReq{GraphID: "g", IdempotencyKey: "k"})
	checks["SubmitRun"] = errS
	_, errG := c.GetRun(ctx, "r")
	checks["GetRun"] = errG
	_, errL := c.ListRuns(ctx, "g", "", "", 0)
	checks["ListRuns"] = errL
	checks["CancelRun"] = c.CancelRun(ctx, "r")
	checks["ResumeRun"] = c.ResumeRun(ctx, "r", "i", ApprovalDecision{Approved: true})

	for name, err := range checks {
		if err == nil {
			t.Fatalf("%s: blocked 实现必须返回错误", name)
		}
		if !errors.Is(err, ErrHarnessContractUnverified) {
			t.Fatalf("%s: 错误必须是 ErrHarnessContractUnverified 哨兵, got %v", name, err)
		}
		if !strings.Contains(err.Error(), "driver=blocked") {
			t.Fatalf("%s: 错误应如实带原因, got %v", name, err)
		}
	}
}

func TestConfNormalizeValidate(t *testing.T) {
	var c Conf
	c.Normalize()
	if c.Driver != DriverBlocked {
		t.Fatalf("缺省 driver 必须为 blocked, got %q", c.Driver)
	}
	if c.TokenRef != "env:NT_ARANEA_TOKEN" {
		t.Fatalf("缺省 token_ref 应为 env 引用, got %q", c.TokenRef)
	}
	if c.Timeout != 30*time.Second {
		t.Fatalf("缺省 timeout 应为 30s, got %s", c.Timeout)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("blocked 默认配置应合法: %v", err)
	}

	c = Conf{Driver: "http", URL: ""}
	c.Normalize()
	if err := c.Validate(); err == nil {
		t.Fatal("http 缺 url 必须 fail-fast")
	}

	c = Conf{Driver: "http", URL: "ftp://x"}
	c.Normalize()
	if err := c.Validate(); err == nil {
		t.Fatal("非 http(s) url 必须 fail-fast")
	}

	c = Conf{Driver: "bogus", URL: "http://x"}
	c.Normalize()
	if err := c.Validate(); err == nil {
		t.Fatal("词表外 driver 必须 fail-fast")
	}

	c = Conf{Driver: " HTTP ", URL: "http://192.168.4.44:8810/"}
	c.Normalize()
	if c.Driver != "http" || c.URL != "http://192.168.4.44:8810" {
		t.Fatalf("Normalize 应小写 driver 并去尾斜杠, got %+v", c)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("合法 http 配置被拒: %v", err)
	}
}

func TestNewClientFallback(t *testing.T) {
	resolveOK := func(string) (string, error) { return "tok", nil }
	resolveFail := func(string) (string, error) { return "", errors.New("env NT_ARANEA_TOKEN 未设置") }
	resolveEmpty := func(string) (string, error) { return "", nil }

	cases := []struct {
		name    string
		conf    Conf
		resolve func(string) (string, error)
		wantBlk bool
	}{
		{"driver=blocked 恒阻断", Conf{Driver: "blocked"}, resolveOK, true},
		{"http+token 解析失败→blocked", Conf{Driver: "http", URL: "http://x"}, resolveFail, true},
		{"http+token 空→blocked", Conf{Driver: "http", URL: "http://x"}, resolveEmpty, true},
		{"http+无 resolver→blocked", Conf{Driver: "http", URL: "http://x"}, nil, true},
		{"http+token 正常→http 适配器", Conf{Driver: "http", URL: "http://x"}, resolveOK, false},
		{"非法配置→blocked", Conf{Driver: "bogus"}, resolveOK, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cl := NewClient(tc.conf, tc.resolve)
			_, err := cl.Health(context.Background())
			if tc.wantBlk {
				if !errors.Is(err, ErrHarnessContractUnverified) {
					t.Fatalf("want blocked sentinel, got %v", err)
				}
				return
			}
			// http 适配器对不存在的服务器应报不可达（而非恒阻断）。
			if err == nil || errors.Is(err, ErrHarnessContractUnverified) {
				t.Fatalf("want http adapter (unavailable), got %v", err)
			}
			if !errors.Is(err, ErrHarnessUnavailable) {
				t.Fatalf("want unavailable sentinel, got %v", err)
			}
		})
	}
}

// TestHTTPClientContract httptest 对拍机器面契约：Bearer 头、请求体键名、
// 响应解码、状态码哨兵映射。
func TestHTTPClientContract(t *testing.T) {
	var gotAuth, gotMethod, gotPath, gotQuery string
	var gotBody []byte
	mux := http.NewServeMux()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
		if r.Body != nil {
			gotBody, _ = io.ReadAll(r.Body)
		}
		mux.ServeHTTP(w, r)
	}))
	defer srv.Close()

	mux.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "healthy", "version": "dev", "uptime_seconds": 3,
			"agent_count": 12, "graph_count": 5, "model_count": 2,
		})
	})
	mux.HandleFunc("GET /api/v1/agents", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{
			{"id": "a1", "name": "ops-lead", "description": "d"},
		}})
	})
	mux.HandleFunc("POST /api/v1/runs", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"run_id": "run-1"})
	})
	mux.HandleFunc("GET /api/v1/runs/run-1", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"run_id": "run-1", "graph_id": "g1", "status": "succeeded",
			"started_at": "2026-11-02T01:00:00Z", "finished_at": "2026-11-02T01:00:05Z",
			"output": "done", "duration_ms": 5000, "model_used": "qwen3.8-flash-next",
			"tokens_input": 100, "tokens_output": 42,
			"nodes": []map[string]any{{"node_id": "n1", "status": "succeeded"}},
		})
	})
	mux.HandleFunc("GET /api/v1/runs", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items":       []map[string]any{{"run_id": "run-1", "graph_id": "g1", "status": "succeeded"}},
			"next_cursor": "",
		})
	})
	mux.HandleFunc("POST /api/v1/runs/run-1/cancel", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})

	cl := NewClient(Conf{Driver: "http", URL: srv.URL, Timeout: 5 * time.Second},
		func(string) (string, error) { return "tok-nt", nil })
	ctx := context.Background()

	h, err := cl.Health(ctx)
	if err != nil || h.AgentCount != 12 || h.ModelCount != 2 {
		t.Fatalf("health wrong: %+v err=%v", h, err)
	}
	if gotAuth != "Bearer tok-nt" {
		t.Fatalf("Bearer 头缺失/错误: %q", gotAuth)
	}

	agents, err := cl.ListAgents(ctx)
	if err != nil || len(agents) != 1 || agents[0].ID != "a1" {
		t.Fatalf("agents wrong: %+v err=%v", agents, err)
	}

	resp, err := cl.SubmitRun(ctx, SubmitReq{
		GraphID: "g1", Input: "hi", IdempotencyKey: "nt-biz-1",
		WebhookURL: "http://192.168.4.44:9302/hooks/nt", WebhookSecret: "s3",
	})
	if err != nil || resp.RunID != "run-1" {
		t.Fatalf("submit wrong: %+v err=%v", resp, err)
	}
	var payload map[string]any
	if err := json.Unmarshal(gotBody, &payload); err != nil {
		t.Fatalf("submit payload unmarshal: %v", err)
	}
	if payload["graph_id"] != "g1" || payload["idempotency_key"] != "nt-biz-1" ||
		payload["webhook_url"] == "" || payload["webhook_secret"] != "s3" {
		t.Fatalf("submit payload keys wrong: %v", payload)
	}

	detail, err := cl.GetRun(ctx, "run-1")
	if err != nil || detail.Status != "succeeded" || detail.TokensOutput != 42 || len(detail.Nodes) != 1 {
		t.Fatalf("get run wrong: %+v err=%v", detail, err)
	}
	if !Terminal(detail.Status) {
		t.Fatal("succeeded must be terminal")
	}
	if Terminal("waiting_approval") || Terminal("running") {
		t.Fatal("waiting_approval/running must NOT be terminal")
	}

	page, err := cl.ListRuns(ctx, "g1", "succeeded", "", 50)
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("list runs wrong: %+v err=%v", page, err)
	}
	if !strings.Contains(gotQuery, "graph_id=g1") || !strings.Contains(gotQuery, "status=succeeded") {
		t.Fatalf("list query wrong: %q", gotQuery)
	}

	if err := cl.CancelRun(ctx, "run-1"); err != nil {
		t.Fatalf("cancel wrong: %v", err)
	}
	_ = gotMethod
	_ = gotPath
}

// TestHTTPClientErrorMapping 状态码→哨兵映射 + 底座错误文案透传。
func TestHTTPClientErrorMapping(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   error
	}{
		{http.StatusBadRequest, `{"error":"webhook_url host \"evil\" not in allowlist"}`, ErrHarnessBadRequest},
		{http.StatusUnauthorized, `{"error":"invalid api token"}`, ErrHarnessUnavailable},
		{http.StatusNotFound, `{"error":"run not found"}`, ErrHarnessNotFound},
		{http.StatusInternalServerError, `{"error":"boom"}`, ErrHarnessUnavailable},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		}))
		cl := NewClient(Conf{Driver: "http", URL: srv.URL, Timeout: 5 * time.Second},
			func(string) (string, error) { return "tok", nil })
		_, err := cl.Health(context.Background())
		if !errors.Is(err, tc.want) {
			t.Fatalf("status %d: want %v, got %v", tc.status, tc.want, err)
		}
		if tc.status == http.StatusBadRequest && !strings.Contains(err.Error(), "allowlist") {
			t.Fatalf("底座错误文案应如实透传, got %v", err)
		}
		srv.Close()
	}
}

// TestSubmitRunClientSideValidation 必填纪律：缺 graph_id/idempotency_key
// 在客户端侧即拒（不发请求）。
func TestSubmitRunClientSideValidation(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits++ }))
	defer srv.Close()
	cl := NewClient(Conf{Driver: "http", URL: srv.URL, Timeout: 5 * time.Second},
		func(string) (string, error) { return "tok", nil })
	ctx := context.Background()

	if _, err := cl.SubmitRun(ctx, SubmitReq{IdempotencyKey: "k"}); !errors.Is(err, ErrHarnessBadRequest) {
		t.Fatalf("缺 graph_id 必须 400 哨兵, got %v", err)
	}
	if _, err := cl.SubmitRun(ctx, SubmitReq{GraphID: "g"}); !errors.Is(err, ErrHarnessBadRequest) {
		t.Fatalf("缺 idempotency_key 必须 400 哨兵, got %v", err)
	}
	if hits != 0 {
		t.Fatalf("客户端校验失败时不应发出请求, hits=%d", hits)
	}
}
