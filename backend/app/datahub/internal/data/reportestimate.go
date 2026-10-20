package data

import (
	"context"

	"server/app/datahub/internal/biz"
	"server/ent"
	"server/ent/reportestimate"
)

// UpsertReportEstimates 写 report_estimate（目录册增补 B4），(info_code, est_year) 幂等，
// PIT：不同券商/不同时点研报各自成行互不覆盖。对抗复核修订后的 upsert 合同（同
// stock_basic「可空字段只在有值时覆盖」先例）：
//   - 批内按幂等键去重（页边界漂移同键双现 → 同语句 ON CONFLICT 21000 楔形，见底注）；
//     同源同键双现取最后一份。
//   - 冲突更新只刷锚列与 source；披露列不进 DO UPDATE SET——「二次采集披露更少将旧值
//     抹回 NULL」（缺列 padding NULL 进入并集列后 EXCLUDED 会把已披露值冲掉）属
//     「没取到≠0」的反向破口：缺披露≠撤披露。
//   - 披露列条件补丁：有值才覆盖，nil 保留旧值；首次插入的缺失列保持 NULL（不冒充 0）。
func (r *Repo) UpsertReportEstimates(ctx context.Context, items []biz.EstimateRow, source string, rd biz.Ready) (int, error) {
	return r.withReady(ctx, rd, func(tx *ent.Tx) (int, error) {
		rows := dedupeEstimateRows(items)
		bulk := make([]*ent.ReportEstimateCreate, 0, len(rows))
		for _, it := range rows {
			bulk = append(bulk, tx.ReportEstimate.Create().
				SetSymbol(it.Symbol).
				SetPublishDate(day(it.PublishDate)).
				SetOrgSname(it.OrgSname).
				SetNillableOrgCode(it.OrgCode).
				SetInfoCode(it.InfoCode).
				SetEstYear(it.EstYear).
				SetNillableEps(it.Eps).
				SetNillablePe(it.Pe).
				SetNillableAimPriceLow(it.AimPriceLow).
				SetNillableAimPriceHigh(it.AimPriceHigh).
				SetNillableRating(it.Rating).
				SetNillableLastRating(it.LastRating).
				SetNillableRatingChange(it.RatingChange).
				SetNillableReportType(it.ReportType).
				SetNillableResearcher(it.Researcher).
				SetNillableIndustry(it.Industry).
				SetSource(source))
		}
		err := chunks(bulk, func(part []*ent.ReportEstimateCreate) error {
			return tx.ReportEstimate.CreateBulk(part...).
				OnConflictColumns(reportestimate.FieldInfoCode, reportestimate.FieldEstYear).
				Update(func(u *ent.ReportEstimateUpsert) {
					// 见函数头合同：冲突只刷锚列与来源，披露列走下方条件补丁。
					u.UpdateSymbol().UpdatePublishDate().UpdateOrgSname().UpdateSource()
				}).Exec(ctx)
		})
		if err != nil {
			return 0, err
		}
		for _, it := range rows {
			upd := tx.ReportEstimate.Update().Where(
				reportestimate.InfoCodeEQ(it.InfoCode), reportestimate.EstYearEQ(it.EstYear))
			n := 0
			if it.OrgCode != nil {
				upd.SetOrgCode(*it.OrgCode)
				n++
			}
			if it.Eps != nil {
				upd.SetEps(*it.Eps)
				n++
			}
			if it.Pe != nil {
				upd.SetPe(*it.Pe)
				n++
			}
			if it.AimPriceLow != nil {
				upd.SetAimPriceLow(*it.AimPriceLow)
				n++
			}
			if it.AimPriceHigh != nil {
				upd.SetAimPriceHigh(*it.AimPriceHigh)
				n++
			}
			if it.Rating != nil {
				upd.SetRating(*it.Rating)
				n++
			}
			if it.LastRating != nil {
				upd.SetLastRating(*it.LastRating)
				n++
			}
			if it.RatingChange != nil {
				upd.SetRatingChange(*it.RatingChange)
				n++
			}
			if it.ReportType != nil {
				upd.SetReportType(*it.ReportType)
				n++
			}
			if it.Researcher != nil {
				upd.SetResearcher(*it.Researcher)
				n++
			}
			if it.Industry != nil {
				upd.SetIndustry(*it.Industry)
				n++
			}
			if n == 0 {
				continue
			}
			if err := upd.Exec(ctx); err != nil {
				return 0, err
			}
		}
		return len(rows), nil
	})
}

// dedupeEstimateRows 批内按 (info_code, est_year) 去重，后者覆盖前者。
// 不做的后果：东财 reportapi 顺序翻页（最多 10 页）在采集途中新研报灌入造成页边界
// 漂移时，同一 infoCode 可在单轮 items 双现；PG 对同语句同键 ON CONFLICT DO UPDATE
// 报 21000 cardinality violation → 整 chunk/整事务失败 → report 域按纪律③停发
// 停推水位，洪峰页稳定前自锁楔形。同仓先例：UpsertFinance seen map、SaveIssues 合并。
func dedupeEstimateRows(items []biz.EstimateRow) []biz.EstimateRow {
	type key struct {
		ic string
		y  int
	}
	seen := make(map[key]int, len(items))
	rows := make([]biz.EstimateRow, 0, len(items))
	for _, it := range items {
		k := key{it.InfoCode, it.EstYear}
		if j, ok := seen[k]; ok {
			rows[j] = it
			continue
		}
		seen[k] = len(rows)
		rows = append(rows, it)
	}
	return rows
}
