package biz

import (
	"context"
	"time"
)

/* ---------- 券商研报盈利预测读出口（目录册增补 B4） ---------- */

const (
	estimateDefaultDays = 90
	estimateMaxDays     = 400
)

// ReportEstimateItem 一份研报×一个预测年度的披露行（PIT 明细）。
// 披露列缺披露保持 nil——「没取到≠0」，消费侧不得改写为 0 或空串冒充有披露。
type ReportEstimateItem struct {
	PublishDate  time.Time
	OrgSname     string
	OrgCode      *string
	EstYear      int
	Eps          *float64
	Pe           *float64
	AimPriceLow  *float64
	AimPriceHigh *float64
	Rating       *string
	LastRating   *string
	RatingChange *int
	ReportType   *int
	Researcher   *string
	Industry     *string
}

// ReportEstimateReport 某券发布日窗口内的研报预测截面。
// Found=false 表示窗口内无披露行（含券无研报覆盖），显式缺失不编数。
type ReportEstimateReport struct {
	Symbol      string
	Found       bool
	Days        int
	WindowStart time.Time
	AsOf        time.Time
	Rows        []ReportEstimateItem
}

// ReportEstimates 读 symbol 发布日 ∈ [asOf-days, asOf] 的研报预测行。
// 纯读（写入链归 datahub report 采集分支），days ≤0 默认 90、>400 截 400。
func (u *Usecase) ReportEstimates(ctx context.Context, symbol string, asOf time.Time, days int) (*ReportEstimateReport, error) {
	if days <= 0 {
		days = estimateDefaultDays
	}
	if days > estimateMaxDays {
		days = estimateMaxDays
	}
	since := asOf.AddDate(0, 0, -days)
	rows, err := u.repo.ReportEstimateRows(ctx, symbol, since, asOf)
	if err != nil {
		return nil, err
	}
	return &ReportEstimateReport{
		Symbol:      symbol,
		Found:       len(rows) > 0,
		Days:        days,
		WindowStart: since,
		AsOf:        asOf,
		Rows:        rows,
	}, nil
}
