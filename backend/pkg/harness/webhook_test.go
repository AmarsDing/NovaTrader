package harness

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// =============================================================================
// M14-T05/T06 钉死测试（doc/开发文档/M14-智能体底座对接 设计文档 §5 D6/D7）：
// T-WH-NT-01 验签 401 同面（缺 secret/解析故障/畸形 ts/越窗双侧/签名失配）；
// T-WH-NT-02 算法逐字节钉死 + handler 派发（run.completed 字段投影）；
// T-WH-NT-03 未知事件类型/畸形包络/缺必填 → 400；GET → 405；consume 错 → 500；
//             体超限 → 413；waiting_approval InterruptID 投影；
// T-WH-NT-04 ResumeRun 契约（路径/方法/请求体）+ 404/400 哨兵 + 必填不发请求。
// =============================================================================

func webhookTestSign(secret, tsHeader string, body []byte) string {
	// 独立计算钉死算法形状：消息 "<ts>\n<body>"，前缀 "v1="。
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(tsHeader + "\n"))
	mac.Write(body)
	return "v1=" + hex.EncodeToString(mac.Sum(nil))
}

type webhookHarnessNT struct {
	now        time.Time
	secret     string
	secretErr  error
	consumed   []WebhookEvent
	consumeErr error
}

func (h *webhookHarnessNT) handler() *WebhookHandler {
	wh := NewWebhookHandler(
		func(context.Context) (string, error) { return h.secret, h.secretErr },
		func(_ context.Context, evt *WebhookEvent) error {
			h.consumed = append(h.consumed, *evt)
			return h.consumeErr
		},
	)
	wh.now = func() time.Time { return h.now }
	return wh
}

func (h *webhookHarnessNT) signedRequest(t *testing.T, eventType string, payload map[string]any) *http.Request {
	t.Helper()
	env := WebhookEvent{
		EventID:    "evt-1",
		EventType:  eventType,
		OccurredAt: h.now.UTC().Format(time.RFC3339),
		RunID:      "run-1",
		GraphID:    "g1",
		Payload:    payload,
	}
	body, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	ts := strconv.FormatInt(h.now.Unix(), 10)
	req := httptest.NewRequest(http.MethodPost, "/hooks/nt", bytes.NewReader(body))
	req.Header.Set("X-Webhook-Timestamp", ts)
	req.Header.Set("X-Webhook-Signature", webhookTestSign(h.secret, ts, body))
	return req
}

func newWebhookHarnessNT() *webhookHarnessNT {
	return &webhookHarnessNT{
		now:    time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC),
		secret: "wsec-nt",
	}
}

// T-WH-NT-01 验签 401 同面：五种失败原因一律 401（防枚举）。
func TestWebhookVerify_401Unchanged(t *testing.T) {
	h := newWebhookHarnessNT()

	statusOf := func(req *http.Request) int {
		rec := httptest.NewRecorder()
		h.handler().ServeHTTP(rec, req)
		return rec.Code
	}

	// secret 未配置。
	h.secret = ""
	if got := statusOf(h.signedRequest(t, WebhookEventRunCompleted, nil)); got != http.StatusUnauthorized {
		t.Fatalf("missing secret must yield 401, got %d", got)
	}
	h.secret = "wsec-nt"

	// secret 读取故障。
	h.secretErr = errors.New("db down")
	if got := statusOf(h.signedRequest(t, WebhookEventRunCompleted, nil)); got != http.StatusUnauthorized {
		t.Fatalf("secret read failure must yield 401, got %d", got)
	}
	h.secretErr = nil

	// 签名失配 / 空签名。
	req := h.signedRequest(t, WebhookEventRunCompleted, nil)
	req.Header.Set("X-Webhook-Signature", "v1=deadbeef")
	if got := statusOf(req); got != http.StatusUnauthorized {
		t.Fatalf("signature mismatch must yield 401, got %d", got)
	}
	req = h.signedRequest(t, WebhookEventRunCompleted, nil)
	req.Header.Del("X-Webhook-Signature")
	if got := statusOf(req); got != http.StatusUnauthorized {
		t.Fatalf("empty signature must yield 401, got %d", got)
	}

	// 时间戳畸形 + 越窗双侧。
	req = h.signedRequest(t, WebhookEventRunCompleted, nil)
	req.Header.Set("X-Webhook-Timestamp", "not-a-ts")
	if got := statusOf(req); got != http.StatusUnauthorized {
		t.Fatalf("malformed timestamp must yield 401, got %d", got)
	}
	for _, d := range []time.Duration{WebhookMaxSkew + time.Minute, -(WebhookMaxSkew + time.Minute)} {
		hh := newWebhookHarnessNT()
		hh.now = h.now.Add(d)
		req = hh.signedRequest(t, WebhookEventRunCompleted, nil) // 按事件时刻签名
		// 验签 now 固定回主时刻 → 越窗。
		if got := statusOf(req); got != http.StatusUnauthorized {
			t.Fatalf("skew %s must yield 401, got %d", d, got)
		}
	}
}

