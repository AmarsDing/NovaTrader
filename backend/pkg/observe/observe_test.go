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

// 调用点⊆注册表静态守卫：app 各服务代码里的 Op("key") 字面量必须属于该文件中
// ForService("svc") 声明过的服务注册集，漏注册（如 market_alert_handle 曾路过
// 只判 def 存在性的测试）在此显红，不等到运行时 panic 才暴露。
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
