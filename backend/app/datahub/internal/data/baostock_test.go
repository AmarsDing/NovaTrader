package data

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"server/app/datahub/internal/biz"
)

// bsQuery 固定查询窗，覆盖 ③ 的 start/end/expect 格式断言。
func bsQuery() biz.BarQuery {
	loc := shanghaiLoc()
	return biz.BarQuery{
		Freq:    "1d",
		Start:   time.Date(2026, 10, 1, 0, 0, 0, 0, loc),
		End:     time.Date(2026, 10, 16, 0, 0, 0, 0, loc),
		Expect:  time.Date(2026, 10, 16, 0, 0, 0, 0, loc),
		Symbols: []string{"600519.SH"},
	}
}

// ① /bs/bars 200 行映射：全字段含 *PreClose==10.1、volume 原样股数（不乘100）、stale/last_date 透传。
func TestBaostockBarsMapping(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bs/bars" {
			t.Errorf("wrong path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"bars":[{"symbol":"600519.SH","time":"2026-10-16T00:00:00+08:00","freq":"1d","open":1700.0,"high":1710.0,"low":1690.0,"close":1705.0,"pre_close":10.1,"volume":3600000,"amount":6120000000.0}],"last_date":"2026-10-16","stale":false}`))
	}))
	defer srv.Close()
	s := NewBaostock(srv.URL, nil, HTTPOptions{QPS: 10000})
	batch, err := s.DailyBars(context.Background(), bsQuery())
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Bars) != 1 {
		t.Fatalf("bars=%d want 1", len(batch.Bars))
	}
	b := batch.Bars[0]
	if b.Symbol != "600519.SH" || b.Close != 1705.0 || b.Open != 1700.0 {
		t.Fatalf("bad bar mapping: %+v", b)
	}
	if b.PreClose == nil || *b.PreClose != 10.1 {
		t.Fatalf("PreClose=%v want 10.1", b.PreClose)
	}
	if b.Volume != 3600000 {
		t.Fatalf("Volume=%d want 3600000（股，原样不乘100）", b.Volume)
	}
	if b.Freq != "1d" {
		t.Fatalf("Freq=%q want 1d", b.Freq)
	}
	if batch.LastDate != "2026-10-16" || batch.Stale {
		t.Fatalf("LastDate=%q Stale=%v want 2026-10-16/false", batch.LastDate, batch.Stale)
	}
	if batch.Expect != "2026-10-16" {
		t.Fatalf("Expect=%q want 2026-10-16", batch.Expect)
	}
}

// ② 请求体钉死：adjustflag=="3"、freq 钉 1d、start/end/expect 为 YYYY-MM-DD。
func TestBaostockBarsRequestContract(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"bars":[],"last_date":"","stale":false}`))
	}))
	defer srv.Close()
	q := bsQuery()
	q.Freq = "5m" // 应被钉回 1d
	s := NewBaostock(srv.URL, nil, HTTPOptions{QPS: 10000})
	if _, err := s.DailyBars(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{"adjustflag": "3", "freq": "1d", "start": "2026-10-01", "end": "2026-10-16", "expect": "2026-10-16"} {
		if v, _ := got[k].(string); v != want {
			t.Fatalf("request %s=%q want %q (body=%v)", k, v, want, got)
		}
	}
	syms, _ := got["symbols"].([]any)
	if len(syms) != 1 || syms[0] != "600519.SH" {
		t.Fatalf("symbols=%v want [600519.SH]", got["symbols"])
	}
}

// symbols 为空走 universe 闭包；闭包失败直接报错不空跑。
func TestBaostockUniverseFallback(t *testing.T) {
	var gotSyms []any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		gotSyms, _ = req["symbols"].([]any)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"bars":[],"last_date":"","stale":false}`))
	}))
	defer srv.Close()
	q := bsQuery()
	q.Symbols = nil
	s := NewBaostock(srv.URL, func(context.Context) ([]string, error) { return []string{"000001.SZ"}, nil }, HTTPOptions{QPS: 10000})
	if _, err := s.DailyBars(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	if len(gotSyms) != 1 || gotSyms[0] != "000001.SZ" {
		t.Fatalf("symbols=%v want [000001.SZ]（universe 补全）", gotSyms)
	}

	s2 := NewBaostock(srv.URL, func(context.Context) ([]string, error) { return nil, errors.New("repo down") }, HTTPOptions{QPS: 10000})
	if _, err := s2.DailyBars(context.Background(), q); err == nil || !strings.Contains(err.Error(), "repo down") {
		t.Fatalf("universe 失败应直接报错不空跑，得 %v", err)
	}
}

