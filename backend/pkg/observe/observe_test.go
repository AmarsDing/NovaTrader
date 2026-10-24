package observe

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	v1 "server/api/platform/v1"

	khttp "github.com/go-kratos/kratos/v2/transport/http"
	"google.golang.org/protobuf/encoding/protojson"
)

func find(t *testing.T, r *Registry, key string) *v1.ConsumerOperation {
	t.Helper()
	for _, op := range r.Snapshot().Operations {
		if op.Key == key {
			return op
		}
	}
	t.Fatalf("missing %s", key)
	return nil
}

func TestNotificationsDoNotClaimBusinessSuccess(t *testing.T) {
	at := time.Date(2026, 10, 18, 1, 0, 0, 0, time.UTC)
	r := newRegistry("market", definitions["market"], func() time.Time { return at })
	r.Op("snapshot_notice").Received()
	r.Op("snapshot_notice").Queued()
	r.Op("snapshot_notice").Received()
	r.Op("snapshot_notice").Coalesced()
	notice := find(t, r, "snapshot_notice")
	refresh := find(t, r, "refresh")
	if notice.Total.Received != 2 || notice.Total.Queued != 1 || notice.Total.Coalesced != 1 || notice.Total.Succeeded != 0 || refresh.Total.Attempted != 0 || notice.Total.DurationP95UpperMs != nil {
		t.Fatalf("notification confused with business: %v / %v", notice, refresh)
	}
	if r.Snapshot().WindowComplete || r.Snapshot().WindowObservedSeconds != 0 {
		t.Fatal("new process must not claim a full minute")
	}
}

func TestFinishOnceConcurrentAndWrappedCancellation(t *testing.T) {
	at := time.Now()
	r := newRegistry("intel", definitions["intel"], func() time.Time { return at })
	finish := r.Op("raw").Begin()
	if got := find(t, r, "raw"); got.Inflight != 1 || got.Total.Attempted != 1 || got.Total.Succeeded != 0 {
		t.Fatalf("before completion %v", got)
	}
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); FinishError(finish, fmt.Errorf("wrapped: %w", context.Canceled)) }()
	}
	wg.Wait()
	got := find(t, r, "raw")
	if got.Inflight != 0 || got.Total.Cancelled != 1 || got.Total.Failed != 0 || got.Total.DurationCount != 1 || got.Total.DurationP95UpperMs == nil || *got.Total.DurationP95UpperMs != 1 {
		t.Fatalf("finish was not atomic: %v", got)
	}
	if err := ValidateSnapshot("intel", r.Snapshot()); err != nil {
		t.Fatal(err)
	}
}

func TestWindowExpiresAndLongDurationRemainsUnknown(t *testing.T) {
	at := time.Date(2026, 10, 18, 1, 0, 0, 0, time.UTC)
	r := newRegistry("market", definitions["market"], func() time.Time { return at })
	op := r.Op("refresh")
	finish := op.Begin()
	at = at.Add(61 * time.Second)
	finish(Succeeded)
	got := find(t, r, "refresh")
	if got.Window.Attempted != 0 || got.Window.Succeeded != 1 || got.Window.DurationCount != 1 || got.Total.DurationP95UpperMs != nil || got.Total.DurationMaxMs != 61000 || !r.Snapshot().WindowComplete {
		t.Fatalf("duration belongs to completion bucket: %v", got)
	}
	at = at.Add(60 * time.Second)
	got = find(t, r, "refresh")
	if got.Window.Succeeded != 0 || got.Total.Succeeded != 1 || got.Window.DurationP95UpperMs != nil {
		t.Fatalf("window retained expired data: %v", got)
	}
}

func TestRegistryValidationRejectsArbitraryLabelsAndCounterCorruption(t *testing.T) {
	r := newRegistry("strategy", definitions["strategy"], time.Now)
	for _, mutate := range []func(*v1.ConsumerObservation){
		func(in *v1.ConsumerObservation) { in.Service = "trade" },
		func(in *v1.ConsumerObservation) { in.SchemaVersion = 2 },
		func(in *v1.ConsumerObservation) { in.Operations[0].Input = "secret-payload" },
		func(in *v1.ConsumerObservation) { in.Operations[0].Total.Succeeded = 1 },
		func(in *v1.ConsumerObservation) { in.Operations[0].Inflight = 1 },
		func(in *v1.ConsumerObservation) { in.Operations[0] = in.Operations[1] },
		func(in *v1.ConsumerObservation) { in.WindowComplete = true },
	} {
		in := r.Snapshot()
		mutate(in)
		if err := ValidateSnapshot("strategy", in); err == nil {
			t.Fatalf("accepted corrupted snapshot: %v", in)
		}
	}
}

func TestGeneratedHTTPReturnsDeclaredScopeOnly(t *testing.T) {
	r := newRegistry("intel", definitions["intel"], time.Now)
	server := khttp.NewServer()
	RegisterHTTP(server, r)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/internal/observability", nil))
	if w.Code != 200 {
		t.Fatalf("HTTP %d: %s", w.Code, w.Body)
	}
	out := &v1.ConsumerObservation{}
	if err := protojson.Unmarshal(w.Body.Bytes(), out); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSnapshot("intel", out); err != nil {
		t.Fatal(err)
	}
	if out.Scope != "process_consumer_operations" || len(out.Unsupported) == 0 || out.Operations[2].Total.DurationP95UpperMs != nil {
		t.Fatalf("unknown values or scope lost %v", out)
	}
}

