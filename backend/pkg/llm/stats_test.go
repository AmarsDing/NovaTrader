package llm

import (
	"context"
	"testing"
	"time"
)

// FR-05-12 统计口径的包级钉死。llm_test.go 已钉 parseMetrics 标签聚合与
// Gateway.Stats 的熔断/vLLM 字段/未配置回退；本文件钉 tierStats 本体：
// 200 条延迟环形窗、失败计数但不进窗、百分位取值口径与 μs→ms 换算、
// Stats 对计数/百分位的汇聚以及抓取失败的三字段 -1 回退。

func TestTierStatsRingEvictsOldest(t *testing.T) {
	// 窗口 200 条（stats.go :15），装满后最旧的一条被挤出（stats.go :41-42）。
	s := newTierStats()
	for i := 1; i <= 200; i++ {
		s.observe(time.Duration(i)*time.Millisecond, true)
	}
	calls, failures, p50, p95 := s.snapshot()
	if calls != 200 || failures != 0 {
		t.Fatalf("counts = %d %d", calls, failures)
	}
	// pick：i = int(q*(n-1)+0.5)（stats.go :54-57）。n=200 有序 1..200ms：
	// p50 → sorted[100] = 101ms；p95 → int(189.05+0.5)=189 → sorted[189] = 190ms。
	if p50 != 101 || p95 != 190 {
		t.Fatalf("percentiles = %v %v", p50, p95)
	}
	// 第 201 条 999ms 覆盖最旧的 1ms：窗内变成 2..200 + 999。
	s.observe(999*time.Millisecond, true)
	calls, failures, p50, p95 = s.snapshot()
	if calls != 201 || failures != 0 {
		t.Fatalf("counts after wrap = %d %d", calls, failures)
	}
	// 有序 [2,3,…,200,999]：p50 → sorted[100] = 102ms；p95 → sorted[189] = 191ms。
	if p50 != 102 || p95 != 191 {
		t.Fatalf("percentiles after wrap = %v %v（最旧条未被挤出）", p50, p95)
	}
}

func TestTierStatsFailuresCountedButNotInWindow(t *testing.T) {
	// 失败计数进 calls/failures，但它的延迟不进窗（stats.go :33-36 提前返回）——
	// 失败样本若进窗，尾部延迟会把 p95 顶上天。
	s := newTierStats()
	s.observe(10*time.Millisecond, true)
	s.observe(20*time.Millisecond, true)
	s.observe(30*time.Millisecond, true)
	s.observe(9*time.Second, false)
	s.observe(9*time.Second, false)
	calls, failures, p50, p95 := s.snapshot()
	if calls != 5 || failures != 2 {
		t.Fatalf("counts = %d %d", calls, failures)
	}
	// n=3：p50 → int(0.5*2+0.5)=1 → 20ms；p95 → int(0.95*2+0.5)=2 → 30ms。
	// 两个 9s 失败样本必须不影响百分位。
	if p50 != 20 || p95 != 30 {
		t.Fatalf("percentiles = %v %v（失败样本混入窗口）", p50, p95)
	}
}

func TestTierStatsEmptyAndMilliseconds(t *testing.T) {
	// 空窗：计数照给，百分位为 0（stats.go :50-51），不抛下标越界。
	s := newTierStats()
	if calls, failures, p50, p95 := s.snapshot(); calls != 0 || failures != 0 || p50 != 0 || p95 != 0 {
		t.Fatalf("empty snapshot = %d %d %v %v", calls, failures, p50, p95)
	}
	// μs→ms 保留小数（stats.go :56）：1500μs = 1.5ms，单样本 p50==p95。
	s.observe(1500*time.Microsecond, true)
	if _, _, p50, p95 := s.snapshot(); p50 != 1.5 || p95 != 1.5 {
		t.Fatalf("fractional ms = %v %v", p50, p95)
	}
}

func TestGatewayStatsCarriesCallsAndPercentiles(t *testing.T) {
	// Gateway.Stats 只转交 snapshot 的四元组（stats.go :89），不改写口径。
	srv := fakeVLLM(t, func(chatBody) (int, string) { return 200, "{}" })
	g := New(map[Tier]ModelConfig{Small: {BaseURL: srv.URL + "/v1", Model: "qwen"}}, nil)
	tr := g.tiers[Small]
	tr.stats.observe(100*time.Millisecond, true)
	tr.stats.observe(300*time.Millisecond, true)
	tr.stats.observe(time.Second, false)
	st := g.Stats(context.Background())
	if len(st) != 1 {
		t.Fatalf("stats = %+v", st)
	}
	// n=2 有效样本 {100,300}：p50 → int(0.5+0.5)=1 → 300；p95 → int(1.45)=1 → 300。
	row := st[0]
	if row.Calls != 3 || row.Failures != 1 || row.P50MS != 300 || row.P95MS != 300 {
		t.Fatalf("gateway stats row = %+v", row)
	}
	if row.InFlight != 0 || row.Queued != 0 || row.CircuitOpen {
		t.Fatalf("idle queue/breaker defaults = %+v", row)
	}
	// 排队数/并发数与各模型取值（设计 :33 统计行承诺「排队数、并发数」、:21「各模型」）：
	// 空载 0/0 双零掩盖接反——占一格队列后必须如实上报 InFlight=1，Model 逐字取自配置。
	if err := tr.queue.acquire(context.Background(), PositionRisk); err != nil {
		t.Fatal(err)
	}
	if st := g.Stats(context.Background()); st[0].InFlight != 1 || st[0].Queued != 0 || st[0].Model != "qwen" {
		t.Fatalf("busy stats = %+v, want InFlight 1 Model qwen", st[0])
	}
	tr.queue.release()
	if st := g.Stats(context.Background()); st[0].InFlight != 0 {
		t.Fatalf("released stats = %+v", st[0])
	}
}

func TestGatewayStatsScrapeFailureKeepsMinusOne(t *testing.T) {
	// 配置了 BaseURL 但 vLLM 抓不到：三个指标字段保持 -1（stats.go :88 初始化、
	// :92-97 仅成功才覆盖），Configured 仍为真——区分「未配置」与「失联」。
	srv := fakeVLLM(t, func(chatBody) (int, string) { return 200, "{}" })
	srv.Close() // 立刻失联
	g := New(map[Tier]ModelConfig{Small: {BaseURL: srv.URL + "/v1", Model: "qwen"}}, nil)
	st := g.Stats(context.Background())
	if len(st) != 1 {
		t.Fatalf("stats = %+v", st)
	}
	if !st[0].Configured || st[0].KVCacheUsage != -1 || st[0].Running != -1 || st[0].Waiting != -1 {
		t.Fatalf("scrape failure must keep -1s: %+v", st[0])
	}
	if st[0].Calls != 0 || st[0].P50MS != 0 || st[0].P95MS != 0 {
		t.Fatalf("no calls yet: %+v", st[0])
	}
}