// T-WH-NT-02 算法钉死 + 派发投影：run.completed 事件验签后原样进 consume。
func TestWebhookSignAndDispatch(t *testing.T) {
	// 算法形状钉死（独立 hmac 计算对比 SignV1）。
	body := []byte(`{"event_id":"e1"}`)
	ts := "1762094400"
	if got := SignV1("s3", ts, body); got != webhookTestSign("s3", ts, body) {
		t.Fatalf("SignV1 must match \"<ts>\\n<body>\" hmac-sha256 v1= shape")
	}
	if !strings.HasPrefix(SignV1("s3", ts, body), "v1=") {
		t.Fatal("signature must carry v1= prefix")
	}

	h := newWebhookHarnessNT()
	req := h.signedRequest(t, WebhookEventRunCompleted,
		map[string]any{"output": "done", "duration_ms": float64(4200)})
	rec := httptest.NewRecorder()
	h.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid event must be accepted, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("success body must be {\"ok\":true}, got %s", rec.Body.String())
	}
	if len(h.consumed) != 1 {
		t.Fatalf("consume must be called exactly once, got %d", len(h.consumed))
	}
	evt := h.consumed[0]
	if evt.EventID != "evt-1" || evt.EventType != WebhookEventRunCompleted ||
		evt.RunID != "run-1" || evt.GraphID != "g1" {
		t.Fatalf("envelope projection wrong: %+v", evt)
	}
	if evt.Payload["duration_ms"] != float64(4200) {
		t.Fatalf("payload must pass through, got %+v", evt.Payload)
	}
}

// T-WH-NT-03 契约漂移/方法/体限/消费方故障分流。
func TestWebhookHandler_BadRequestsAndFaults(t *testing.T) {
	h := newWebhookHarnessNT()
	statusOf := func(req *http.Request) int {
		rec := httptest.NewRecorder()
		h.handler().ServeHTTP(rec, req)
		return rec.Code
	}

	// GET → 405 + Allow 头。
	rec := httptest.NewRecorder()
	h.handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hooks/nt", nil))
	if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != http.MethodPost {
		t.Fatalf("GET must yield 405 + Allow: POST, got %d %q", rec.Code, rec.Header().Get("Allow"))
	}

	// 词表外事件类型 → 400（契约漂移不猜）。
	if got := statusOf(h.signedRequest(t, "run.whatever", nil)); got != http.StatusBadRequest {
		t.Fatalf("unknown event type must yield 400, got %d", got)
	}

	// 缺必填字段（run_id）→ 400。
	req := h.signedRequest(t, WebhookEventRunCompleted, nil)
	var env map[string]any
	b, _ := io.ReadAll(req.Body)
	_ = json.Unmarshal(b, &env)
	delete(env, "run_id")
	nb, _ := json.Marshal(env)
	ts := strconv.FormatInt(h.now.Unix(), 10)
	req = httptest.NewRequest(http.MethodPost, "/hooks/nt", bytes.NewReader(nb))
	req.Header.Set("X-Webhook-Timestamp", ts)
	req.Header.Set("X-Webhook-Signature", webhookTestSign(h.secret, ts, nb))
	if got := statusOf(req); got != http.StatusBadRequest {
		t.Fatalf("missing run_id must yield 400, got %d", got)
	}

	// 畸形 JSON（验签过的坏体）→ 400。
	malformed := []byte("{")
	req = httptest.NewRequest(http.MethodPost, "/hooks/nt", bytes.NewReader(malformed))
	req.Header.Set("X-Webhook-Timestamp", ts)
	req.Header.Set("X-Webhook-Signature", webhookTestSign(h.secret, ts, malformed))
	if got := statusOf(req); got != http.StatusBadRequest {
		t.Fatalf("malformed envelope must yield 400, got %d", got)
	}

	// 体超限 → 413。
	big := bytes.Repeat([]byte("x"), webhookBodyLimit+2)
	req = httptest.NewRequest(http.MethodPost, "/hooks/nt", bytes.NewReader(big))
	if got := statusOf(req); got != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body must yield 413, got %d", got)
	}

	// consume 故障 → 500（如实上抛，不塌成签收成功）。
	h.consumeErr = errors.New("store down")
	if got := statusOf(h.signedRequest(t, WebhookEventRunCompleted, nil)); got != http.StatusInternalServerError {
		t.Fatalf("consume failure must yield 500, got %d", got)
	}
}

