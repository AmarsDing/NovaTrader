package data

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
}

// HTTPOptions 是一个源的请求级限制。QPS 按单个 HTTP 请求计，分页接口每页算一次。
type HTTPOptions struct {
	Concurrency int
	QPS         float64
	Raw         RawSink
}

func newHTTPC(source string, opt HTTPOptions, headers map[string]string) *httpc {
	if opt.Concurrency <= 0 {
		opt.Concurrency = 4
	}
	return &httpc{
		source:  source,
		hc:      &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, MaxIdleConnsPerHost: opt.Concurrency * 2, IdleConnTimeout: 90 * time.Second}},
		headers: headers,
		sem:     make(chan struct{}, opt.Concurrency),
		limit:   biz.NewLimiter(opt.QPS),
		raw:     opt.Raw,
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

func (c *httpc) do(ctx context.Context, req *http.Request) ([]byte, error) {
	select {
	case c.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-c.sem }()
	if err := c.limit.Wait(ctx); err != nil {
		return nil, err
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

// httpError 是非 2xx 响应。
type httpError struct {
	Source string
	Where  string
	Status int
	Body   []byte
}

func (e *httpError) Error() string {
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
