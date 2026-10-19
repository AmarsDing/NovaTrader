package biz

import (
	"context"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/log"
)

type fakeFeedback struct {
	rows     []FeedbackRow
	points   []ScorePnlPoint
	strategy []string // 逐次调用收到的 strategy 过滤词（钉透传契约）
}

func (f *fakeFeedback) RecentFeedback(_ context.Context, _ int, strategy string) ([]FeedbackRow, error) {
	f.strategy = append(f.strategy, strategy)
	return f.rows, nil
}
func (f *fakeFeedback) ScoreVsPnl(_ context.Context, _ int, strategy string) ([]ScorePnlPoint, error) {
	f.strategy = append(f.strategy, strategy)
	return f.points, nil
}

func day(s string) time.Time {
	t, _ := time.Parse("2006-01-02", s)
	return t
}

func TestPerformance(t *testing.T) {
	yes, no := true, false
	uc := NewUsecase(NewSettings(nil), nil, nil, nil, nil, nil, nil, nil, &fakeFeedback{rows: []FeedbackRow{
		{TradeDate: day("2026-10-09"), Symbol: "A", ActualReturn: 0.05, SignalCorrect: &yes, Attribution: ""},
		{TradeDate: day("2026-10-10"), Symbol: "B", ActualReturn: -0.02, SignalCorrect: &no, Attribution: "indicator_failed"},
		{TradeDate: day("2026-10-12"), Symbol: "C", ActualReturn: 0.01, SignalCorrect: &yes, Attribution: "unfilled"},
	}}, log.DefaultLogger)

	p, err := uc.Performance(context.Background(), 20, "")
	if err != nil {
		t.Fatal(err)
	}
	if p.Sample != 3 {
		t.Fatalf("sample = %d, want 3", p.Sample)
	}
	if p.WinRate < 0.66 || p.WinRate > 0.67 {
		t.Fatalf("win_rate = %v, want 2/3", p.WinRate)
	}
	// 平均盈利 (0.05+0.01)/2 = 0.03，平均亏损 0.02 → 盈亏比 1.5
	if p.PLRatio < 1.49 || p.PLRatio > 1.51 {
		t.Fatalf("pl_ratio = %v, want 1.5", p.PLRatio)
	}
	// 累计曲线 +0.05 → +0.03 → +0.04，峰 0.05 谷 0.03，回撤 0.02
	if p.MaxDrawdown < 0.019 || p.MaxDrawdown > 0.021 {
		t.Fatalf("max_drawdown = %v, want 0.02", p.MaxDrawdown)
	}
	if p.Attributions["indicator_failed"] != 1 || p.Attributions["unfilled"] != 1 {
		t.Fatalf("attributions = %v", p.Attributions)
	}
}

func TestPerformanceEmpty(t *testing.T) {
	uc := NewUsecase(NewSettings(nil), nil, nil, nil, nil, nil, nil, nil, &fakeFeedback{}, log.DefaultLogger)
	p, err := uc.Performance(context.Background(), 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if p.Sample != 0 || p.WinRate != 0 || p.PLRatio != 0 {
		t.Fatalf("empty sample should be all zero: %+v", p)
	}
}

func TestScoreVsPnlPearson(t *testing.T) {
	uc := NewUsecase(NewSettings(nil), nil, nil, nil, nil, nil, nil, nil, &fakeFeedback{points: []ScorePnlPoint{
		{SignalID: 1, Score: 90, ActualReturn: 0.09},
		{SignalID: 2, Score: 80, ActualReturn: 0.07},
		{SignalID: 3, Score: 70, ActualReturn: 0.04},
		{SignalID: 4, Score: 60, ActualReturn: 0.02},
	}}, log.DefaultLogger)

	points, r, err := uc.ScoreVsPnl(context.Background(), 50, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 4 {
		t.Fatalf("points = %d, want 4", len(points))
	}
	// 完全正相关 → ≈1（线性：ret = score/1000 - 0.04 附近）
	if r < 0.99 {
		t.Fatalf("pearson = %v, want ≈1", r)
	}
}

func TestPearsonInsufficient(t *testing.T) {
	if got := pearson(nil); got != 0 {
		t.Fatalf("nil sample pearson = %v, want 0", got)
	}
	// 方差为 0（全部同分）也必须返回 0，不做除零
	if got := pearson([]ScorePnlPoint{{Score: 80, ActualReturn: 0.01}, {Score: 80, ActualReturn: 0.02}}); got != 0 {
		t.Fatalf("zero-variance pearson = %v, want 0", got)
	}
}

// strategy 过滤词必须原样透传到 repo（空串 = 不过滤同样逐字透传），两层之间不许改写。
func TestStrategyFilterForward(t *testing.T) {
	fb := &fakeFeedback{}
	uc := NewUsecase(NewSettings(nil), nil, nil, nil, nil, nil, nil, nil, fb, log.DefaultLogger)
	if _, err := uc.Performance(context.Background(), 20, "breakout_ref"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := uc.ScoreVsPnl(context.Background(), 50, ""); err != nil {
		t.Fatal(err)
	}
	if len(fb.strategy) != 2 || fb.strategy[0] != "breakout_ref" || fb.strategy[1] != "" {
		t.Fatalf("strategy forward = %v, want [breakout_ref \"\"]", fb.strategy)
	}
}
