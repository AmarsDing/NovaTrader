package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	v1 "server/api/admin/v1"
	"server/pkg/observe"

	"google.golang.org/protobuf/encoding/protojson"
)

func environmentFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := protojson.Marshal(observe.ForService(name).Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func decodeEnvironment(t *testing.T, w *httptest.ResponseRecorder) *v1.GetEnvironmentReply {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", w.Code, w.Body)
	}
	out := &v1.GetEnvironmentReply{}
	if err := protojson.Unmarshal(w.Body.Bytes(), out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestEnvironmentAuthenticationFixedInventorySingleFlightAndCache(t *testing.T) {
	g := testGate("", nil)
	g.Now = time.Now
	g.Settings.Upstreams = map[string]string{}
	var requests, active, peak atomic.Int32
	for _, name := range environmentServices {
		name := name
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			current := active.Add(1)
			defer active.Add(-1)
			for old := peak.Load(); current > old && !peak.CompareAndSwap(old, current); old = peak.Load() {
			}
			if r.URL.Path != "/internal/observability" || r.Header.Get("Authorization") != "" {
				t.Errorf("unsafe probe %s", r.URL.Path)
			}
			time.Sleep(10 * time.Millisecond)
			if observe.RegisteredService(name) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(environmentFixture(t, name))
			} else {
				http.NotFound(w, r)
			}
		}))
		t.Cleanup(server.Close)
		g.Settings.Upstreams[name] = server.URL
	}
	// Unknown configuration keys and request parameters must never become probe destinations.
	g.Settings.Upstreams["unexpected"] = "http://invalid.local/secret"
	if got := observationRequest(t, g, "/api/ops/environment?upstream=http://invalid.local", ""); got.Code != 401 || requests.Load() != 0 {
		t.Fatalf("anonymous probe: %d, requests=%d", got.Code, requests.Load())
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out := decodeEnvironment(t, observationRequest(t, g, "/api/ops/environment", "viewer"))
			if len(out.Services) != 9 || out.Scope != "configured_service_processes" {
				t.Errorf("bad inventory %v", out)
			}
		}()
	}
	wg.Wait()
	if requests.Load() != 9 || peak.Load() > environmentParallel || peak.Load() < 2 {
		t.Fatalf("requests=%d, peak=%d", requests.Load(), peak.Load())
	}
	out := decodeEnvironment(t, observationRequest(t, g, "/api/ops/environment", "owner"))
	if !out.Cached || out.CacheExpiresAt == "" || out.ProbeTimeoutMs != 700 || out.MaxParallelProbes != 3 {
		t.Fatalf("missing cache/probe basis %v", out)
	}
	states := map[string]int{}
	for _, service := range out.Services {
		states[service.State]++
	}
	registered := 0
	for _, name := range environmentServices {
		if observe.RegisteredService(name) {
			registered++
		}
	}
	if states["observed"] != registered || states["unsupported"] != len(environmentServices)-registered {
		t.Fatalf("states %v", states)
	}
	for _, method := range []string{http.MethodPost, http.MethodDelete} {
		w := httptest.NewRecorder()
		g.ServeHTTP(w, httptest.NewRequest(method, "/api/ops/environment", nil))
		if w.Code != http.StatusMethodNotAllowed || requests.Load() != 9 {
			t.Fatalf("write permitted %d", w.Code)
		}
	}
	// Callers receive clones and cannot poison a cache shared by other windows.
	out.Services[0].State = "fake"
	cloned, err := g.environmentSnapshot(context.Background())
	if err != nil || cloned.Services[0].State == "fake" {
		t.Fatal("cache snapshot leaked mutable memory")
	}
	// Only elapsed server time expires the cache; there is no client refresh bypass.
	g.environment.mu.Lock()
	g.environment.expires = g.now().Add(-time.Second)
	g.environment.mu.Unlock()
	decodeEnvironment(t, observationRequest(t, g, "/api/ops/environment?refresh=true", "viewer"))
	if requests.Load() != 18 {
		t.Fatalf("cache did not expire: %d", requests.Load())
	}
}

func TestEnvironmentProbeClassifiesUnknownWithoutLeakingUpstream(t *testing.T) {
	g := testGate("", nil)
	g.Now = time.Now
	fixture := environmentFixture(t, "market")
	for _, tc := range []struct {
		name, base, body, state string
		status                  int
	}{
		{name: "missing", state: "unconfigured"},
		{name: "credentials", base: "http://user:secret@localhost:1", state: "invalid"},
		{name: "unsupported-scheme", base: "file:///secret", state: "invalid"},
		{name: "path", base: "http://localhost:1/private", state: "invalid"},
		{name: "unknown-endpoint", status: 404, state: "unsupported", body: "secret upstream failure"},
		{name: "http-failure", status: 500, state: "unknown", body: "secret upstream failure"},
		{name: "bad-contract", status: 200, state: "invalid", body: `{"secret":"leaked"}`},
		{name: "oversized", status: 200, state: "invalid", body: strings.Repeat("secret", environmentBodyMax)},
		{name: "valid", status: 200, state: "observed", body: string(fixture)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := tc.base
			if tc.status != 0 {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(tc.body))
				}))
				defer server.Close()
				base = server.URL
			}
			out := g.probeEnvironment(context.Background(), &http.Client{Timeout: time.Second}, "market", base)
			if out.State != tc.state || strings.Contains(out.Note, "secret") || out.CheckedAt == "" || (tc.state != "observed" && out.Observation != nil) {
				t.Fatalf("unsafe classification %v", out)
			}
		})
	}
}

func TestEnvironmentDoesNotFollowRedirectOrTreatOldSampleAsReady(t *testing.T) {
	g := testGate("", nil)
	g.Now = time.Now
	var reached atomic.Bool
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached.Store(true) }))
	defer destination.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, http.StatusFound) }))
	defer redirect.Close()
	g.Settings.Upstreams = map[string]string{"market": redirect.URL}
	out := g.collectEnvironment(context.Background())
	if reached.Load() || out.Services[1].State != "unknown" {
		t.Fatalf("redirect followed or accepted: %v", out.Services[1])
	}
	old := observe.ForService("market").Snapshot()
	old.SampledAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	body, _ := protojson.Marshal(old)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }))
	defer server.Close()
	got := g.probeEnvironment(context.Background(), &http.Client{Timeout: time.Second}, "market", server.URL)
	if got.State != "unknown" || got.Observation == nil || !strings.Contains(got.Note, "clock_skew_unknown") {
		t.Fatalf("old sample treated as fresh: %v", got)
	}
}
