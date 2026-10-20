package data

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"

	"server/app/datahub/internal/biz"

	"golang.org/x/text/encoding/simplifiedchinese"
)

const maxBody = 32 << 20

// RawSink 保存原始响应。为 nil 时不留存。
type RawSink func(ctx context.Context, r biz.RawRecord)

// 循环采集的域每几秒一轮，留存原始响应会撑爆库，始终不留。
var rawSkip = map[string]bool{biz.DomainSnapshot: true, biz.DomainSectorQuote: true}

// httpc 是各源共用的 HTTP 客户端：并发上限、固定请求头、原始响应留存。
// 错误信息只带 URL 路径，不带查询串和请求体，避免泄露令牌。
type httpc struct {
	source  string
	hc      *http.Client
	headers map[string]string
	sem     chan struct{}
	limit   *biz.Limiter
	raw     RawSink
	// P3-9（东财 _em_get 同款）：随机抖动把固定节奏打散、退避重试 throttled。
	// JitterGap 用并发上限前的最后间隔估计；Retries 仅对可退避错误生效。
	jitter  time.Duration
	retries int
	// backoff 是退避基准时长（默认 1s，实际序列 1s/2s/4s，throttled 再乘 4）。
	// 测试里可调小避免真睡。
	backoff time.Duration
}

// HTTPOptions 是一个源的请求级限制。QPS 按单个 HTTP 请求计，分页接口每页算一次。
// P3-9 两个可选件只在真正需要节流的源上打开（当前只东财）：
// JitterGap>0 时在 Limiter 之后加 [0, JitterGap) 随机延迟打散节奏；Retries>0 时对
// 「限流/封禁」（HTTP 403/429/连接重置/超时）重试最多 Retries 次，间隔指数退避。
type HTTPOptions struct {
	Concurrency int
	QPS         float64
	Raw         RawSink
	JitterGap   time.Duration
	Retries     int
}

func newHTTPC(source string, opt HTTPOptions, headers map[string]string) *httpc {
	if opt.Concurrency <= 0 {
		opt.Concurrency = 4
	}
	if opt.Retries < 0 {
		opt.Retries = 0
	}
	return &httpc{
		source:  source,
		hc:      &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, MaxIdleConnsPerHost: opt.Concurrency * 2, IdleConnTimeout: 90 * time.Second}},
		headers: headers,
		sem:     make(chan struct{}, opt.Concurrency),
		limit:   biz.NewLimiter(opt.QPS),
		raw:     opt.Raw,
		jitter:  opt.JitterGap,
		retries: opt.Retries,
		backoff: time.Second,
	}
}

func (c *httpc) get(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	return c.do(ctx, req)
}

func (c *httpc) postJSON(ctx context.Context, rawURL string, body any) ([]byte, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(ctx, req)
}

func (c *httpc) postForm(ctx context.Context, rawURL string, form url.Values) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	return c.do(ctx, req)
}

// do 执行一次请求：并发上限 + 节流 + 抖动（如有）+ 可退避错误的退避重试（如有）。
// sem 在整次调用（含重试）期间持有；limit/jitter 按每次实际发出计，重试也消耗令牌。
func (c *httpc) do(ctx context.Context, req *http.Request) ([]byte, error) {
	select {
	case c.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-c.sem }()

	maxAttempts := 1 + c.retries
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			// 退避等待：1s/2s/4s…；疑似限流/封禁（403/429）乘 4 让对端冷却。
			if err := sleepCtx(ctx, backoffFor(lastErr, c.backoff, attempt)); err != nil {
				return nil, err
			}
		}
		body, err := c.attempt(ctx, req)
		if err == nil {
			return body, nil
		}
		lastErr = err
		if !retryable(err) || attempt == maxAttempts {
			return nil, err
		}
	}
	return nil, lastErr
}

// attempt 发出单次 HTTP 请求（节流 + 抖动 + 头部 + 原始留存 + 非 2xx 归 httpError）。
func (c *httpc) attempt(ctx context.Context, req *http.Request) ([]byte, error) {
	if err := c.limit.Wait(ctx); err != nil {
		return nil, err
	}
	if c.jitter > 0 {
		if err := sleepCtx(ctx, time.Duration(rand.Int63n(int64(c.jitter)))); err != nil {
			return nil, err
		}
	}
	for k, v := range c.headers {
		if req.Header.Get(k) == "" {
			req.Header.Set(k, v)
		}
	}
	where := truncate(req.URL.Host+req.URL.Path, 80)
	resp, err := c.hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%s %s: %w", c.source, where, ctx.Err())
		}
		return nil, fmt.Errorf("%s %s: %s", c.source, where, stripURL(err))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("%s %s: read body: %w", c.source, where, err)
	}
	if c.raw != nil {
		if d := biz.DomainOf(ctx); !rawSkip[d] {
			c.raw(ctx, biz.RawRecord{Source: c.source, Domain: d, Request: where, Status: resp.StatusCode, Body: body, At: time.Now()})
		}
	}
	if resp.StatusCode/100 != 2 {
		return nil, &httpError{Source: c.source, Where: where, Status: resp.StatusCode, Body: body}
	}
	return body, nil
}

// retryable 判定一次失败是否值得退避重试：HTTP 403/429（疑似限流/封禁）、
// 5xx（服务端瞬断）与网络层错误（连接重置/EOF 等）重试；ctx 取消/超时与 4xx 不重试。
func retryable(err error) bool {
	var he *httpError
	if errors.As(err, &he) {
		return he.Throttled() || he.Status >= 500
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return true
}

// backoffFor 第 attempt 次（>=2）发出前的等待：base*2^(attempt-2)；throttled 乘 4。
func backoffFor(lastErr error, base time.Duration, attempt int) time.Duration {
	d := base << (attempt - 2)
	var he *httpError
	if errors.As(lastErr, &he) && he.Throttled() {
		d *= 4
	}
	return d
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// httpError 是非 2xx 响应。
type httpError struct {
	Source string
	Where  string
	Status int
	Body   []byte
}

// Throttled 表示疑似被对端限流/封禁（403/429）。数据源健康面板的
// LastError 直接展示该标签（healthBook.record 截 err.Error() 入库）。
func (e *httpError) Throttled() bool { return e.Status == 403 || e.Status == 429 }

func (e *httpError) Error() string {
	if e.Throttled() {
		// 标签放行首：数据源面板「说明」列 nowrap 且横向滚动，行首一眼可辨。
		return fmt.Sprintf("【疑似限流/封禁】%s %s: HTTP %d: %s", e.Source, e.Where, e.Status, snippet(e.Body))
	}
	return fmt.Sprintf("%s %s: HTTP %d: %s", e.Source, e.Where, e.Status, snippet(e.Body))
}

func (c *httpc) getJSON(ctx context.Context, rawURL string, out any) error {
	b, err := c.get(ctx, rawURL)
	if err != nil {
		return err
	}
	return decodeJSON(c.source, b, out)
}

func decodeJSON(source string, b []byte, out any) error {
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("%s: decode: %v: %s", source, err, snippet(b))
	}
	return nil
}

// stripURL 去掉 *url.Error 里带查询串的完整地址。
func stripURL(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err.Error()
	}
	return err.Error()
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	r := []rune(s)
	if len(r) > 200 {
		return string(r[:200]) + "…"
	}
	return s
}

func gbk(b []byte) string {
	out, err := simplifiedchinese.GBK.NewDecoder().Bytes(b)
	if err != nil {
		return string(b)
	}
	return string(out)
}
