package gateway

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	v1 "server/api/admin/v1"

	"google.golang.org/protobuf/encoding/protojson"
)

// P2-10 服务端时钟读口：鉴权、只读、双字段同时刻、可注入时钟、无缓存直读。
func TestSystemClockAuthGetOnlyAndEpochConsistency(t *testing.T) {
	g := testGate("", nil)
	g.Now = time.Now

	if got := observationRequest(t, g, "/api/ops/time", ""); got.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous clock read: %d", got.Code)
	}

	before := time.Now().UnixMilli()
	w := observationRequest(t, g, "/api/ops/time", "viewer")
	after := time.Now().UnixMilli()
	if w.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", w.Code, w.Body)
	}
	out := &v1.GetServerTimeReply{}
	if err := protojson.Unmarshal(w.Body.Bytes(), out); err != nil {
		t.Fatal(err)
	}
	if out.EpochMs < before || out.EpochMs > after {
		t.Fatalf("epochMs %d outside [%d, %d]", out.EpochMs, before, after)
	}
	parsed, err := time.Parse(time.RFC3339Nano, out.ServerTime)
	if err != nil {
		t.Fatalf("server_time not RFC3339Nano: %q", out.ServerTime)
	}
	// server_time 与 epoch_ms 是同一时刻的两种编码（毫秒粒度对齐）。
	if parsed.UnixMilli() != out.EpochMs {
		t.Fatalf("server_time %s != epoch_ms %d", out.ServerTime, out.EpochMs)
	}

	// P2-10 合同「无缓存」显式钉死（评审 nit）：任何一层缓存都会污染前端漂移补偿。
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("clock response must be no-store, got %q", got)
	}

	for _, method := range []string{http.MethodPost, http.MethodDelete} {
		w := httptest.NewRecorder()
		g.ServeHTTP(w, httptest.NewRequest(method, "/api/ops/time", nil))
		if w.Code != http.StatusMethodNotAllowed {
			t.Fatalf("write permitted: %d", w.Code)
		}
	}
}

// 注入时钟直接决定读数：两连读必须真推进（无缓存），且每次读数都落在注入序列上。
func TestSystemClockUsesGateNowWithoutCaching(t *testing.T) {
	g := testGate("", nil)
	base := time.Date(2026, 10, 19, 9, 15, 0, 123000000, time.FixedZone("CST", 8*3600))
	var seq atomic.Int64
	g.Now = func() time.Time {
		return base.Add(time.Duration(seq.Add(1)) * 42 * time.Millisecond)
	}
	baseMs := base.UnixMilli()
	onSequence := func(epochMs int64) bool {
		return epochMs > baseMs && (epochMs-baseMs)%42 == 0
	}

	first := &v1.GetServerTimeReply{}
	if err := protojson.Unmarshal(observationRequest(t, g, "/api/ops/time", "viewer").Body.Bytes(), first); err != nil {
		t.Fatal(err)
	}
	second := &v1.GetServerTimeReply{}
	if err := protojson.Unmarshal(observationRequest(t, g, "/api/ops/time", "viewer").Body.Bytes(), second); err != nil {
		t.Fatal(err)
	}
	// Issue/Parse 等同请求路径也吃 g.now()（会占序列刻度），只断言读数来源正确与两读真实推进。
	if !onSequence(first.EpochMs) || !onSequence(second.EpochMs) {
		t.Fatalf("reads not sourced from injected clock: first=%d second=%d base=%d", first.EpochMs, second.EpochMs, baseMs)
	}
	if second.EpochMs <= first.EpochMs {
		t.Fatalf("clock reads cached: first=%d second=%d", first.EpochMs, second.EpochMs)
	}
}
