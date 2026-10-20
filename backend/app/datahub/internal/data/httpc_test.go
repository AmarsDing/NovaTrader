package data

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"server/app/datahub/internal/biz"
)

// stubTransport 按队列返回状态码；netErr 非空时恒返网络错误。
type stubTransport struct {
	calls    int
	statuses []int
	netErr   error
}

func (t *stubTransport) RoundTrip(*http.Request) (*http.Response, error) {
	t.calls++
	if t.netErr != nil {
		return nil, t.netErr
	}
	st := http.StatusOK
	if len(t.statuses) > 0 {
		st = t.statuses[0]
		t.statuses = t.statuses[1:]
	}
	return &http.Response{
		StatusCode: st,
		Body:       io.NopCloser(strings.NewReader(`{}`)),
		Header:     make(http.Header),
	}, nil
}

// newStubHTTPC 组一个走 stub 传输的客户端；backoff 调小免得测试真睡。
func newStubHTTPC(statuses []int, netErr error, retries int) (*httpc, *stubTransport) {
	tt := &stubTransport{statuses: statuses, netErr: netErr}
	c := newHTTPC(biz.SourceEastmoney, HTTPOptions{QPS: 10000, Retries: retries}, nil)
	c.hc = &http.Client{Transport: tt}
	c.backoff = time.Millisecond
	return c, tt
}

const stubURL = "https://push2.eastmoney.com/api/qt/clist/get?pn=1"

// 5xx 瞬断退避重试后成功（P3-9 失败退避）。
func TestHTTPCRetryOnServerError(t *testing.T) {
	c, tt := newStubHTTPC([]int{500, 200}, nil, 2)
	b, err := c.get(context.Background(), stubURL)
	if err != nil {
		t.Fatalf("should succeed, got %v", err)
	}
	if string(b) != `{}` {
		t.Fatalf("bad body %q", b)
	}
	if tt.calls != 2 {
		t.Fatalf("calls=%d want 2", tt.calls)
	}
}

// 连续 429 重试到上限仍失败，错误带「疑似限流/封禁」供 source_health 面板辨识。
func TestHTTPCRetryThrottledExhausts(t *testing.T) {
	c, tt := newStubHTTPC([]int{429, 429, 429}, nil, 2)
	_, err := c.get(context.Background(), stubURL)
	if err == nil {
		t.Fatal("want error")
	}
	var he *httpError
	if !errors.As(err, &he) || !he.Throttled() {
		t.Fatalf("want throttled httpError, got %T %v", err, err)
	}
	if !strings.HasPrefix(err.Error(), "【疑似限流/封禁】") {
		t.Fatalf("message must lead with throttle tag (panel 一眼可辨): %v", err)
	}
	if tt.calls != 3 {
		t.Fatalf("calls=%d want 3 (1+2 retries)", tt.calls)
	}
}

// 4xx 非限流类不重试：400 只发一次。
func TestHTTPCNoRetryOnClientError(t *testing.T) {
	c, tt := newStubHTTPC([]int{400}, nil, 2)
	_, err := c.get(context.Background(), stubURL)
	var he *httpError
	if !errors.As(err, &he) || he.Status != 400 {
		t.Fatalf("want HTTP 400, got %v", err)
	}
	if he.Throttled() {
		t.Fatal("400 must not be throttled")
	}
	if strings.Contains(err.Error(), "疑似限流/封禁") {
		t.Fatalf("400 message must not carry throttle tag: %v", err)
	}
	if tt.calls != 1 {
		t.Fatalf("calls=%d want 1", tt.calls)
	}
}

// 网络错误（连接重置，封 IP 实测形态）重试到上限仍失败。
func TestHTTPCRetryNetworkError(t *testing.T) {
	c, tt := newStubHTTPC(nil, errors.New("read tcp: connection reset by peer"), 1)
	_, err := c.get(context.Background(), stubURL)
	if err == nil {
		t.Fatal("want error")
	}
	if tt.calls != 2 {
		t.Fatalf("calls=%d want 2", tt.calls)
	}
}

// 抖动冒烟：JitterGap>0 时流程正常完成（时长不做时钟断言，CI 不稳）。
func TestHTTPCJitterSmoke(t *testing.T) {
	c, tt := newStubHTTPC(nil, nil, 0)
	c.jitter = 2 * time.Millisecond
	if _, err := c.get(context.Background(), stubURL); err != nil {
		t.Fatalf("jitter path broken: %v", err)
	}
	if tt.calls != 1 {
		t.Fatalf("calls=%d want 1", tt.calls)
	}
}

// 重试每次发出都消耗 Limiter 令牌（重试也计 QPS）。
func TestHTTPCRetryConsumesLimiter(t *testing.T) {
	c, tt := newStubHTTPC([]int{503, 503, 200}, nil, 2)
	c.limit = biz.NewLimiter(0) // 不限速
	if _, err := c.get(context.Background(), stubURL); err != nil {
		t.Fatalf("should succeed, got %v", err)
	}
	if tt.calls != 3 {
		t.Fatalf("calls=%d want 3", tt.calls)
	}
}

// 退避时长：非 throttled 指数 1x/2x/4x；throttled 再乘 4。
func TestBackoffFor(t *testing.T) {
	base := time.Second
	plain := errors.New("net down")
	if got := backoffFor(plain, base, 2); got != base {
		t.Fatalf("attempt2 plain=%v want 1s", got)
	}
	if got := backoffFor(plain, base, 3); got != 2*base {
		t.Fatalf("attempt3 plain=%v want 2s", got)
	}
	th := &httpError{Status: 429}
	if got := backoffFor(th, base, 2); got != 4*base {
		t.Fatalf("attempt2 throttled=%v want 4s", got)
	}
	if got := backoffFor(th, base, 3); got != 8*base {
		t.Fatalf("attempt3 throttled=%v want 8s", got)
	}
}
