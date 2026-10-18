package gateway

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	v1 "server/api/admin/v1"
	platform "server/api/platform/v1"
	"server/pkg/observe"

	"github.com/go-kratos/kratos/v2/errors"
	"github.com/go-kratos/kratos/v2/transport"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const (
	environmentCacheFor = 5 * time.Second
	environmentTimeout  = 700 * time.Millisecond
	environmentParallel = 3
	environmentBodyMax  = 128 << 10
)

// The request never selects a destination. Only these configured service keys are probed.
var environmentServices = [...]string{"datahub", "market", "intel", "brain", "strategy", "backtest", "risk", "trade", "notify"}

type environmentCache struct {
	mu       sync.Mutex
	snapshot *v1.GetEnvironmentReply
	expires  time.Time
	inflight chan struct{}
}

type environmentService struct{ gate *Gate }

func (s environmentService) GetEnvironment(ctx context.Context, _ *v1.GetEnvironmentRequest) (*v1.GetEnvironmentReply, error) {
	tr, ok := transport.FromServerContext(ctx)
	if !ok {
		return nil, errors.Unauthorized("AUTH_REQUIRED", "未登录")
	}
	r := &http.Request{Header: http.Header{"Authorization": []string{tr.RequestHeader().Get("Authorization")}}}
	if _, ok := s.gate.principal(r); !ok {
		return nil, errors.Unauthorized("AUTH_REQUIRED", "未登录")
	}
	return s.gate.environmentSnapshot(ctx)
}

func (g *Gate) serveEnvironment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeErr(w, http.StatusMethodNotAllowed, "环境观测接口只读")
		return
	}
	g.environmentOnce.Do(func() {
		server := khttp.NewServer()
		v1.RegisterEnvironmentObservabilityHTTPServer(server, environmentService{gate: g})
		g.environmentHandler = server
	})
	g.environmentHandler.ServeHTTP(w, r)
}

func (g *Gate) environmentSnapshot(ctx context.Context) (*v1.GetEnvironmentReply, error) {
	c := &g.environment
	c.mu.Lock()
	if c.snapshot != nil && g.now().Before(c.expires) {
		out := proto.Clone(c.snapshot).(*v1.GetEnvironmentReply)
		out.Cached = true
		c.mu.Unlock()
		return out, nil
	}
	if c.inflight != nil {
		wait := c.inflight
		c.mu.Unlock()
		select {
		case <-wait:
			return g.environmentSnapshot(ctx)
		case <-ctx.Done():
			return nil, errors.ServiceUnavailable("OBSERVATION_BUSY", "环境观测正在采样")
		}
	}
	c.inflight = make(chan struct{})
	c.mu.Unlock()
	// One bounded sampling operation is shared by concurrent windows. A disconnect does not
	// abort the cache population or multiply probes. No background polling is created.
	probeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	out := g.collectEnvironment(probeCtx)
	cancel()
	c.mu.Lock()
	c.snapshot, c.expires = out, g.now().Add(environmentCacheFor)
	out.CacheExpiresAt = observationTime(c.expires)
	close(c.inflight)
	c.inflight = nil
	result := proto.Clone(out).(*v1.GetEnvironmentReply)
	c.mu.Unlock()
	return result, nil
}

func (g *Gate) collectEnvironment(ctx context.Context) *v1.GetEnvironmentReply {
	out := &v1.GetEnvironmentReply{SchemaVersion: 1, Scope: "configured_service_processes", CacheSeconds: 5, ProbeTimeoutMs: int32(environmentTimeout / time.Millisecond), MaxParallelProbes: environmentParallel,
		Services: make([]*v1.EnvironmentService, len(environmentServices)), Unsupported: []string{"unconfigured_hosts_and_processes", "load_balanced_sibling_instances", "broker_ack_confirmation", "cross_host_clock_skew", "end_to_end_latency", "host_cpu_and_disk_utilization", "unregistered_operations"},
		Note: "固定配置服务的当前HTTP实例观测；收到消息、本地队列、业务完成分别计数。探针耗时仅为admin HTTP往返。未登记服务或操作保持未知，不等于故障或成功。"}
	client := &http.Client{Timeout: environmentTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	limit := make(chan struct{}, environmentParallel)
	var wg sync.WaitGroup
	for i, name := range environmentServices {
		wg.Add(1)
		go func(i int, name, base string) {
			defer wg.Done()
			select {
			case limit <- struct{}{}:
				defer func() { <-limit }()
			case <-ctx.Done():
				out.Services[i] = &v1.EnvironmentService{Service: name, State: "unknown", CheckedAt: observationTime(g.now()), Note: "aggregate_probe_timeout"}
				return
			}
			out.Services[i] = g.probeEnvironment(ctx, client, name, base)
		}(i, name, g.Settings.Upstreams[name])
	}
	wg.Wait()
	out.SampledAt = observationTime(g.now())
	return out
}

func (g *Gate) probeEnvironment(ctx context.Context, client *http.Client, name, base string) *v1.EnvironmentService {
	out := &v1.EnvironmentService{Service: name, State: "unknown"}
	defer func() { out.CheckedAt = observationTime(g.now()) }()
	if strings.TrimSpace(base) == "" {
		out.State, out.Note = "unconfigured", "service_upstream_not_configured"
		return out
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		out.State, out.Note = "invalid", "invalid_configured_upstream"
		return out
	}
	u.Path = "/internal/observability"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		out.State, out.Note = "invalid", "invalid_configured_upstream"
		return out
	}
	start := time.Now()
	res, err := client.Do(req)
	ms := float64(time.Since(start)) / float64(time.Millisecond)
	out.ProbeDurationMs = &ms
	if err != nil {
		out.State, out.Note = "offline", "http_probe_failed_or_timed_out"
		return out
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound || res.StatusCode == http.StatusNotImplemented {
		out.State, out.Note = "unsupported", "consumer_observation_endpoint_not_registered"
		return out
	}
	if res.StatusCode != http.StatusOK {
		out.State, out.Note = "unknown", "consumer_observation_http_status_not_ok"
		return out
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, environmentBodyMax+1))
	if err != nil || len(body) > environmentBodyMax {
		out.State, out.Note = "invalid", "observation_body_unreadable_or_over_limit"
		return out
	}
	observation := &platform.ConsumerObservation{}
	if err := protojson.Unmarshal(body, observation); err != nil || observe.ValidateSnapshot(name, observation) != nil {
		out.State, out.Note = "invalid", "observation_contract_invalid_or_unsupported"
		return out
	}
	out.Observation = observation
	sampled, _ := time.Parse(time.RFC3339Nano, observation.SampledAt)
	if age := g.now().Sub(sampled); age < -10*time.Second || age > 10*time.Second {
		out.State, out.Note = "unknown", "service_sample_time_outside_expected_window_clock_skew_unknown"
		return out
	}
	out.State, out.Note = "observed", "declared_process_operations_only_not_business_readiness"
	return out
}
