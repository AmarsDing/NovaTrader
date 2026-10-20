package data

import (
	"context"
	"math"
	"testing"
	"time"

	"server/ent/reportestimate"
	"server/pkg/tradecal"
)

// ReportEstimateRows（B4）：窗口谓词 + publish_date 倒序/org_sname 次之排序 +
// Nillable 披露列 NULL 往返（没取到≠0，读侧原样透传不改写）。
func TestReportEstimateRowsRead(t *testing.T) {
	c := openTestDB(t)
	ctx := context.Background()
	if _, err := c.ReportEstimate.Delete().Exec(ctx); err != nil {
		t.Fatal(err)
	}
	r := NewRepo(c)
	day := time.Date(2026, 10, 20, 0, 0, 0, 0, tradecal.Shanghai())
	put := func(infoCode, org string, pub time.Time, year int) {
		if _, err := c.ReportEstimate.Create().
			SetSymbol("600519.SH").SetPublishDate(pub).
			SetOrgSname(org).SetInfoCode(infoCode).SetEstYear(year).
			SetSource("eastmoney").Save(ctx); err != nil {
			t.Fatal(err)
		}
	}
	// 同日两行检验 org_sname 次序（ASCII 前缀定序免 locale 漂移）
	put("IC-A", "Alpha证券", day.AddDate(0, 0, -3), 2026)
	put("IC-B", "Beta证券", day.AddDate(0, 0, -3), 2026)
	put("IC-C", "中金公司", day.AddDate(0, 0, -9), 2026)
	put("IC-D", "中金公司", day.AddDate(0, 0, -200), 2026) // 出 90 天窗
	if _, err := c.ReportEstimate.Create().
		SetSymbol("000001.SZ").SetPublishDate(day.AddDate(0, 0, -5)).
		SetOrgSname("中金公司").SetInfoCode("IC-X").SetEstYear(2026).
		SetSource("eastmoney").Save(ctx); err != nil {
		t.Fatal(err)
	}
	// IC-A 补披露列（eps=1.23 + 评级文本 + 评级动作 1），其余行保持 NULL
	if _, err := c.ReportEstimate.Update().
		Where(reportestimate.InfoCodeEQ("IC-A")).
		SetNillableEps(fp(1.23)).SetNillableRating(sp("买入")).SetNillableRatingChange(i32p(1)).
		Save(ctx); err != nil {
		t.Fatal(err)
	}

	got, err := r.ReportEstimateRows(ctx, "600519.SH", day.AddDate(0, 0, -90), day)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("count=%d want 3（窗口外/他券行被谓词排除）", len(got))
	}
	if got[0].OrgSname != "Alpha证券" || got[1].OrgSname != "Beta证券" || got[2].OrgSname != "中金公司" {
		t.Fatalf("排序错位: %s %s %s（want publish_date 倒序 + org_sname 升序）", got[0].OrgSname, got[1].OrgSname, got[2].OrgSname)
	}
	// 披露列往返：只有 IC-A 带值；零值不得改写
	if got[0].Eps == nil || math.Abs(*got[0].Eps-1.23) > 1e-9 || got[0].Rating == nil || *got[0].Rating != "买入" ||
		got[0].RatingChange == nil || *got[0].RatingChange != 1 {
		t.Fatalf("IC-A 披露列往返失败: %+v", got[0])
	}
	for i, it := range got {
		if i == 0 {
			continue
		}
		if it.Eps != nil || it.Pe != nil || it.Rating != nil || it.RatingChange != nil || it.AimPriceLow != nil {
			t.Fatalf("行 %d 缺披露应往返 nil: %+v", i, it)
		}
	}
}

func sp(v string) *string { return &v }
func i32p(v int) *int     { return &v }

// 窗口边界（对抗复核补强）：发布日 == since / == asOf 均命中（闭区间按上海日历日）；
// since/asOf 带时刻（含上海子夜后 00:30）按上海日历日截断不误伤；PublishDate 回读日历日
// （fromPgDay 零值守护）；Industry 等缺披露列 NULL 往返不冒有。
func TestReportEstimateRowsWindowBoundary(t *testing.T) {
	c := openTestDB(t)
	ctx := context.Background()
	if _, err := c.ReportEstimate.Delete().Exec(ctx); err != nil {
		t.Fatal(err)
	}
	r := NewRepo(c)
	day := time.Date(2026, 10, 20, 0, 0, 0, 0, tradecal.Shanghai())
	put := func(infoCode string, pub time.Time, industry *string) {
		cb := c.ReportEstimate.Create().
			SetSymbol("600519.SH").SetPublishDate(pub).
			SetOrgSname("中金公司").SetInfoCode(infoCode).SetEstYear(2026).
			SetSource("eastmoney")
		if industry != nil {
			cb.SetNillableIndustry(industry)
		}
		if _, err := cb.Save(ctx); err != nil {
			t.Fatal(err)
		}
	}
	// 边界两侧：== since、== asOf 命中；各出窗一天者排除。
	put("IC-EQ-SINCE", day.AddDate(0, 0, -30), nil)
	put("IC-EQ-ASOF", day, sp("新能源"))
	put("IC-OUT-LO", day.AddDate(0, 0, -31), nil)
	put("IC-OUT-HI", day.AddDate(0, 0, 1), nil)

	// since 带时刻（15:00 收盘后查询截面）→ 按上海日历日截 9-20 零点，边界日仍命中。
	since := time.Date(2026, 9, 20, 15, 0, 0, 0, tradecal.Shanghai())
	// asOf 上海子夜后 00:30（UTC 为 10-19 16:30）→ 上海日历日仍 10-20，当日行命中。
	asOf := time.Date(2026, 10, 20, 0, 30, 0, 0, tradecal.Shanghai())
	got, err := r.ReportEstimateRows(ctx, "600519.SH", since, asOf)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("count=%d want 2（边界闭区间、出窗行排除）", len(got))
	}
	if got[0].PublishDate.Format("2006-01-02") != "2026-10-20" ||
		got[1].PublishDate.Format("2006-01-02") != "2026-09-20" {
		t.Fatalf("PublishDate 回读日历日错位: %v / %v（fromPgDay 零值守护）",
			got[0].PublishDate, got[1].PublishDate)
	}
	if got[0].Industry == nil || *got[0].Industry != "新能源" {
		t.Fatalf("IC-EQ-ASOF Industry 丢失: %+v", got[0])
	}
	if got[1].Industry != nil || got[1].LastRating != nil || got[1].Researcher != nil || got[1].OrgCode != nil {
		t.Fatalf("IC-EQ-SINCE 缺披露列应 NULL 往返: %+v", got[1])
	}
}
