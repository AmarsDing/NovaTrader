package data

import (
	"context"
	"sort"

	"server/app/strategy/internal/biz"
	"server/ent/strategyfeedback"
	"server/ent/tradesignal"
)

// feedbackRepo 只读 strategy_feedback（× trade_signals），给监控驾驶舱 P1 的绩效与散点。
// 写方是 backtest 的 SaveFeedback；这里不改任何行。
type feedbackRepo struct{ d *Data }

func NewFeedbackRepo(d *Data) biz.FeedbackRepo { return &feedbackRepo{d: d} }

func (r *feedbackRepo) RecentFeedback(ctx context.Context, limit int, strategy string) ([]biz.FeedbackRow, error) {
	q := r.d.client.StrategyFeedback.Query().
		Where(strategyfeedback.SignalCorrectNotNil())
	if strategy != "" {
		q = q.Where(strategyfeedback.StrategyEQ(strategy))
	}
	rows, err := q.
		Order(strategyfeedback.ByTradeDate(), strategyfeedback.ByID()).
		// 要的是最近 N 笔：ent 的 Order 升序后从尾部取，保持简单不引入 desc 修饰
		All(ctx)
	if err != nil {
		return nil, err
	}
	if len(rows) > limit {
		rows = rows[len(rows)-limit:]
	}
	out := make([]biz.FeedbackRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, biz.FeedbackRow{
			TradeDate:     row.TradeDate,
			Symbol:        row.StockCode,
			SignalID:      row.SignalID,
			ActualReturn:  row.ActualReturn,
			SignalCorrect: row.SignalCorrect,
			Attribution:   row.Attribution,
			Strategy:      row.Strategy,
		})
	}
	return out, nil
}

func (r *feedbackRepo) ScoreVsPnl(ctx context.Context, limit int, strategy string) ([]biz.ScorePnlPoint, error) {
	q := r.d.client.StrategyFeedback.Query().
		Where(strategyfeedback.SignalIDNotNil(), strategyfeedback.SignalCorrectNotNil())
	if strategy != "" {
		q = q.Where(strategyfeedback.StrategyEQ(strategy))
	}
	rows, err := q.All(ctx)
	if err != nil {
		return nil, err
	}
	// 按交易日倒序取最近 limit 条，再逐条取信号的综合分
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].TradeDate.Equal(rows[j].TradeDate) {
			return rows[i].ID > rows[j].ID
		}
		return rows[i].TradeDate.After(rows[j].TradeDate)
	})
	if len(rows) > limit {
		rows = rows[:limit]
	}
	points := make([]biz.ScorePnlPoint, 0, len(rows))
	for _, row := range rows {
		sig, err := r.d.client.TradeSignal.Query().
			Where(tradesignal.ID(*row.SignalID)).
			Select(tradesignal.FieldConfidenceScore).
			Only(ctx)
		if err != nil {
			// 信号被删或库不一致时跳过这一笔，不让整份散点失败
			continue
		}
		points = append(points, biz.ScorePnlPoint{
			SignalID:     *row.SignalID,
			Symbol:       row.StockCode,
			Score:        sig.ConfidenceScore,
			ActualReturn: row.ActualReturn,
			TradeDate:    row.TradeDate,
			Strategy:     row.Strategy,
		})
	}
	return points, nil
}
