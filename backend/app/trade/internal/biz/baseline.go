package biz

import (
	"context"
	"fmt"
	"math"
	"strings"

	"server/pkg/events"
	"server/pkg/tradecal"
)

// LiveCashSeed 是操作员人工录入的 LIVE 资金基线。金额只能来自柜台实际读数
// （东方财富量化终端当日可用资金/结单），绝不自动写入、绝不虚构。
type LiveCashSeed struct {
	// Available 是柜台实际可用资金（必须 > 0）。
	Available float64
	// Operator 必填：谁录入的，写进审计留痕。
	Operator string
	// SourceNote 必填：来源备注（终端读数/结单口径与时间），供人核回溯。
	SourceNote string
}

// SeedLiveCashBaseline 一次性写入 LIVE 账本资金基线（自身债高危：此前全仓无
// 写入口，cash() 恒返零使 LIVE 开仓买单被现金闸实质锁死）。
//
// 纪律：
//   - 真基线（DayStartDate 非空）已存在时拒绝——其后的资金变动只许走柜台回报归并
//     （report.go 幂等入账）与持仓对账链，不允许静默覆盖基线；占位行（缺基线时回报
//     循环按 0 基线落库、DayStartDate 为空）不算基线，允许本入口一次性纠偏为真基线。
//   - DayStartEquity 按「当日可用资金 + 账内持仓市值」同 startDay 口径锁定；持仓
//     尚未同步到位时等于可用资金本身（保守，不虚构浮盈浮亏）。
//   - 柜台资金快照（cash.csv）仍不改写本地现金（与归并器契约一致）；本入口是
//     人工声明，写入同时留 disposition 人核口与 trade.reconciliation 事件。
func (e *Engine) SeedLiveCashBaseline(ctx context.Context, seed LiveCashSeed) (out AccountView, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	err = e.withLedger(ctx, "global", func(c context.Context) error {
		out, err = e.seedLiveCash(c, seed)
		return err
	})
	return
}

func (e *Engine) seedLiveCash(ctx context.Context, seed LiveCashSeed) (AccountView, error) {
	if seed.Available <= 0 || math.IsNaN(seed.Available) || math.IsInf(seed.Available, 0) {
		return AccountView{}, fmt.Errorf("%w: 可用资金必须大于 0（只能是柜台实际读数，不许虚构）", ErrInvalid)
	}
	operator, note := strings.TrimSpace(seed.Operator), strings.TrimSpace(seed.SourceNote)
	if operator == "" || note == "" {
		return AccountView{}, fmt.Errorf("%w: 资金基线必须录操作员与来源备注（留人核口）", ErrInvalid)
	}
	existing, ok, err := e.store.LoadCash(ctx, BookLive)
	if err != nil {
		return AccountView{}, err
	}
	// 「已存在」只认人工/归并形成过的真基线（DayStartDate 非空）。回报循环在缺基线时
	// 会按 0 基线落占位行（report.go：BaselineMissing 人核口 + batch.Cash 无条件写，
	// DayStartDate 恒空）——占位行不是基线，本入口正是它的纠偏口；若连占位行也拒，
	// 该入口在全部目标环境（服务起连即被回报循环建过行）将恒 ErrConflict 不可达。
	if ok && existing.DayStartDate != "" {
		return AccountView{}, fmt.Errorf("%w: LIVE 资金基线已存在；其后变动走柜台回报归并与对账链，不允许覆盖基线", ErrConflict)
	}
	positions, err := e.store.ListPositions(ctx, BookLive)
	if err != nil {
		return AccountView{}, err
	}
	now := e.clock()
	today := now.In(tradecal.Shanghai()).Format("2006-01-02")
	cash := Cash{Book: BookLive, Cash: seed.Available, DayStartDate: today, DayStartEquity: seed.Available}
	for _, p := range positions {
		if p.Quantity <= 0 {
			continue
		}
		px := p.LastPrice
		if px <= 0 {
			px = p.AvgCost
		}
		cash.DayStartEquity += px * float64(p.Quantity)
	}
	if err := e.store.Commit(ctx, Batch{
		Cash: &cash,
		Events: []Event{{Subject: events.SubjectTradeReconciliation, Payload: map[string]any{
			"kind": "cash_baseline_seeded", "version": 1, "book": BookLive,
			"available": seed.Available, "day_start_date": today, "day_start_equity": cash.DayStartEquity,
			"operator": operator, "source_note": note, "as_of": now,
		}}},
	}); err != nil {
		return AccountView{}, err
	}
	// 留痕跟随账本成功走；存储未接人核口时不阻塞（内存档/极简账本），事件已在账目序列里。
	if audit, ok := e.store.(ReportAuditStore); ok {
		if _, err := audit.RecordDisposition(ctx, Disposition{
			EventID: "cash-baseline:live:" + today,
			TraceID: events.TraceID(ctx),
			Action:  "cash_baseline_seeded",
			Note: fmt.Sprintf("LIVE 资金基线人工写入：可用 %.2f，日初权益 %.2f（来源：%s）",
				seed.Available, cash.DayStartEquity, note),
			Actor: "operator/" + operator,
		}); err != nil {
			return AccountView{}, err
		}
	}
	return e.view(ctx, BookLive)
}
