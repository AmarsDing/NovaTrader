package llm

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const latencyWindow = 200

type tierStats struct {
	mu       sync.Mutex
	calls    int64
	failures int64
	window   []time.Duration
	next     int
}

func newTierStats() *tierStats {
	return &tierStats{window: make([]time.Duration, 0, latencyWindow)}
}

func (s *tierStats) observe(d time.Duration, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if !ok {
		s.failures++
		return
	}
	if len(s.window) < latencyWindow {
		s.window = append(s.window, d)
		return
	}
	s.window[s.next] = d
	s.next = (s.next + 1) % latencyWindow
}

func (s *tierStats) snapshot() (calls, failures int64, p50, p95 float64) {
	s.mu.Lock()
	sorted := append([]time.Duration(nil), s.window...)
	calls, failures = s.calls, s.failures
	s.mu.Unlock()
	if len(sorted) == 0 {
		return calls, failures, 0, 0
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	pick := func(q float64) float64 {
		i := int(q*float64(len(sorted)-1) + 0.5)
		return float64(sorted[i].Microseconds()) / 1000
	}
	return calls, failures, pick(0.50), pick(0.95)
}

// TierStats 是某档模型的运行情况。vLLM 指标读不到时 KVCacheUsage、Running、Waiting 为 -1。
type TierStats struct {
	Tier         Tier
	Model        string
	Configured   bool
	Calls        int64
	Failures     int64
	P50MS        float64
	P95MS        float64
	InFlight     int
	Queued       int
	CircuitOpen  bool
	KVCacheUsage float64
	Running      float64
	Waiting      float64
}

// Stats 汇总各档统计，并顺带读取 vLLM 的 /metrics（FR-05-12）。
func (g *Gateway) Stats(ctx context.Context) []TierStats {
	names := make([]string, 0, len(g.tiers))
	for name := range g.tiers {
		names = append(names, string(name))
	}
	sort.Strings(names)
	out := make([]TierStats, 0, len(names))
	for _, name := range names {
		tr := g.tiers[Tier(name)]
		st := TierStats{Tier: Tier(name), Model: tr.cfg.Model, Configured: tr.cfg.BaseURL != "", KVCacheUsage: -1, Running: -1, Waiting: -1}
		st.Calls, st.Failures, st.P50MS, st.P95MS = tr.stats.snapshot()
		st.InFlight, st.Queued = tr.queue.load()
		st.CircuitOpen = tr.breakerOpen(g.now())
		if st.Configured {
			if m, err := g.scrape(ctx, tr.cfg); err == nil {
				st.KVCacheUsage = pickMetric(m, -1, "vllm:gpu_cache_usage_perc", "vllm:kv_cache_usage_perc")
				st.Running = pickMetric(m, -1, "vllm:num_requests_running")
				st.Waiting = pickMetric(m, -1, "vllm:num_requests_waiting")
			}
		}
		out = append(out, st)
	}
	return out
}

func (g *Gateway) scrape(ctx context.Context, cfg ModelConfig) (map[string]float64, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	base := strings.TrimSuffix(cfg.BaseURL, "/v1")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/metrics", nil)
	if err != nil {
		return nil, err
	}
	resp, err := g.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return parseMetrics(io.LimitReader(resp.Body, 8<<20)), nil
}

// parseMetrics 读 Prometheus 文本。同名不同标签的样本相加（vLLM 按模型名打标签）。
func parseMetrics(r io.Reader) map[string]float64 {
	out := map[string]float64{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := fields[0]
		if i := strings.IndexByte(name, '{'); i >= 0 {
			name = name[:i]
			if j := strings.LastIndexByte(line, '}'); j >= 0 {
				fields = strings.Fields(line[j+1:])
			}
		}
		if len(fields) == 0 {
			continue
		}
		v, err := strconv.ParseFloat(fields[0], 64)
		if err != nil {
			continue
		}
		out[name] += v
	}
	return out
}

func pickMetric(m map[string]float64, def float64, names ...string) float64 {
	for _, n := range names {
		if v, ok := m[n]; ok {
			return v
		}
	}
	return def
}
