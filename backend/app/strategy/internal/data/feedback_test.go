package data

import (
	"testing"
	"time"
)

// strategy 过滤词必须真的落进 SQL 谓词，而不只是层间透传：不同 strategy 的反馈
// 混在同一张 strategy_feedback 表里——空串全量、非空精确匹配、未知词空集。
// 钉在真实 PG（NT_TEST_PG_PORT）上，fake 层证明不了谓词存在。
func TestFeedbackRepoStrategyFilter(t *testing.T) {
	d := newTestData(t)
	if _, err := d.client.StrategyFeedback.Delete().Exec(ctx); err != nil {
		t.Fatal(err)
	}
	repo := NewFeedbackRepo(d)
	day := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	mk := func(sym, strat string, ret float64) int {
		sig := d.client.TradeSignal.Create().
			SetSignalTime(day).SetStockCode(sym).SetStockName("x").
			SetSignalType("buy").SetEntryPrice(1).SetStopLoss(1).SetTakeProfit(1).
			SetSignalReasoning("r").SetConfidenceScore(90).SetStrategy(strat).SaveX(ctx)
		d.client.StrategyFeedback.Create().
			SetTradeDate(day).SetStockCode(sym).
			SetSignalID(sig.ID).SetActualReturn(ret).SetPredictedReturn(ret).
			SetSignalCorrect(true).SetStrategy(strat).ExecX(ctx)
		return sig.ID
	}
	mk("600700.SH", "breakout_ref", 0.05)
	idTrend := mk("600701.SH", "trend_follow", -0.02)

	all, err := repo.RecentFeedback(ctx, 20, "")
	if err != nil || len(all) != 2 {
		t.Fatalf("unfiltered RecentFeedback = %d %v", len(all), err)
	}
	bo, err := repo.RecentFeedback(ctx, 20, "breakout_ref")
	if err != nil || len(bo) != 1 || bo[0].Symbol != "600700.SH" || bo[0].Strategy != "breakout_ref" {
		t.Fatalf("filtered RecentFeedback = %+v %v", bo, err)
	}
	miss, err := repo.RecentFeedback(ctx, 20, "no_such_strategy")
	if err != nil || len(miss) != 0 {
		t.Fatalf("unknown strategy must be empty: %d %v", len(miss), err)
	}
	pts, err := repo.ScoreVsPnl(ctx, 50, "trend_follow")
	if err != nil || len(pts) != 1 || pts[0].SignalID != idTrend || pts[0].Strategy != "trend_follow" {
		t.Fatalf("filtered ScoreVsPnl = %+v %v", pts, err)
	}
	if pts[0].Score != 90 {
		t.Fatalf("score join = %v", pts[0].Score)
	}
}

// 「先过滤后截断」是行为承诺而非结构巧合：谓词若被误改为「先取最近 N 行全量再过滤」，
// 或下沉为升序 SQL LIMIT，旧钉子（limit ≥ 行数）照样全绿。隔日三行 A(bo)–B(xx)–C(bo)
// 让两种错序都暴露出来。
func TestFeedbackRepoFilterBeforeLimit(t *testing.T) {
	d := newTestData(t)
	if _, err := d.client.StrategyFeedback.Delete().Exec(ctx); err != nil {
		t.Fatal(err)
	}
	repo := NewFeedbackRepo(d)
	dayOf := func(n int) time.Time { return time.Date(2026, 10, n, 0, 0, 0, 0, time.UTC) }
	seed := func(sym, strat string, day time.Time, ret float64) int {
		sig := d.client.TradeSignal.Create().
			SetSignalTime(day).SetStockCode(sym).SetStockName("x").
			SetSignalType("buy").SetEntryPrice(1).SetStopLoss(1).SetTakeProfit(1).
			SetSignalReasoning("r").SetConfidenceScore(80).SetStrategy(strat).SaveX(ctx)
		d.client.StrategyFeedback.Create().
			SetTradeDate(day).SetStockCode(sym).
			SetSignalID(sig.ID).SetActualReturn(ret).SetPredictedReturn(ret).
			SetSignalCorrect(true).SetStrategy(strat).ExecX(ctx)
		return sig.ID
	}
	seed("600710.SH", "breakout_ref", dayOf(13), 0.01)
	seed("600711.SH", "trend_follow", dayOf(14), 0.02)
	idC := seed("600712.SH", "breakout_ref", dayOf(15), 0.03)

	// 过滤后的「最近 1 笔」必须是 C：先截后滤会把 B 带来再被滤掉得空集，升序误截得 A
	latest, err := repo.RecentFeedback(ctx, 1, "breakout_ref")
	if err != nil || len(latest) != 1 || latest[0].Symbol != "600712.SH" {
		t.Fatalf("filtered latest = %+v %v, want C only", latest, err)
	}
	// 升序合同一并钉住：两行 bo 必须早→晚
	two, err := repo.RecentFeedback(ctx, 10, "breakout_ref")
	if err != nil || len(two) != 2 || two[0].Symbol != "600710.SH" || two[1].Symbol != "600712.SH" {
		t.Fatalf("ascending order = %+v %v", two, err)
	}
	// 散点路径（倒序截断）同样先过滤：limit=1 只能剩 C
	pts, err := repo.ScoreVsPnl(ctx, 1, "breakout_ref")
	if err != nil || len(pts) != 1 || pts[0].SignalID != idC {
		t.Fatalf("filtered scatter = %+v %v, want C only", pts, err)
	}
}