// ③ /bs/finance 映射：Kind==FinanceIndicator、AnnDate/EndDate 解析、NetProfit 指针透传、
// Data 保留原始四表行、坏 pub_date 行 skip（AnnDate 缺失不可入库）。
func TestBaostockFinanceMapping(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bs/finance" {
			t.Errorf("wrong path %s", r.URL.Path)
		}
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if since, _ := req["since"].(string); since != "2026-09-01" {
			t.Errorf("since=%q want 2026-09-01", since)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[
		  {"symbol":"600519.SH","end_date":"2026-09-30","pub_date":"2026-10-15","kind":"indicator","net_profit":64000000000.0,"net_profit_yoy":15.5,"roe":26.1,"eps":50.9,"data":{"profit":{"statDate":"2026-09-30","netProfit":"64000000000"},"balance":{},"cashflow":{},"growth":{}}},
		  {"symbol":"600519.SH","end_date":"2026-06-30","pub_date":"n/a","kind":"indicator","net_profit":null,"net_profit_yoy":null,"data":{}}
		]}`))
	}))
	defer srv.Close()
	s := NewBaostock(srv.URL, func(context.Context) ([]string, error) { return []string{"600519.SH"}, nil }, HTTPOptions{QPS: 10000})
	items, err := s.Finance(context.Background(), time.Date(2026, 9, 1, 0, 0, 0, 0, shanghaiLoc()))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("items=%d want 1（坏 pub_date 行应 skip）: %+v", len(items), items)
	}
	it := items[0]
	if it.Kind != biz.FinanceIndicator {
		t.Fatalf("Kind=%q want indicator", it.Kind)
	}
	if it.AnnDate.Format("2006-01-02") != "2026-10-15" {
		t.Fatalf("AnnDate=%v want 2026-10-15", it.AnnDate)
	}
	if it.EndDate.Format("2006-01-02") != "2026-09-30" {
		t.Fatalf("EndDate=%v want 2026-09-30", it.EndDate)
	}
	if it.NetProfit == nil || *it.NetProfit != 64000000000.0 {
		t.Fatalf("NetProfit=%v want 6.4e10", it.NetProfit)
	}
	if it.NetProfitYoY == nil || *it.NetProfitYoY != 15.5 {
		t.Fatalf("NetProfitYoY=%v want 15.5", it.NetProfitYoY)
	}
	profit, ok := it.Data["profit"].(map[string]any)
	if !ok || profit["netProfit"] != "64000000000" {
		t.Fatalf("Data 未原样留存 profit 行: %v", it.Data)
	}
}

// ④ 503 {"error":"unavailable","reason":"未安装 baostock"} → UnavailableError 且 Reason 带原文。
func TestBaostockUnavailableMapping(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"unavailable","reason":"未安装 baostock"}`))
	}))
	defer srv.Close()
	s := NewBaostock(srv.URL, nil, HTTPOptions{QPS: 10000})
	_, err := s.DailyBars(context.Background(), bsQuery())
	var ue *biz.UnavailableError
	if !errors.As(err, &ue) {
		t.Fatalf("want *biz.UnavailableError, got %T %v", err, err)
	}
	if !strings.Contains(ue.Reason, "未安装 baostock") {
		t.Fatalf("Reason=%q 应带原文「未安装 baostock」", ue.Reason)
	}
}

// ⑤ 404 → UnavailableError「pyworker 未启用该角色」。
func TestBaostockRoleDisabled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"role disabled","role":"baostock"}`))
	}))
	defer srv.Close()
	s := NewBaostock(srv.URL, nil, HTTPOptions{QPS: 10000})
	_, err := s.DailyBars(context.Background(), bsQuery()) // Symbols 显式给出，不碰 universe，直达 404 映射
	var ue *biz.UnavailableError
	if !errors.As(err, &ue) {
		t.Fatalf("want *biz.UnavailableError, got %T %v", err, err)
	}
	if !strings.Contains(ue.Reason, "pyworker 未启用该角色") {
		t.Fatalf("Reason=%q want pyworker 未启用该角色", ue.Reason)
	}
}

// ⑥200 {"bars":[],"stale":true} → BarBatch.Stale==true 且零行（显式缺失非 0 填充）。
func TestBaostockEmptyStale(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"bars":[],"last_date":"2026-10-15","stale":true}`))
	}))
	defer srv.Close()
	s := NewBaostock(srv.URL, nil, HTTPOptions{QPS: 10000})
	batch, err := s.DailyBars(context.Background(), bsQuery())
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Bars) != 0 {
		t.Fatalf("bars=%d want 0", len(batch.Bars))
	}
	if !batch.Stale {
		t.Fatal("Stale 应透传 true")
	}
	if batch.LastDate != "2026-10-15" {
		t.Fatalf("LastDate=%q want 2026-10-15", batch.LastDate)
	}
}
