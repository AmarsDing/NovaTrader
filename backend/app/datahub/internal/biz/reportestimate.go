package biz

import "time"

/* ---------- 研报盈利预测落库（目录册增补 B4） ---------- */

// EstimateRow 一份研报×一个预测年度的落库行（report_estimate 表，PIT：多券商分歧各自成行。
// 披露列缺披露保持 nil——消费解析产物 Intel.Forecast 的 omitempty/哨兵口径，
// 「没取到≠0」原样传导，聚合层不得改写为 0 或空串。
type EstimateRow struct {
	Symbol       string
	PublishDate  time.Time
	OrgSname     string
	OrgCode      *string
	InfoCode     string
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

// sp0 空串 → nil（ReportForecast 的 omitempty 串字段映射到 Nillable 列）。
func sp0(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// clip 截到 ent MaxLen（按字符计）。不做的后果：任一源值超长 → ent 校验失败 →
// 整 chunk/整事务失败 → report 域按纪律③停发停推水位，而水位以日粒度回看、
// 毒行下轮又被拉回——单行可把 report 事件流卡近一天。截断仅作用于展示标签列，
// 幂等键（info_code）与数值内容不受影响。
func clip(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

// 标签列长度镜 ent/schema/report_estimate.go 的 MaxLen（改 schema 必须同步改此处）。
const (
	clipMaxSymbol     = 16
	clipMaxOrgSname   = 32
	clipMaxOrgCode    = 16
	clipMaxRating     = 16
	clipMaxResearcher = 64
	clipMaxIndustry   = 32
	clipMaxInfoCode   = 32
)

// forecastToEstimateRows 把一条 kind=report 的 Intel 展平为行集（每预测年度一行）。
// 同解析层三重纪律缝合：
//   - Forecast==nil（源头未披露扩展列）→ 无行（不发空行）；
//   - 双 nil 年度点不建行（同解析层「双 nil 年度点整条丢弃」先例）；
//   - Codes 空（无归一 symbol）、Forecast.OrgSName 空串（无签发主体）、SourceID 空（无幂等锚）、
//     PublishTime 解析失败（无披露日锚）→ 整条无行（不入缺锚行）。
func forecastToEstimateRows(it Intel) []EstimateRow {
	if it.Forecast == nil || len(it.Codes) == 0 || it.SourceID == "" || it.Forecast.OrgSName == "" {
		return nil
	}
	if len([]rune(it.SourceID)) > clipMaxInfoCode {
		return nil // 幂等锚超长：截断会撞键合并异研报，丢行保锚完整性（不截标签列的例外）
	}
	pd, err := time.Parse(time.RFC3339, it.PublishTime)
	if err != nil {
		return nil
	}
	fc := it.Forecast
	base := EstimateRow{
		Symbol: clip(it.Codes[0], clipMaxSymbol), PublishDate: pd, OrgSname: clip(fc.OrgSName, clipMaxOrgSname),
		OrgCode: sp0(clip(fc.OrgCode, clipMaxOrgCode)), InfoCode: it.SourceID,
		AimPriceLow: fc.AimPriceLow, AimPriceHigh: fc.AimPriceHigh,
		Rating: sp0(clip(fc.Rating, clipMaxRating)), LastRating: sp0(clip(fc.LastRating, clipMaxRating)),
		RatingChange: fc.RatingChange, ReportType: fc.ReportType,
		Researcher: sp0(clip(fc.Researcher, clipMaxResearcher)), Industry: sp0(clip(fc.Industry, clipMaxIndustry)),
	}
	out := make([]EstimateRow, 0, len(fc.EPS))
	for _, p := range fc.EPS {
		if p.EPS == nil && p.PE == nil {
			continue // 双 nil 年度点不建行（同解析层则）
		}
		r := base
		r.EstYear, r.Eps, r.Pe = p.Year, p.EPS, p.PE
		out = append(out, r)
	}
	return out
}

// EstimateRowsOf 聚合一批 Intel 的预测行（仅 Forecast 非空者产行）。
func EstimateRowsOf(items []Intel) []EstimateRow {
	var out []EstimateRow
	for _, it := range items {
		out = append(out, forecastToEstimateRows(it)...)
	}
	return out
}
