package biz

import (
	"context"
	"time"
)

// FinanceBriefRow 是 finance_report 的一行（附录 B-3 财务排雷摘要）。
// 披露列 NetProfit/NetProfitYoy 保持指针：DB NULL ↔ 缺省，缺披露不落 0。
type FinanceBriefRow struct {
	EndDate      time.Time
	Kind         string // forecast / express / indicator
	AnnDate      time.Time
	ForecastType string
	NetProfit    *float64
	NetProfitYoY *float64
	Summary      string
	Source       string
}

// maxFinancePeriods 是 GetFinanceBrief 的报告期窗口上限。
const maxFinancePeriods = 12

// FinanceBrief 读财务排雷摘要：periods ≤0 取 4，>12 截 12；同报告期多公告
// （修正公告/多种 kind）按公告日倒序全列。rows 为空即 found=false——无披露行
// 是合法业务态，前端显「无」不告警。
func (u *Usecase) FinanceBrief(ctx context.Context, symbol string, periods int) ([]FinanceBriefRow, error) {
	if periods <= 0 {
		periods = 4
	}
	if periods > maxFinancePeriods {
		periods = maxFinancePeriods
	}
	return u.repo.FinanceBriefRows(ctx, symbol, periods)
}