func TestTradeBacktestRegistryResetAndFixedLabels(t *testing.T) {
	for _, name := range []string{"trade", "backtest"} {
		t.Run(name, func(t *testing.T) {
			at := time.Now()
			r := newRegistry(name, definitions[name], func() time.Time { return at })
			for key := range r.ops {
				r.Op(key).Begin()(Skipped)
			}
			if err := ValidateSnapshot(name, r.Snapshot()); err != nil {
				t.Fatal(err)
			}
			first := r.Snapshot()
			fresh := newRegistry(name, definitions[name], func() time.Time { return at }).Snapshot()
			if first.InstanceId == fresh.InstanceId {
				t.Fatal("restart identity retained")
			}
			for _, op := range fresh.Operations {
				if op.Total.Attempted != 0 || op.Total.DurationP95UpperMs != nil {
					t.Fatal("new process fabricated samples")
				}
			}
			at = at.Add(61 * time.Second)
			for _, op := range r.Snapshot().Operations {
				if op.Window.Skipped != 0 || op.Total.Skipped != 1 {
					t.Fatal("window/reset mixed", op)
				}
			}
			func() {
				defer func() {
					if recover() == nil {
						t.Fatal("arbitrary operation label accepted")
					}
				}()
				r.Op("600519.SH-user-payload")
			}()
		})
	}
}

func TestEveryRegisteredServiceHasClosedValidHTTPContract(t *testing.T) {
	for name, defs := range definitions {
		t.Run(name, func(t *testing.T) {
			r := newRegistry(name, defs, time.Now)
			for _, d := range defs {
				r.Op(d.key).Begin()(Succeeded)
			}
			s := khttp.NewServer()
			RegisterHTTP(s, r)
			w := httptest.NewRecorder()
			s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/internal/observability", nil))
			out := &v1.ConsumerObservation{}
			if w.Code != 200 {
				t.Fatal(w.Code)
			}
			if err := protojson.Unmarshal(w.Body.Bytes(), out); err != nil {
				t.Fatal(err)
			}
			if err := ValidateSnapshot(name, out); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestBeginFromE2E 钉样端到端时延三态：source_time 优先基准、零 source 退发布
// 时刻、两者皆零或负偏差进 skewed（绝不取零冒充零时延）；Begin 不进 e2e 桶。
func TestBeginFromE2E(t *testing.T) {
	base := time.Date(2026, 10, 24, 9, 30, 0, 0, time.UTC)
	at := base
	r := newRegistry("market", definitions["market"], func() time.Time { return at })
	op := r.Op("refresh")

	op.BeginFrom(base.Add(-1500*time.Millisecond), time.Time{})(Succeeded) // from_source=1，e2e=1500ms → 2500 档
	op.BeginFrom(time.Time{}, base.Add(-100*time.Millisecond))(Succeeded)  // no_source_time=1，e2e=100ms → 100 档
	op.BeginFrom(time.Time{}, time.Time{})(Failed)                         // 无基准 → skewed
	op.BeginFrom(base.Add(2500*time.Millisecond), time.Time{})(Failed)     // 负偏差 → skewed
	op.Begin()(Skipped)                                                    // Begin 不带 e2e：三桶不进

	var m *v1.ConsumerOperation
	for _, o := range r.Snapshot().Operations {
		if o.Key == "refresh" {
			m = o
		}
	}
	if m == nil {
		t.Fatal("op missing")
	}
	if m.Total.Attempted != 5 || m.Total.DurationCount != 5 {
		t.Fatalf("duration 治应照旧: %+v", m.Total)
	}
	if m.Total.E2ECount != 2 || m.Total.E2EFromSourceCount != 1 || m.Total.E2ENoSourceTimeCount != 1 || m.Total.E2ESkewedCount != 2 {
		t.Fatalf("e2e 三桶不符: %+v", m.Total)
	}
	wantTotal := 1500.0 + 100.0
	if m.Total.E2ETotalMs != wantTotal {
		t.Fatalf("e2eTotal = %v, want %v", m.Total.E2ETotalMs, wantTotal)
	}
	if m.Total.E2EMaxMs != 1500 {
		t.Fatalf("e2eMax = %v, want 1500", m.Total.E2EMaxMs)
	}
	// P95(2 样本)→ 向上取到 2500ms 档（含 1500ms 那格）。
	if m.Total.E2EP95UpperMs == nil || *m.Total.E2EP95UpperMs != 2500 {
		t.Fatalf("e2eP95Upper = %v, want 2500", m.Total.E2EP95UpperMs)
	}
	if m.Window.E2ECount != 2 { // 同秒窗口与累计同口径
		t.Fatalf("window e2e = %d, want 2", m.Window.E2ECount)
	}
	// 契约校验对 e2e 不变式开绿灯（from_source + no_source_time == count）。
	if err := ValidateSnapshot("market", r.Snapshot()); err != nil {
		t.Fatal(err)
	}
}

func TestOpCallSitesRegistered(t *testing.T) {
	forService := regexp.MustCompile(`ForService\("([a-z0-9_]+)"\)`)
	opKey := regexp.MustCompile(`\.Op\("([a-z0-9_.]+)"\)`)
	root := filepath.Join("..", "..", "app")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		services := map[string]bool{}
		for _, m := range forService.FindAllSubmatch(raw, -1) {
			services[string(m[1])] = true
		}
		if len(services) == 0 {
			return nil
		}
		for _, m := range opKey.FindAllSubmatch(raw, -1) {
			key := string(m[1])
			ok := false
			for svc := range services {
				defs, known := definitions[svc]
				if !known {
					continue
				}
				for _, def := range defs {
					if def.key == key {
						ok = true
						break
					}
				}
			}
			if !ok {
				t.Errorf("%s: Op(%q) 不在该文件 ForService 声明的服务注册集内（漏注册会运行时 panic）", path, key)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
