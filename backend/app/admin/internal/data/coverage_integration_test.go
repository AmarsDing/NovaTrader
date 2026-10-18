package data

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"server/app/admin/internal/gateway"
)

// 因果覆盖口径持久往返（隔离PG，独立库名避开其他 outbox 空断言测试）：
// 根/续链/无链/孤儿父/畸形行逐一入形状，窗口顺序与上限如实生效。
func TestTraceCoverageOnIsolatedPostgres(t *testing.T) {
	client := adminContractClient(t, "novatrader_admin_coverage_test")
	s := &AuditStore{client: client}
	ctx := context.Background()
	tag := uuid.NewString()[:8]

	base := time.Now().UTC().Add(-time.Minute)
	insert := func(i int, source, trace string, metadata map[string]any) string {
		eventID := "evt-cov-" + tag + "-" + source[0:1] + string(rune('a'+i))
		q := client.Outbox.Create().
			SetEventID(eventID).
			SetTraceID(trace).
			SetSubject("novatrader.test.coverage").
			SetSource(source).
			SetPayload([]byte(`{}`)).
			SetCreatedAt(base.Add(time.Duration(i) * time.Second))
		if metadata != nil {
			q = q.SetMetadata(metadata)
		}
		if _, err := q.Save(ctx); err != nil {
			t.Fatal(err)
		}
		return eventID
	}

	// 0: 根事件（有链号、无因果父）
	rootID := insert(0, "strategy", "tr-"+tag, nil)
	// 1: 续链事件，父在同一窗口内
	insert(1, "trade", "tr-"+tag, map[string]any{"causation_id": rootID})
	// 2: 续链事件，父不在窗口持久历史中（如内存域 market.* 父）→ 孤儿父如实计数
	insert(2, "trade", "tr-"+tag, map[string]any{"causation_id": "not-present-" + tag})
	// 3: 无链事件
	insert(3, "notify", "", nil)
	// 4: 畸形行——有因果父却丢了链号：同时计入 children 与 without_trace，不掩饰
	insert(4, "strategy", "", map[string]any{"causation_id": rootID})

	coverage, err := s.ReadTraceCoverage(ctx, 2000)
	if err != nil {
		t.Fatal(err)
	}
	if !coverage.Available || coverage.Scope != "durable_outbox_history" || coverage.AsOf == "" {
		t.Fatalf("header wrong: %+v", coverage)
	}
	if coverage.WindowRows != 5 || coverage.OldestInWindow == "" {
		t.Fatalf("window wrong: rows=%d oldest=%q", coverage.WindowRows, coverage.OldestInWindow)
	}
	wantTotals := gateway.SourceCoverage{Total: 5, WithTrace: 3, WithoutTrace: 2, Children: 3, OrphanParents: 1}
	if coverage.Totals != wantTotals {
		t.Fatalf("totals = %+v want %+v", coverage.Totals, wantTotals)
	}
	// by_source 按 source 名稳定排序，分组计数与逐行一致
	if len(coverage.BySource) != 3 ||
		coverage.BySource[0].Source != "notify" ||
		coverage.BySource[1].Source != "strategy" ||
		coverage.BySource[2].Source != "trade" {
		t.Fatalf("by_source order/grouping: %+v", coverage.BySource)
	}
	trade := coverage.BySource[2]
	if trade.Total != 2 || trade.WithTrace != 2 || trade.Children != 2 || trade.OrphanParents != 1 || trade.WithoutTrace != 0 {
		t.Fatalf("trade bucket: %+v", trade)
	}
	strategy := coverage.BySource[1]
	if strategy.Total != 2 || strategy.WithTrace != 1 || strategy.WithoutTrace != 1 || strategy.Children != 1 || strategy.OrphanParents != 0 {
		t.Fatalf("strategy bucket: %+v", strategy)
	}
	// 孤儿父计数：窗口内有父的续链不算孤儿
	if coverage.Totals.Children-coverage.Totals.OrphanParents != 2 {
		t.Fatalf("stitched-in-window count wrong: %+v", coverage.Totals)
	}

	// 窗口上限如实生效：limit=2 只统计最新两行（trade 孤儿父 + trade 窗口内续链）
	narrow, err := s.ReadTraceCoverage(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if narrow.WindowRows != 2 || narrow.Totals.Total != 2 {
		t.Fatalf("narrow window: %+v", narrow.Totals)
	}
	// 最新两行的因果父都在窗口外（rootID 不在最新两行里）→ 两行都是孤儿父
	if narrow.Totals.Children != 1 || narrow.Totals.OrphanParents != 1 {
		t.Fatalf("narrow orphans: %+v", narrow.Totals)
	}

	// 空库口径：零行也是真实应答，不是绿色补齐
	emptyStore := &AuditStore{client: adminContractClient(t, "novatrader_admin_coverage_empty")}
	empty, err := emptyStore.ReadTraceCoverage(ctx, 2000)
	if err != nil {
		t.Fatal(err)
	}
	if empty.Totals != (gateway.SourceCoverage{}) || len(empty.BySource) != 0 || empty.OldestInWindow != "" {
		t.Fatalf("empty db coverage: %+v", empty)
	}
}
