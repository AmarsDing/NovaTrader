package biz

import (
	"context"
	"math"
	"sort"
	"time"
)

// FeedbackRow 是一条已出后验的信号反馈（监控驾驶舱 P1 只读聚合用）。
type FeedbackRow struct {
	TradeDate     time.Time
	Symbol        string
	SignalID      *int
	ActualReturn  float64 // 小数
	SignalCorrect *bool
	Attribution   string
	Strategy      string
}

// ScorePnlPoint 是评分-收益散点的一点（feedback × trade_signals）。
type ScorePnlPoint struct {
	SignalID     int
	Symbol       string
	Score        float64 // confidence_score，0-100
	ActualReturn float64
	TradeDate    time.Time
	Strategy     string
}

// FeedbackRepo 读 strategy_feedback 与 trade_signals 的后验数据（只读）。
type FeedbackRepo interface {
	// RecentFeedback 按交易日倒序返回最近 limit 条 signal_correct 非空的反馈。
	RecentFeedback(ctx context.Context, limit int) ([]FeedbackRow, error)
	// ScoreVsPnl 返回最近 limit 条挂得上信号的反馈的评分-收益对照。
	ScoreVsPnl(ctx context.Context, limit int) ([]ScorePnlPoint, error)
}

// Performance 是近 N 笔后验的绩效汇总。Sample 为 0 表示还没有后验样本。
type Performance struct {
	Sample       int
	WinRate      float64
	PLRatio      float64
	MaxDrawdown  float64
	AvgReturn    float64
	Attributions map[string]int
}

// Performance 汇总近 last 笔后验反馈（默认 20，上限 200）。
func (uc *Usecase) Performance(ctx context.Context, last int) (*Performance, error) {
	if uc.feedback == nil {
		return nil, ErrNotFound
	}
	if last <= 0 {
		last = 20
	}
	if last > 200 {
		last = 200
	}
	rows, err := uc.feedback.RecentFeedback(ctx, last)
	if err != nil {
		return nil, err
	}
	p := &Performance{Attributions: map[string]int{}}
	if len(rows) == 0 {
		return p, nil
	}
	// 交易日升序累计，回撤才是时间序的峰谷差
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].TradeDate.Equal(rows[j].TradeDate) {
			return rows[i].Symbol < rows[j].Symbol
		}
		return rows[i].TradeDate.Before(rows[j].TradeDate)
	})
	var wins, losses float64
	var winCount, lossCount int
	var equity, peak float64
	for _, r := range rows {
		p.Sample++
		p.AvgReturn += r.ActualReturn
		if r.SignalCorrect != nil && *r.SignalCorrect {
			winCount++
			wins += r.ActualReturn
		} else {
			lossCount++
			losses += r.ActualReturn
		}
		if r.Attribution != "" {
			p.Attributions[r.Attribution]++
		}
		equity += r.ActualReturn
		if equity > peak {
			peak = equity
		}
		if dd := peak - equity; dd > p.MaxDrawdown {
			p.MaxDrawdown = dd
		}
	}
	p.AvgReturn /= float64(p.Sample)
	p.WinRate = float64(winCount) / float64(p.Sample)
	// 盈亏比：平均盈利 / 平均亏损绝对值；任一为空时返回 0（前端写「样本不足」而不是编数）
	if winCount > 0 && lossCount > 0 && losses < 0 {
		p.PLRatio = (wins / float64(winCount)) / math.Abs(losses/float64(lossCount))
	}
	return p, nil
}

// ScoreVsPnl 返回评分-收益散点与 Pearson 相关系数（默认 50，上限 500）。
// 样本 <2 或任一维方差为 0 时 Pearson 为 0，由前端写「样本不足」。
func (uc *Usecase) ScoreVsPnl(ctx context.Context, last int) ([]ScorePnlPoint, float64, error) {
	if uc.feedback == nil {
		return nil, 0, ErrNotFound
	}
	if last <= 0 {
		last = 50
	}
	if last > 500 {
		last = 500
	}
	points, err := uc.feedback.ScoreVsPnl(ctx, last)
	if err != nil {
		return nil, 0, err
	}
	return points, pearson(points), nil
}

func pearson(points []ScorePnlPoint) float64 {
	n := float64(len(points))
	if n < 2 {
		return 0
	}
	var sx, sy, sxx, syy, sxy float64
	for _, p := range points {
		sx += p.Score
		sy += p.ActualReturn
		sxx += p.Score * p.Score
		syy += p.ActualReturn * p.ActualReturn
		sxy += p.Score * p.ActualReturn
	}
	num := n*sxy - sx*sy
	den := math.Sqrt((n*sxx - sx*sx) * (n*syy - sy*sy))
	if den == 0 {
		return 0
	}
	return num / den
}
