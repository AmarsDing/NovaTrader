package data

// M13-T01 四上游端口 data 侧客户端降级路径测试：
// - marketClient.Stage：404/未知阶段=显式缺失（ok=false 无错），5xx/未配置=err，60s 同日缓存；
// - newsClient.Negative：阈值过滤、404=确无利空（空集），单只失败进 missing，未配置整轮缺失；
// - marketStub / intelStub 供 market_cache/data_test 的行情语义用例复用。

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	marketv1 "server/api/market/v1"
	intelv1 "server/api/intel/v1"
	"server/conf"
	"server/pkg/rules"
	"server/pkg/tradecal"

	"github.com/go-kratos/kratos/v2/log"
	"google.golang.org/protobuf/encoding/protojson"
)

// ---- market 桩：/v1/market/bars（按 start/end 过滤）+ /v1/market/sentiment ----

type stubMarket struct {
	*httptest.Server

	mu        sync.Mutex
	bars      map[string][]*marketv1.Bar
	phase     string // 空串 → sentiment 回 404
	barsReq   atomic.Int32
	phaseReq  atomic.Int32
	barsQuery []string // 每次 bars 请求的 query 串（钉参数用）
}

func newStubMarket() *stubMarket {
	s := &stubMarket{bars: map[string][]*marketv1.Bar{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/market/bars", s.serveBars)
	mux.HandleFunc("/v1/market/sentiment", s.serveSentiment)
	s.Server = httptest.NewServer(mux)
	return s
}

func (s *stubMarket) setBars(symbol string, bars ...*marketv1.Bar) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bars[symbol] = bars
}

func (s *stubMarket) serveBars(w http.ResponseWriter, r *http.Request) {
	s.barsReq.Add(1)
	q := r.URL.Query()
	s.mu.Lock()
	s.barsQuery = append(s.barsQuery, q.Encode())
	rows := append([]*marketv1.Bar(nil), s.bars[q.Get("symbol")]...)
	s.mu.Unlock()
	lo, hi := q.Get("start"), q.Get("end")
	out := &marketv1.GetBarsReply{Symbol: q.Get("symbol"), Freq: "1d"}
	for _, b := range rows {
		t, err := time.Parse(time.RFC3339, b.GetTime())
		if err != nil {
			continue
		}
		d := t.In(tradecal.Shanghai()).Format("2006-01-02")
		if (lo == "" || d >= lo) && (hi == "" || d <= hi) {
			out.Bars = append(out.Bars, b)
		}
	}
	raw, _ := protojson.Marshal(out)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(raw)
}

func (s *stubMarket) serveSentiment(w http.ResponseWriter, r *http.Request) {
	s.phaseReq.Add(1)
	s.mu.Lock()
	phase := s.phase
	s.mu.Unlock()
	if phase == "" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	raw, _ := protojson.Marshal(&marketv1.Sentiment{TradeDate: r.URL.Query().Get("trade_date"), Phase: phase})
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(raw)
}

func bar(day string, close, adj float64) *marketv1.Bar {
	t, _ := time.ParseInLocation("2006-01-02", day, tradecal.Shanghai())
	return &marketv1.Bar{Time: t.In(time.UTC).Format(time.RFC3339), Open: close, High: close, Low: close,
		Close: close, Volume: 1000, Amount: 1e8, AdjFactor: adj}
}

// ---- intel 桩：/v1/intel/stocks/{code}/timeline ----

type stubIntel struct {
	*httptest.Server

	mu     sync.Mutex
	items  map[string][]*intelv1.Item // code → 时间线条目（不再按窗口过滤，交给客户端）
	status map[string]int             // code → HTTP 状态（默认 200；404 当无情报）
}

func newStubIntel() *stubIntel {
	s := &stubIntel{items: map[string][]*intelv1.Item{}, status: map[string]int{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/intel/stocks/", func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/v1/intel/stocks/")
		code := strings.TrimSuffix(rest, "/timeline")
		s.mu.Lock()
		status, items := s.status[code], s.items[code]
		s.mu.Unlock()
		if status == 0 {
			status = http.StatusOK
		}
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		raw, _ := protojson.Marshal(&intelv1.TimelineReply{Code: code, Items: items})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw)
	})
	s.Server = httptest.NewServer(mux)
	return s
}

var newsAsOf = time.Date(2026, 10, 9, 15, 0, 0, 0, tradecal.Shanghai())

func item(at time.Time, importance int32, sentiment float64) *intelv1.Item {
	return &intelv1.Item{PublishTime: at.Format(time.RFC3339), Importance: importance, Sentiment: sentiment}
}

