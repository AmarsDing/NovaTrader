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

// routeTransport 按 URL 子串路由到预置响应体；用例间共享 QPS 高位 client（不限速）。
type routeTransport struct {
	routes map[string]string // url 子串 → 响应体
	calls  int
}

func (t *routeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.calls++
	u := req.URL.String()
	for k, body := range t.routes {
		if strings.Contains(u, k) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
		}
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
}

func newRouteEastmoney(routes map[string]string) *Eastmoney {
	tt := &routeTransport{routes: routes}
	// HTTPOptions 不带 P3-9 的 Retries（其批未落 HEAD，零值即不重试）。
	hc := newHTTPC("", HTTPOptions{QPS: 10000, Concurrency: 1}, nil)
	hc.hc = &http.Client{Transport: tt}
	return &Eastmoney{hc: hc}
}

func TestEastmoneyBlockTrades(t *testing.T) {
	em := newRouteEastmoney(map[string]string{
		"reportName=RPT_DATA_BLOCKTRADE": `{"success":true,"result":{"pages":1,"data":[
			{"TRADE_DATE":"2026-10-17","SECUCODE":"600519.SH","TRADE_PRICE":1680.5,"VOLUME":12.3,"TURNOVER":20670.15,"BUYER_NAME":"机构专用","SELLER_NAME":"某某营业部","PREMIUM_RATIO":-2.35,"CLOSE_PRICE":1721.0},
			{"TRADE_DATE":"2026-10-17","SECUCODE":"","TRADE_PRICE":1.0},
			{"TRADE_DATE":"bad-date","SECUCODE":"000001.SZ","TRADE_PRICE":2.0},
			{"TRADE_DATE":"2026-10-17","SECUCODE":"000001.SZ","TRADE_PRICE":10.2,"VOLUME":5,"TURNOVER":51}
		]}}`,
	})
	items, err := em.BlockTrades(context.Background(), time.Date(2026, 10, 17, 0, 0, 0, 0, time.Local))
	if err != nil {
		t.Fatalf("BlockTrades: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("rows=%d want 2（空代码行/坏日期行 skip）", len(items))
	}
	a := items[0]
	if a.Symbol != "600519.SH" || a.TradePrice != 1680.5 || a.Volume != 12.3 {
		t.Fatalf("row0 基础字段不对: %+v", a)
	}
	if a.Amount != 20670.15*1e4 {
		t.Fatalf("TURNOVER 万元应 ×1e4 归一为元: %v", a.Amount)
	}
	if a.PremiumRatio == nil || *a.PremiumRatio != -2.35 || a.ClosePrice == nil {
		t.Fatalf("溢价率/收盘价不应缺失: %+v", a)
	}
	b := items[1]
	if b.PremiumRatio != nil || b.ClosePrice != nil {
		t.Fatalf("缺列应保持 nil——没取到≠0: %+v", b)
	}
}

func TestEastmoneyGuba(t *testing.T) {
	old := gubaInterval
	gubaInterval = 0
	defer func() { gubaInterval = old }()
	em := newRouteEastmoney(map[string]string{
		"gbapi.eastmoney.com": `{"rc":0,"re":[
			{"post_id":"1001","post_title":"主力开始干活了","post_click_count":1234,"post_comment_count":56,"post_publish_time":"2026-10-19 14:03:22","stockbar_code":"600519"},
			{"post_id":"1002","post_title":"跨吧串帖","post_click_count":1,"post_comment_count":0,"post_publish_time":"2026-10-19 14:01:00","stockbar_code":"000001"},
			{"post_id":"1003","post_title":"无时间帖","post_click_count":2,"post_comment_count":1,"post_publish_time":""},
			{"post_id":"","post_title":"无ID帖","post_publish_time":"2026-10-19 13:00:00"}
		]}`,
	})
	// 未注入观察清单：ErrUnsupported 计跳过不计失败。
	if _, err := em.guba(context.Background(), biz.IntelQuery{}); !errors.Is(err, biz.ErrUnsupported) {
		t.Fatalf("nil watch 应 ErrUnsupported: %v", err)
	}
	em.SetGubaWatch(func(context.Context) ([]string, error) { return []string{"600519.SH"}, nil })
	items, err := em.guba(context.Background(), biz.IntelQuery{})
	if err != nil {
		t.Fatalf("guba: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items=%d want 1（跨吧/无时间/无ID 全部 skip）: %+v", len(items), items)
	}
	it := items[0]
	if it.Kind != biz.KindSocial || it.SourceID != "1001" || len(it.Codes) != 1 || it.Codes[0] != "600519.SH" {
		t.Fatalf("item 形态不对: %+v", it)
	}
	if !strings.Contains(it.URL, "guba.eastmoney.com/news,600519,1001.html") {
		t.Fatalf("URL 不对: %s", it.URL)
	}
	if it.PublishTime == "" || !strings.Contains(it.Content, "点击1234｜评论56") {
		t.Fatalf("发布时间/热度 content 不对: %+v", it)
	}
}

func TestEastmoneyProbeReports(t *testing.T) {
	em := newRouteEastmoney(map[string]string{
		"reportName=RPTA_WEB_RZRQ_GGMX":             `{"success":false,"code":9501,"message":"报表配置不存在","result":null}`,
		"reportName=RPT_LIFT_STAGE":                 `{"success":true,"result":{"pages":0,"data":[]}}`,
		"reportName=RPT_MUTUAL_TOP10DEAL":           `{"success":true,"result":{"pages":1,"data":[{"x":1}]}}`,
		"reportName=RPT_PUBLIC_OP_NEWPREDICT":       `{"success":true,"result":{"pages":0,"data":[]}}`,
		"reportName=RPT_FCI_PERFORMANCEE":           `{"success":true,"result":{"pages":0,"data":[]}}`,
		"reportName=RPT_BILLBOARD_DAILYDETAILSBUY":  `{"success":true,"result":{"pages":1,"data":[{"x":1}]}}`,
		"reportName=RPT_BILLBOARD_DAILYDETAILSSELL": `{"success":true,"result":{"pages":0,"data":[]}}`,
	})
	day := time.Date(2026, 10, 17, 0, 0, 0, 0, time.Local)
	out := em.ProbeReports(context.Background(), day)
	if len(out) != 7 {
		t.Fatalf("results=%d want 7", len(out))
	}
	type stub struct {
		rows int
		err  error
	}
	byReport := map[string]stub{}
	for _, r := range out {
		byReport[r.Report] = stub{r.Rows, r.Err}
	}
	var rg *ReportGoneError
	if gone := byReport["RPTA_WEB_RZRQ_GGMX"].err; !errors.As(gone, &rg) || rg.Code != 9501 {
		t.Fatalf("GGMX 应为 ReportGoneError 9501: %v", gone)
	}
	if e := byReport["RPT_BILLBOARD_DAILYDETAILSSELL"].err; e == nil || !strings.Contains(e.Error(), "预期非空") {
		t.Fatalf("非空报表 0 行应报错: %v", e)
	}
	for _, name := range []string{"RPT_LIFT_STAGE", "RPT_PUBLIC_OP_NEWPREDICT", "RPT_FCI_PERFORMANCEE"} {
		if r := byReport[name]; r.err != nil || r.rows != 0 {
			t.Fatalf("%s allowEmpty 0 行应通过: %+v", name, r)
		}
	}
	if r := byReport["RPT_MUTUAL_TOP10DEAL"]; r.err != nil || r.rows != 1 {
		t.Fatalf("正常报表应 1 行无错: %+v", r)
	}
}
