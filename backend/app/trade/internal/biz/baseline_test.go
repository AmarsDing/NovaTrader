package biz

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"server/pkg/events"
	"server/pkg/tradecal"
)

var baselineTime = time.Date(2026, 10, 15, 10, 30, 0, 0, tradecal.Shanghai())

// TestLiveCashBaselineSeedOnce 钉死高危债修复：LIVE 无基线行时现金闸恒拒买单；
// 一次性人工写入解锁，且再写必须冲突（调基线只能走柜台回报归并+对账链）。
func TestLiveCashBaselineSeedOnce(t *testing.T) {
	e, mem := newEngine(t, allow{})
	e.now = func() time.Time { return baselineTime }
	ctx := context.Background()
	// 持仓已先行同步（柜台回报归并产物）：日初权益按「可用 + 持仓市值」锁定。
	if err := mem.Commit(ctx, Batch{Positions: []Position{{Book: BookLive, Symbol: "600519.SH", Quantity: 100, Available: 100, AvgCost: 1700, LastPrice: 1800}}}); err != nil {
		t.Fatal(err)
	}
	if cash, err := e.cash(ctx, BookLive); err != nil || cash.Cash != 0 {
		t.Fatal("pre-seed LIVE cash must be zero", cash, err)
	}
	view, err := e.SeedLiveCashBaseline(ctx, LiveCashSeed{Available: 320000, Operator: "ada", SourceNote: "东财终端可用资金 2026-10-15 10:25"})
	if err != nil {
		t.Fatal(err)
	}
	if view.Cash != 320000 {
		t.Fatal(view)
	}
	cash, ok, err := mem.LoadCash(ctx, BookLive)
	if err != nil || !ok {
		t.Fatal("baseline row missing", ok, err)
	}
	if cash.DayStartDate != "2026-10-15" || cash.DayStartEquity != 320000+1800*100 {
		t.Fatal("day-start equity drifted", cash)
	}
	seeded := 0
	for _, ev := range mem.Events() {
		if ev.Subject != events.SubjectTradeReconciliation {
			continue
		}
		seeded++
		p, ok := ev.Payload.(map[string]any)
		if !ok || p["kind"] != "cash_baseline_seeded" || p["operator"] != "ada" {
			t.Fatal("audit payload incomplete", ev.Payload)
		}
	}
	if seeded != 1 {
		t.Fatal("reconciliation event missing", mem.Events())
	}
	d := mem.Dispositions()
	if len(d) != 1 || d[0].Action != "cash_baseline_seeded" || !strings.Contains(d[0].Note, "320000.00") || d[0].Actor != "operator/ada" {
		t.Fatal("human-review disposition missing or wrong", d)
	}
	// 一次性：基线已存在后任何再写一律冲突，不接受覆盖。
	if _, err := e.SeedLiveCashBaseline(ctx, LiveCashSeed{Available: 9999999, Operator: "ada", SourceNote: "尝试重写"}); !errors.Is(err, ErrConflict) {
		t.Fatal("reseed must conflict", err)
	}
	if cash, _, _ := mem.LoadCash(ctx, BookLive); cash.Cash != 320000 {
		t.Fatal("conflict reseed overwrote baseline", cash)
	}
}

// TestLiveCashBaselineSeedOverwritesPlaceholderRow 钉死占位行路径：服务起连后回报循环
// 会先于操作员按下 SeedLiveCashBaseline 把 0 基线占位行（DayStartDate=""）落库
// （report.go BaselineMissing 人核口）；占位行不是真基线，种子口必须可一次性纠偏，
// 纠偏后立即回到「再写必冲突」—否则种子口在全部真实部署环境恒不可达（blocker 回归）。
func TestLiveCashBaselineSeedOverwritesPlaceholderRow(t *testing.T) {
	e, mem := newEngine(t, allow{})
	e.now = func() time.Time { return baselineTime }
	ctx := context.Background()
	// 模拟回报循环已跑过：占位行（零值、DayStartDate 恒空）+ 柜台持仓回报已在账内。
	if err := mem.Commit(ctx, Batch{
		Cash:      &Cash{Book: BookLive},
		Positions: []Position{{Book: BookLive, Symbol: "600519.SH", Quantity: 100, Available: 100, AvgCost: 1700, LastPrice: 1800}},
	}); err != nil {
		t.Fatal(err)
	}
	view, err := e.SeedLiveCashBaseline(ctx, LiveCashSeed{Available: 320000, Operator: "ada", SourceNote: "东财终端结单 2026-10-15"})
	if err != nil {
		t.Fatal("placeholder row must not block operator seed", err)
	}
	cash, ok, err := mem.LoadCash(ctx, BookLive)
	if err != nil || !ok {
		t.Fatal("baseline row missing after seed", ok, err)
	}
	if view.Cash != 320000 || cash.Cash != 320000 {
		t.Fatal("placeholder overwrite failed", view, cash)
	}
	if cash.DayStartDate != "2026-10-15" || cash.DayStartEquity != 320000+1800*100 {
		t.Fatal("day-start equity drifted over placeholder", cash)
	}
	// 纠偏后即真基线：再写冲突，覆盖禁绝。
	if _, err := e.SeedLiveCashBaseline(ctx, LiveCashSeed{Available: 1, Operator: "ada", SourceNote: "尝试重写"}); !errors.Is(err, ErrConflict) {
		t.Fatal("reseed after placeholder correction must conflict", err)
	}
}

func TestLiveCashBaselineSeedRejectsFabrication(t *testing.T) {
	e, _ := newEngine(t, allow{})
	ctx := context.Background()
	for _, seed := range []LiveCashSeed{
		{Available: 0, Operator: "ada", SourceNote: "zero"},
		{Available: -1, Operator: "ada", SourceNote: "negative"},
		{Available: 100000, SourceNote: "缺操作员"},
		{Available: 100000, Operator: "ada"},
	} {
		if _, err := e.SeedLiveCashBaseline(ctx, seed); !errors.Is(err, ErrInvalid) {
			t.Fatal(seed, err)
		}
	}
}
