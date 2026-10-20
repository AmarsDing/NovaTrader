package data

// 本文件是 M06 闭仓归属（done→closed）的只读投影仓储：候选信号 keyset 分页 +
// (book, symbol) 全序账本重放事实。只新增读方法；closed 写入零新增路径，
// 唯一写径仍是 signalRepo.Transition 的事务三件套（合同 §1.5 R2）。

import (
	"context"
	"fmt"

	"server/app/strategy/internal/biz"
	"server/ent/tradefill"
	"server/ent/tradesignal"
)

// CloseCandidates 谓语写死（合同 §1.1 候选信号）：status=done ∧ side=buy ∧
// executed_volume 非空且 > 0 ∧ book=参数，keyset IDGT 升序分页。
func (r *signalRepo) CloseCandidates(ctx context.Context, book string, afterID, limit int) ([]int, error) {
	return r.d.client.TradeSignal.Query().Where(
		tradesignal.Book(book),
		tradesignal.SignalTypeEQ(tradesignal.SignalTypeBuy),
		tradesignal.Status(string(biz.StatusDone)),
		tradesignal.ExecutedVolumeNotNil(),
		tradesignal.ExecutedVolumeGT(0),
		tradesignal.IDGT(afterID),
	).Order(tradesignal.ByID()).Limit(limit).IDs(ctx)
}

// ClosurePairFacts 读取一个 pair 的重放事实：trade_fill 按 id 升序全序
// （LIMIT 20001，超限显式报错不截断，与 executionFacts 同纪律）、该 pair
// 全部候选形态信号行、每候选的未决执行意图（复用 unresolvedSignalIntent）。
func (r *signalRepo) ClosurePairFacts(ctx context.Context, book, symbol string) (*biz.ClosureFacts, error) {
	facts := &biz.ClosureFacts{PendingIntent: map[int]bool{}}
	fills, err := r.d.client.TradeFill.Query().Where(
		tradefill.Book(book), tradefill.Symbol(symbol),
	).Order(tradefill.ByID()).Limit(20001).All(ctx)
	if err != nil {
		return nil, err
	}
	if len(fills) > 20000 {
		// 读取超限截断是合同 §1.3 枚举的库存恒等门触发项，显式标记供 core 分流。
		return nil, fmt.Errorf("strategy: closure pair fills exceed safe bound: %w", biz.ErrClosureIdentityGate)
	}
	for _, row := range fills {
		facts.Fills = append(facts.Fills, biz.FillFact{
			ID: row.ID, SignalID: row.SignalID, Side: row.Side, Qty: row.Qty,
			Price: row.Price, Amount: row.Amount, Fee: row.Fee, CreatedAt: row.CreatedAt,
		})
	}
	rows, err := r.d.client.TradeSignal.Query().Where(
		tradesignal.Book(book),
		tradesignal.StockCode(symbol),
		tradesignal.SignalTypeEQ(tradesignal.SignalTypeBuy),
		tradesignal.Status(string(biz.StatusDone)),
		tradesignal.ExecutedVolumeNotNil(),
		tradesignal.ExecutedVolumeGT(0),
	).Order(tradesignal.ByID()).Limit(1001).All(ctx)
	if err != nil {
		return nil, err
	}
	if len(rows) > 1000 {
		// 同属「读取超限截断」：宁 loud 不截断，恒等门分流与 fills 超限同款。
		return nil, fmt.Errorf("strategy: closure pair signals exceed safe bound: %w", biz.ErrClosureIdentityGate)
	}
	for _, row := range rows {
		s := toSignal(row)
		facts.Signals = append(facts.Signals, *s)
		pending, err := unresolvedSignalIntent(ctx, r.d.client, s)
		if err != nil {
			return nil, err
		}
		facts.PendingIntent[s.ID] = pending
	}
	return facts, nil
}

var _ biz.ClosureProjectionRepo = (*signalRepo)(nil)
