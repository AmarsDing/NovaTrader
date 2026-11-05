package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// httpClient 底座机器面 Bearer 适配器（driver=http）。
// 协议形状只允许出现在本文件（防火墙式协议边界，HCF 02 §3 同构）。
type httpClient struct {
	base  string
	token string
	hc    *http.Client
}

func newHTTPClient(base, token string, timeout time.Duration) Client {
	return &httpClient{
		base:  base,
		token: token,
		hc:    &http.Client{Timeout: timeout},
	}
}

// do 发起一次机器面调用：Bearer 头 + JSON 编解码 + 状态码→哨兵分流。
// 2xx 时若 out 非空则解码；非 2xx 按 400/404/其他 映射哨兵，底座
// {"error": "..."} 文案如实带进 Detail。
func (c *httpClient) do(ctx context.Context, method, path, query string, in, out any) error {
	u := c.base + path
	if query != "" {
		u += "?" + query
	}
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return &Error{Kind: ErrHarnessBadRequest, Detail: "请求编码失败: " + err.Error()}
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return &Error{Kind: ErrHarnessBadRequest, Detail: err.Error()}
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return &Error{Kind: ErrHarnessUnavailable, Detail: err.Error()}
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return &Error{Kind: ErrHarnessUnavailable, Detail: "响应读取失败: " + err.Error()}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return mapHTTPError(resp.StatusCode, raw)
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return &Error{Kind: ErrHarnessUnavailable, Detail: "响应解码失败（契约漂移？）: " + err.Error()}
		}
	}
	return nil
}

// mapHTTPError 状态码→哨兵；底座错误体 {"error":"..."} 文案如实透传。
func mapHTTPError(status int, raw []byte) error {
	detail := fmt.Sprintf("http %d", status)
	var body struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(raw, &body) == nil && body.Error != "" {
		detail = body.Error
	}
	switch {
	case status == http.StatusBadRequest:
		return &Error{Kind: ErrHarnessBadRequest, Detail: detail}
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return &Error{Kind: ErrHarnessUnavailable, Detail: "鉴权失败（token 失效或未注册 caller）: " + detail}
	case status == http.StatusNotFound:
		return &Error{Kind: ErrHarnessNotFound, Detail: detail}
	default:
		return &Error{Kind: ErrHarnessUnavailable, Detail: detail}
	}
}

func (c *httpClient) Health(ctx context.Context) (*HealthStatus, error) {
	var out HealthStatus
	if err := c.do(ctx, http.MethodGet, "/api/v1/health", "", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *httpClient) ListAgents(ctx context.Context) ([]AgentSnapshot, error) {
	var out struct {
		Items []AgentSnapshot `json:"items"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v1/agents", "", nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// submitPayload POST /api/v1/runs 请求体（键名与底座 twinRunInput 对齐）。
type submitPayload struct {
	GraphID        string         `json:"graph_id"`
	Input          string         `json:"input,omitempty"`
	Params         map[string]any `json:"params,omitempty"`
	WebhookURL     string         `json:"webhook_url,omitempty"`
	WebhookSecret  string         `json:"webhook_secret,omitempty"`
	IdempotencyKey string         `json:"idempotency_key,omitempty"`
}

func (c *httpClient) SubmitRun(ctx context.Context, req SubmitReq) (*SubmitResp, error) {
	// 必填纪律在客户端侧先行校验（属调用方 bug，不发请求）。
	if strings.TrimSpace(req.GraphID) == "" {
		return nil, &Error{Kind: ErrHarnessBadRequest, Detail: "graph_id 必填（底座不支持 agent 直跑）"}
	}
	if strings.TrimSpace(req.IdempotencyKey) == "" {
		return nil, &Error{Kind: ErrHarnessBadRequest, Detail: "idempotency_key 必填（底座按 caller+key 去重，缺它重试会重复执行）"}
	}
	var out SubmitResp
	if err := c.do(ctx, http.MethodPost, "/api/v1/runs", "", submitPayload{
		GraphID:        req.GraphID,
		Input:          req.Input,
		Params:         req.Params,
		WebhookURL:     req.WebhookURL,
		WebhookSecret:  req.WebhookSecret,
		IdempotencyKey: req.IdempotencyKey,
	}, &out); err != nil {
		return nil, err
	}
	if out.RunID == "" {
		return nil, &Error{Kind: ErrHarnessUnavailable, Detail: "底座 200 但缺 run_id（契约漂移）"}
	}
	return &out, nil
}

func (c *httpClient) GetRun(ctx context.Context, runID string) (*RunDetail, error) {
	if strings.TrimSpace(runID) == "" {
		return nil, &Error{Kind: ErrHarnessBadRequest, Detail: "run_id 必填"}
	}
	var out RunDetail
	if err := c.do(ctx, http.MethodGet, "/api/v1/runs/"+url.PathEscape(runID), "", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *httpClient) ListRuns(ctx context.Context, graphID, status, cursor string, pageSize int) (*RunListPage, error) {
	if strings.TrimSpace(graphID) == "" {
		return nil, &Error{Kind: ErrHarnessBadRequest, Detail: "graph_id 必填（底座按图归属做租户门禁）"}
	}
	q := url.Values{"graph_id": []string{graphID}}
	if status != "" {
		q.Set("status", status)
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	if pageSize > 0 {
		q.Set("page_size", strconv.Itoa(pageSize))
	}
	var out struct {
		Items      []RunSummary `json:"items"`
		NextCursor string       `json:"next_cursor"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v1/runs", q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	return &RunListPage{Items: out.Items, NextCursor: out.NextCursor}, nil
}

func (c *httpClient) CancelRun(ctx context.Context, runID string) error {
	if strings.TrimSpace(runID) == "" {
		return &Error{Kind: ErrHarnessBadRequest, Detail: "run_id 必填"}
	}
	return c.do(ctx, http.MethodPost, "/api/v1/runs/"+url.PathEscape(runID)+"/cancel", "", nil, nil)
}

func (c *httpClient) ResumeRun(ctx context.Context, runID, interruptID string, decision ApprovalDecision) error {
	if strings.TrimSpace(runID) == "" {
		return &Error{Kind: ErrHarnessBadRequest, Detail: "run_id 必填"}
	}
	if strings.TrimSpace(interruptID) == "" {
		return &Error{Kind: ErrHarnessBadRequest, Detail: "interrupt_id 必填（run.waiting_approval 事件 payload 提供）"}
	}
	return c.do(ctx, http.MethodPost,
		"/api/v1/runs/"+url.PathEscape(runID)+"/interrupts/"+url.PathEscape(interruptID)+"/resume",
		"", decision, nil)
}