func TestMarketClientStage(t *testing.T) {
	newClient := func(s *stubMarket) *marketClient {
		return NewMarketClient(&conf.Strategy{MarketAddr: s.URL}, log.DefaultLogger)
	}
	t.Run("200 phase maps to stage", func(t *testing.T) {
		s := newStubMarket()
		s.phase = "HOT"
		c := newClient(s)
		stage, ok, err := c.Stage(ctx, newsAsOf)
		if err != nil || !ok || stage != rules.StageHot {
			t.Fatalf("stage=%q ok=%v err=%v", stage, ok, err)
		}
		// 60s 同日缓存：第二次不打市场端。
		if stage, ok, err = c.Stage(ctx, newsAsOf.Add(2*time.Hour)); err != nil || !ok || stage != rules.StageHot {
			t.Fatalf("cached stage=%q ok=%v err=%v", stage, ok, err)
		}
		if s.phaseReq.Load() != 1 {
			t.Fatalf("cache must suppress re-request, got %d", s.phaseReq.Load())
		}
	})
	t.Run("404 is explicit missing not error", func(t *testing.T) {
		s := newStubMarket() // phase 空 → 404
		c := newClient(s)
		stage, ok, err := c.Stage(ctx, newsAsOf)
		if err != nil || ok || stage != "" {
			t.Fatalf("stage=%q ok=%v err=%v", stage, ok, err)
		}
		// 404 也缓存，早盘前每轮扫描不应反复打市场端。
		if _, _, _ = c.Stage(ctx, newsAsOf); s.phaseReq.Load() != 1 {
			t.Fatalf("404 must be cached, got %d", s.phaseReq.Load())
		}
	})
	t.Run("unknown phase is missing not guessed", func(t *testing.T) {
		s := newStubMarket()
		s.phase = "SIDEWAYS"
		c := newClient(s)
		stage, ok, err := c.Stage(ctx, newsAsOf)
		if err != nil || ok || stage != "" {
			t.Fatalf("stage=%q ok=%v err=%v", stage, ok, err)
		}
	})
	t.Run("5xx is error", func(t *testing.T) {
		s := newStubMarket()
		mux := http.NewServeMux()
		mux.HandleFunc("/v1/market/sentiment", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) })
		s.Close()
		s.Server = httptest.NewServer(mux)
		c := newClient(s)
		defer s.Close()
		if _, _, err := c.Stage(ctx, newsAsOf); err == nil {
			t.Fatal("5xx must surface as error")
		}
	})
	t.Run("addr empty is error", func(t *testing.T) {
		c := NewMarketClient(&conf.Strategy{}, log.DefaultLogger)
		if _, _, err := c.Stage(ctx, newsAsOf); !errors.Is(err, errNoMarketAddr) {
			t.Fatalf("err=%v", err)
		}
	})
}

func TestNewsClientNegative(t *testing.T) {
	newClient := func(s *stubIntel) (*newsClient, *stubIntel) {
		return NewNewsClient(&conf.Strategy{IntelAddr: s.URL}, log.DefaultLogger).(*newsClient), s
	}
	within := newsAsOf.Add(-time.Hour)          // 窗口内
	stale := newsAsOf.Add(-25 * time.Hour)      // 窗口外（24h）
	t.Run("thresholds", func(t *testing.T) {
		c, s := newClient(newStubIntel())
		defer s.Close()
		s.items["a.sh"] = []*intelv1.Item{item(within, 4, -0.5)}      // 卡线命中
		s.items["b.sh"] = []*intelv1.Item{item(within, 3, -0.9)}       // 重要度不够
		s.items["c.sh"] = []*intelv1.Item{item(within, 5, -0.3)}       // 情感不够空
		s.items["d.sh"] = []*intelv1.Item{item(stale, 5, -0.9)}        // 窗口外
		s.items["e.sh"] = []*intelv1.Item{item(within.Add(2*time.Hour), 5, -0.9)} // 晚于 asOf
		set, missing, err := c.Negative(ctx, []string{"a.sh", "b.sh", "c.sh", "d.sh", "e.sh"}, newsAsOf)
		if err != nil || len(missing) != 0 {
			t.Fatalf("missing=%v err=%v", missing, err)
		}
		if len(set) != 1 || !set["a.sh"] {
			t.Fatalf("set=%v", set)
		}
	})
	t.Run("404 is honest empty not missing", func(t *testing.T) {
		c, s := newClient(newStubIntel())
		defer s.Close()
		s.status["gone.sh"] = http.StatusNotFound // M03：无该股情报 = 确无利空
		set, missing, err := c.Negative(ctx, []string{"gone.sh"}, newsAsOf)
		if err != nil || len(set) != 0 || len(missing) != 0 {
			t.Fatalf("set=%v missing=%v err=%v", set, missing, err)
		}
	})
	t.Run("per-symbol failure marks missing only for it", func(t *testing.T) {
		c, s := newClient(newStubIntel())
		defer s.Close()
		s.items["ok.sh"] = []*intelv1.Item{item(within, 5, -0.9)}
		s.status["bad.sh"] = http.StatusInternalServerError
		set, missing, err := c.Negative(ctx, []string{"ok.sh", "bad.sh"}, newsAsOf)
		if err != nil || !set["ok.sh"] || len(missing) != 1 || !missing["bad.sh"] {
			t.Fatalf("set=%v missing=%v err=%v", set, missing, err)
		}
	})
	t.Run("addr empty fails whole call", func(t *testing.T) {
		c := NewNewsClient(&conf.Strategy{}, log.DefaultLogger)
		set, missing, err := c.Negative(ctx, []string{"a.sh", "b.sh"}, newsAsOf)
		if !errors.Is(err, errNoIntelAddr) || len(set) != 0 || !missing["a.sh"] || !missing["b.sh"] {
			t.Fatalf("set=%v missing=%v err=%v", set, missing, err)
		}
	})
	t.Run("no symbols is trivial", func(t *testing.T) {
		c, _ := newClient(newStubIntel())
		set, missing, err := c.Negative(ctx, nil, newsAsOf)
		if err != nil || len(set) != 0 || len(missing) != 0 {
			t.Fatalf("set=%v missing=%v err=%v", set, missing, err)
		}
	})
}
