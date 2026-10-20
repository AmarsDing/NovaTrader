package data

import (
	"context"
	"time"

	"server/app/market/internal/biz"
	"server/ent"
	"server/ent/reportestimate"
)

// ReportEstimateRows 读 symbol 发布日在 [since, asOf] 的研报预测行（目录册增补 B4）。
// publish_date 倒序、org_sname 次之；Nillable 列指针直传——缺披露保持 nil 不落 0。
func (r *Repo) ReportEstimateRows(ctx context.Context, symbol string, since, asOf time.Time) ([]biz.ReportEstimateItem, error) {
	rows, err := r.db.ReportEstimate.Query().
		Where(
			reportestimate.SymbolEQ(symbol),
			reportestimate.PublishDateGTE(pgDay(since)),
			reportestimate.PublishDateLTE(pgDay(asOf)),
		).
		Order(ent.Desc(reportestimate.FieldPublishDate), ent.Asc(reportestimate.FieldOrgSname)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]biz.ReportEstimateItem, 0, len(rows))
	for _, row := range rows {
		out = append(out, biz.ReportEstimateItem{
			PublishDate:  fromPgDay(row.PublishDate),
			OrgSname:     row.OrgSname,
			OrgCode:      row.OrgCode,
			EstYear:      row.EstYear,
			Eps:          row.Eps,
			Pe:           row.Pe,
			AimPriceLow:  row.AimPriceLow,
			AimPriceHigh: row.AimPriceHigh,
			Rating:       row.Rating,
			LastRating:   row.LastRating,
			RatingChange: row.RatingChange,
			ReportType:   row.ReportType,
			Researcher:   row.Researcher,
			Industry:     row.Industry,
		})
	}
	return out, nil
}