// T-WH-NT-03b waiting_approval InterruptID 投影（审批回路消费方取值面）。
func TestWebhookEvent_InterruptID(t *testing.T) {
	h := newWebhookHarnessNT()
	req := h.signedRequest(t, WebhookEventRunWaiting,
		map[string]any{"node_id": "n7", "interrupt_id": "n7", "node_name": "风控复核"})
	rec := httptest.NewRecorder()
	h.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || len(h.consumed) != 1 {
		t.Fatalf("waiting_approval must be consumed, got %d %+v", rec.Code, h.consumed)
	}
	if got := h.consumed[0].InterruptID(); got != "n7" {
		t.Fatalf("InterruptID must project payload.interrupt_id, got %q", got)
	}
	var other WebhookEvent
	if other.InterruptID() != "" {
		t.Fatal("non-waiting event must yield empty InterruptID")
	}
}

// T-WH-NT-04 ResumeRun 契约钉死：路径/方法/请求体键名 + 状态码哨兵 + 必填校验。
func TestResumeRun_Contract(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody []byte
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		gotMethod, gotPath = r.Method, r.URL.Path
		gotBody, _ = io.ReadAll(r.Body)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer srv.Close()

	cl := NewClient(Conf{Driver: "http", URL: srv.URL, Timeout: 5 * time.Second},
		func(string) (string, error) { return "tok", nil })
	ctx := context.Background()

	err := cl.ResumeRun(ctx, "run-1", "n7", ApprovalDecision{Approved: true, Comment: "人工复核通过", ApproverID: 42})
	if err != nil {
		t.Fatalf("resume must pass through, got %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/v1/runs/run-1/interrupts/n7/resume" {
		t.Fatalf("resume endpoint wrong: %s %s", gotMethod, gotPath)
	}
	var payload map[string]any
	if err := json.Unmarshal(gotBody, &payload); err != nil {
		t.Fatalf("resume body unmarshal: %v", err)
	}
	if payload["approved"] != true || payload["comment"] != "人工复核通过" || payload["approver_id"] != float64(42) {
		t.Fatalf("resume payload keys wrong: %v", payload)
	}

	// 必填校验：缺 run_id / interrupt_id 不发请求。
	if err := cl.ResumeRun(ctx, "", "n7", ApprovalDecision{Approved: true}); !errors.Is(err, ErrHarnessBadRequest) {
		t.Fatalf("missing run_id must yield bad request sentinel, got %v", err)
	}
	if err := cl.ResumeRun(ctx, "run-1", "", ApprovalDecision{Approved: true}); !errors.Is(err, ErrHarnessBadRequest) {
		t.Fatalf("missing interrupt_id must yield bad request sentinel, got %v", err)
	}
	if hits != 1 {
		t.Fatalf("client-side validation must not hit server, hits=%d", hits)
	}
}

// T-WH-NT-04b ResumeRun 状态码分流：404→NotFound（run 不可见/interrupt 不符走 400）。
func TestResumeRun_ErrorMapping(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   error
	}{
		{http.StatusNotFound, `{"error":"run not found"}`, ErrHarnessNotFound},
		{http.StatusBadRequest, `{"error":"interrupt_id mismatch"}`, ErrHarnessBadRequest},
		{http.StatusUnauthorized, `{"error":"invalid api token"}`, ErrHarnessUnavailable},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		}))
		cl := NewClient(Conf{Driver: "http", URL: srv.URL, Timeout: 5 * time.Second},
			func(string) (string, error) { return "tok", nil })
		err := cl.ResumeRun(context.Background(), "run-1", "n7", ApprovalDecision{Approved: false, Comment: "拒绝", ApproverID: 1})
		if !errors.Is(err, tc.want) {
			t.Fatalf("status %d: want %v, got %v", tc.status, tc.want, err)
		}
		srv.Close()
	}
}
