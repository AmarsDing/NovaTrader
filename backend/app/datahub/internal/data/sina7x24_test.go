package data

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"server/app/datahub/internal/biz"
)

const sina7x24Fixture = `{
  "result": {
    "status": {"code": 0, "msg": "success"},
    "data": {
      "feed": {
        "list": [
          {
            "id": 6453093,
            "rich_text": "<p>【快讯】<a href='https://finance.sina.com.cn/x'>A股</a>三大指数集体收涨，沪指涨1.2%</p>",
            "create_time": "2026-10-20 09:30:00",
            "is_delete": 0,
            "ext": "{\"docurl\":\"https://finance.sina.com.cn/roll/2026-10-20/doc-12345.shtml\",\"stocks\":[{\"symbol\":\"sh600519\",\"name\":\"贵州茅台\"}]}"
          },
          {
            "id": 6453094,
            "rich_text": "<p>已删除的快讯</p>",
            "create_time": "2026-10-20 09:31:00",
            "is_delete": 1,
            "ext": ""
          },
          {
            "id": 6453095,
            "rich_text": "",
            "create_time": "2026-10-20 09:32:00",
            "is_delete": 0,
            "ext": ""
          },
          {
            "id": 6453096,
            "rich_text": "<p>时间坏掉的快讯</p>",
            "create_time": "not-a-time",
            "is_delete": 0,
            "ext": ""
          }
        ]
      }
    }
  }
}`

// newSina7x24Srv 起 mock：无 Referer 返 403（已核实的反爬行为），有 Referer 返 body。
func newSina7x24Srv(t *testing.T, body string) (*Sina7x24, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Referer") != "https://finance.sina.com.cn" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"forbidden"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	s := NewSina7x24(nil, HTTPOptions{QPS: 10000})
	s.base = srv.URL
	return s, srv
}

// Referer 校验门：mock 无 Referer 返 403、有则返 fixture；客户端经 newHTTPC headers 注入 Referer 后成功。
// 剥掉注入头再采必失败——锚住反爬行为（正反两支一起看才证明成功不是靠服务端放水）。
func TestSina7x24RefererGate(t *testing.T) {
	s, srv := newSina7x24Srv(t, sina7x24Fixture)
	defer srv.Close()
	items, err := s.Intel(context.Background(), biz.KindFlash, biz.IntelQuery{Limit: 20})
	if err != nil {
		t.Fatalf("Referer 注入后应成功: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("应取到条目")
	}
	if got := items[0].Source; got != biz.SourceSina7x24 {
		t.Fatalf("Source=%q want %q", got, biz.SourceSina7x24)
	}

	s.hc.headers = nil // 剥掉注入头，模拟没配 Referer 的客户端
	if _, err := s.Intel(context.Background(), biz.KindFlash, biz.IntelQuery{}); err == nil {
		t.Fatal("无 Referer 直采应失败（403）")
	}
}

// kind!=flash → ErrUnsupported（链条跳过，不计失败）。
func TestSina7x24UnsupportedKind(t *testing.T) {
	s, srv := newSina7x24Srv(t, sina7x24Fixture)
	defer srv.Close()
	for _, kind := range []string{biz.KindNews, biz.KindAnnouncement, biz.KindReport} {
		if _, err := s.Intel(context.Background(), kind, biz.IntelQuery{}); !errors.Is(err, biz.ErrUnsupported) {
			t.Fatalf("kind=%s 应 ErrUnsupported，得 %v", kind, err)
		}
	}
}

// 全字段映射：id→SourceID、create_time→+08:00 RFC3339、HTML 被剥、docurl→URL、
// is_delete/空正文/坏时间过滤、sh600519→600519.SH。
func TestSina7x24FieldMapping(t *testing.T) {
	s, srv := newSina7x24Srv(t, sina7x24Fixture)
	defer srv.Close()
	items, err := s.Intel(context.Background(), biz.KindFlash, biz.IntelQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("应只剩 1 条（删除/空正文/坏时间各滤 1），得 %d: %+v", len(items), items)
	}
	it := items[0]
	if it.SourceID != "6453093" {
		t.Fatalf("SourceID=%q want 6453093", it.SourceID)
	}
	if it.PublishTime != "2026-10-20T09:30:00+08:00" {
		t.Fatalf("PublishTime=%q want 2026-10-20T09:30:00+08:00", it.PublishTime)
	}
	wantContent := "【快讯】A股三大指数集体收涨，沪指涨1.2%"
	if it.Content != wantContent {
		t.Fatalf("Content=%q want %q", it.Content, wantContent)
	}
	if it.Title != truncate(wantContent, 60) {
		t.Fatalf("Title=%q want truncate(60)", it.Title)
	}
	if it.URL != "https://finance.sina.com.cn/roll/2026-10-20/doc-12345.shtml" {
		t.Fatalf("URL=%q want docurl", it.URL)
	}
	if it.Kind != biz.KindFlash {
		t.Fatalf("Kind=%q want flash", it.Kind)
	}
	if len(it.Codes) != 1 || it.Codes[0] != "600519.SH" {
		t.Fatalf("Codes=%v want [600519.SH]", it.Codes)
	}
}

// result.status.code!=0 → 报错（新浪侧业务失败要看成源失败，不当空列表）。
func TestSina7x24StatusError(t *testing.T) {
	s, srv := newSina7x24Srv(t, `{"result":{"status":{"code":1001,"msg":"param error"},"data":{"feed":{"list":[]}}}}`)
	defer srv.Close()
	if _, err := s.Intel(context.Background(), biz.KindFlash, biz.IntelQuery{}); err == nil {
		t.Fatal("status.code!=0 应报错")
	}
}

// 空 feed.list → 空片不报错（合法结果，非缺失冒充）。
func TestSina7x24EmptyList(t *testing.T) {
	s, srv := newSina7x24Srv(t, `{"result":{"status":{"code":0,"msg":""},"data":{"feed":{"list":[]}}}}`)
	defer srv.Close()
	items, err := s.Intel(context.Background(), biz.KindFlash, biz.IntelQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("应为空片，得 %d", len(items))
	}
}

// feeds[0] 覆盖 zhibo_id：请求查询串应带覆盖值；page_size 夹 [20,50]。
func TestSina7x24ZhiboIDOverride(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":{"status":{"code":0},"data":{"feed":{"list":[]}}}}`))
	}))
	defer srv.Close()
	s := NewSina7x24([]string{"153"}, HTTPOptions{QPS: 10000})
	s.base = srv.URL
	if _, err := s.Intel(context.Background(), biz.KindFlash, biz.IntelQuery{Limit: 30}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotQuery, "zhibo_id=153") {
		t.Fatalf("query=%q 缺 zhibo_id=153", gotQuery)
	}
	if !strings.Contains(gotQuery, "page_size=30") {
		t.Fatalf("query=%q 缺 page_size=30", gotQuery)
	}
}
