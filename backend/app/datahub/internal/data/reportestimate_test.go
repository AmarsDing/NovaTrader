package data

import (
	"context"
	"testing"
	"time"

	"server/app/datahub/internal/biz"
	"server/ent/reportestimate"
)

// B4：UpsertReportEstimates 幂等（info_code+est_year）+ 缺披露列 NULL 往返（不降 0/空串）。
func TestUpsertReportEstimatesIdempotentNullRoundtrip(t *testing.T) {
	client := captureClient(t)
	repo := NewRepo(client)
	ctx := context.Background()
	pd := time.Date(2026, 10, 19, 15, 30, 0, 0, shanghaiLoc()) // 带时刻，day() 应截到日期
	rd := biz.Ready{Domain: "", Source: biz.SourceEastmoney}   // Domain 置空避开 outbox 依赖
	t.Cleanup(func() {
		_, _ = client.ReportEstimate.Delete().Where(reportestimate.InfoCodeHasPrefix("IC-RE-")).Exec(ctx)
	})

	e1, p1, e2 := 0.64, 20.18, 1.10
	oc, r1, rs, ind := "80001", "买入", "张三", "白酒"
	rc := 2
	items := []biz.EstimateRow{
		{Symbol: "600519", PublishDate: pd, OrgSname: "东吴证券", OrgCode: &oc, InfoCode: "IC-RE-A",
			EstYear: 2026, Eps: &e1, Pe: &p1, Rating: &r1, RatingChange: &rc, Researcher: &rs, Industry: &ind},
		// 次年点仅披露 EPS：其余全缺列保持 NULL——「没取到≠0」。
		{Symbol: "600519", PublishDate: pd, OrgSname: "东吴证券", InfoCode: "IC-RE-A", EstYear: 2027, Eps: &e2},
	}
	if n, err := repo.UpsertReportEstimates(ctx, items, "eastmoney", rd); err != nil || n != 2 {
		t.Fatalf("first upsert n=%d err=%v", n, err)
	}

	// 同年份重采：值覆盖、不重复落行；评级由 买入 -> 增持。
	e3, r2 := 0.65, "增持"
	if n, err := repo.UpsertReportEstimates(ctx, []biz.EstimateRow{
		{Symbol: "600519", PublishDate: pd, OrgSname: "东吴证券", OrgCode: &oc, InfoCode: "IC-RE-A",
			EstYear: 2026, Eps: &e3, Pe: &p1, Rating: &r2},
	}, "eastmoney", rd); err != nil || n != 1 {
		t.Fatalf("second upsert n=%d err=%v", n, err)
	}

	cnt, err := client.ReportEstimate.Query().Where(reportestimate.InfoCodeEQ("IC-RE-A")).Count(ctx)
	if err != nil || cnt != 2 {
		t.Fatalf("count=%d err=%v（幂等键 info_code+est_year 不应重复落行）", cnt, err)
	}
	y26, err := client.ReportEstimate.Query().Where(
		reportestimate.InfoCodeEQ("IC-RE-A"), reportestimate.EstYearEQ(2026)).Only(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if y26.Eps == nil || *y26.Eps != 0.65 || y26.Rating == nil || *y26.Rating != "增持" {
		t.Fatalf("y26=%+v（冲突行应被新值覆盖）", y26)
	}
	if y26.Pe == nil || *y26.Pe != 20.18 || y26.OrgCode == nil || *y26.OrgCode != "80001" {
		t.Fatalf("y26=%+v（补披露应保留）", y26)
	}
	if y26.PublishDate.Format("2006-01-02") != "2026-10-19" {
		t.Fatalf("publish_date 应截到日期: %v", y26.PublishDate)
	}
	if y26.Source != "eastmoney" {
		t.Fatalf("source=%q", y26.Source)
	}
	y27, err := client.ReportEstimate.Query().Where(
		reportestimate.InfoCodeEQ("IC-RE-A"), reportestimate.EstYearEQ(2027)).Only(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if y27.Eps == nil || *y27.Eps != 1.10 {
		t.Fatalf("y27=%+v", y27)
	}
	if y27.Pe != nil || y27.Rating != nil || y27.OrgCode != nil || y27.ReportType != nil ||
		y27.Researcher != nil || y27.AimPriceLow != nil || y27.LastRating != nil ||
		y27.AimPriceHigh != nil || y27.RatingChange != nil || y27.Industry != nil {
		t.Fatalf("y27 缺披露列必须保持 NULL：%+v", y27)
	}
}

// 对抗复核补强：①批内同幂等键双现不触 21000（去重，后到的覆盖）；
// ②冲突更新绝不把已披露值抹回 NULL（二次采集披露更少 ≠ 撤披露）。
func TestUpsertReportEstimatesDedupAndNoErase(t *testing.T) {
	client := captureClient(t)
	repo := NewRepo(client)
	ctx := context.Background()
	pd := time.Date(2026, 10, 19, 10, 0, 0, 0, shanghaiLoc())
	rd := biz.Ready{Domain: "", Source: biz.SourceEastmoney}
	t.Cleanup(func() {
		_, _ = client.ReportEstimate.Delete().Where(reportestimate.InfoCodeHasPrefix("IC-RE-")).Exec(ctx)
	})

	// 批内 (IC-RE-D,2026) 双现（页边界漂移形态）+ 不同年一行。
	e5, e6, e11 := 0.5, 0.6, 1.1
	rt, ind := "买入", "白酒"
	dupes := []biz.EstimateRow{
		{Symbol: "600519", PublishDate: pd, OrgSname: "东吴证券", InfoCode: "IC-RE-D", EstYear: 2026, Eps: &e5},
		{Symbol: "600519", PublishDate: pd, OrgSname: "东吴证券", InfoCode: "IC-RE-D", EstYear: 2027, Eps: &e11},
		{Symbol: "600519", PublishDate: pd, OrgSname: "东吴证券", InfoCode: "IC-RE-D", EstYear: 2026, Eps: &e6, Rating: &rt, Industry: &ind},
	}
	if n, err := repo.UpsertReportEstimates(ctx, dupes, "eastmoney", rd); err != nil || n != 2 {
		t.Fatalf("dedup upsert n=%d err=%v（同键双现应去重为 2 行）", n, err)
	}
	y26, err := client.ReportEstimate.Query().Where(
		reportestimate.InfoCodeEQ("IC-RE-D"), reportestimate.EstYearEQ(2026)).Only(ctx)
	if err != nil || y26.Eps == nil || *y26.Eps != 0.6 {
		t.Fatalf("y26=%+v err=%v（后者覆盖）", y26, err)
	}

	// 二次采集该行披露更少（仅 EPS）：同批另一行的 Industry 让 industry 进入并集列——
	// 旧合同 UpdateNewValues 会把 y26.industry 抹 NULL；新合同必须保留。
	e7, ind2 := 0.66, "半导体"
	if n, err := repo.UpsertReportEstimates(ctx, []biz.EstimateRow{
		{Symbol: "600519", PublishDate: pd, OrgSname: "东吴证券", InfoCode: "IC-RE-D", EstYear: 2026, Eps: &e7},
		{Symbol: "600519", PublishDate: pd, OrgSname: "东吴证券", InfoCode: "IC-RE-D", EstYear: 2028, Industry: &ind2},
	}, "eastmoney", rd); err != nil || n != 2 {
		t.Fatalf("re-collect n=%d err=%v", n, err)
	}
	y26b, err := client.ReportEstimate.Query().Where(
		reportestimate.InfoCodeEQ("IC-RE-D"), reportestimate.EstYearEQ(2026)).Only(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if y26b.Eps == nil || *y26b.Eps != 0.66 {
		t.Fatalf("y26=%+v（EPS 应被新值覆盖）", y26b)
	}
	if y26b.Rating == nil || *y26b.Rating != "买入" || y26b.Industry == nil || *y26b.Industry != "白酒" {
		t.Fatalf("y26=%+v（已披露值不得被抹回 NULL——缺披露≠撤披露）", y26b)
	}
	y28, err := client.ReportEstimate.Query().Where(
		reportestimate.InfoCodeEQ("IC-RE-D"), reportestimate.EstYearEQ(2028)).Only(ctx)
	if err != nil || y28.Industry == nil || *y28.Industry != "半导体" || y28.Eps != nil {
		t.Fatalf("y28=%+v err=%v", y28, err)
	}
	cnt, err := client.ReportEstimate.Query().Where(reportestimate.InfoCodeEQ("IC-RE-D")).Count(ctx)
	if err != nil || cnt != 3 {
		t.Fatalf("count=%d err=%v", cnt, err)
	}
}
