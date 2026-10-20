package biz

import (
	"context"
	"testing"
	"time"

	"server/pkg/tradecal"
)

// 窗口与透传：asOf 回看 90 天（默认），found=true，行原样透传（披露列指针不触碰）。
func TestReportEstimatesOK(t *testing.T) {
	asOf := time.Date(2026, 10, 20, 0, 0, 0, 0, tradecal.Shanghai())
	eps := 1.23
	rating := "买入"
	repo := &fakeRepo{estRows: []ReportEstimateItem{
		{PublishDate: asOf.AddDate(0, 0, -3), OrgSname: "中金公司", EstYear: 2026, Eps: &eps, Rating: &rating},
		{PublishDate: asOf.AddDate(0, 0, -9), OrgSname: "华泰证券", EstYear: 2027},
	}}
	rep, err := newChipUsecase(repo).ReportEstimates(context.Background(), "600519.SH", asOf, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Found || len(rep.Rows) != 2 || rep.Days != 90 {
		t.Fatalf("found=%v rows=%d days=%d", rep.Found, len(rep.Rows), rep.Days)
	}
	if rep.WindowStart != asOf.AddDate(0, 0, -90) || rep.AsOf != asOf {
		t.Fatalf("window %v..%v", rep.WindowStart, rep.AsOf)
	}
	if rep.Rows[0].Eps == nil || *rep.Rows[0].Eps != 1.23 || rep.Rows[1].Eps != nil || rep.Rows[1].Rating != nil {
		t.Fatalf("指针透传错位: %+v / %+v", rep.Rows[0], rep.Rows[1])
	}
	// 参数管道钉序：since/asOf 若写反会静默成恒空窗（found=false 且无错）。
	if repo.estSym != "600519.SH" || repo.estSince != asOf.AddDate(0, 0, -90) || repo.estAsOf != asOf {
		t.Fatalf("参数管道错位: sym=%q since=%v asOf=%v", repo.estSym, repo.estSince, repo.estAsOf)
	}
}

// 窗口内无行 → found=false（显式缺失，行集为空不编数）。
func TestReportEstimatesEmpty(t *testing.T) {
	asOf := time.Date(2026, 10, 20, 0, 0, 0, 0, tradecal.Shanghai())
	rep, err := newChipUsecase(&fakeRepo{}).ReportEstimates(context.Background(), "600519.SH", asOf, 0)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Found || len(rep.Rows) != 0 {
		t.Fatalf("found=%v rows=%d want found=false", rep.Found, len(rep.Rows))
	}
}

// days 截断：≤0 默认 90、>400 截 400（窗口随之移动）。
func TestReportEstimatesClampDays(t *testing.T) {
	asOf := time.Date(2026, 10, 20, 0, 0, 0, 0, tradecal.Shanghai())
	uc := newChipUsecase(&fakeRepo{})
	rep, err := uc.ReportEstimates(context.Background(), "600519.SH", asOf, 999)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Days != 400 || rep.WindowStart != asOf.AddDate(0, 0, -400) {
		t.Fatalf("days=%d windowStart=%v want 400/%v", rep.Days, rep.WindowStart, asOf.AddDate(0, 0, -400))
	}
	rep2, err := uc.ReportEstimates(context.Background(), "600519.SH", asOf, -1)
	if err != nil {
		t.Fatal(err)
	}
	if rep2.Days != 90 {
		t.Fatalf("days=%d want 90", rep2.Days)
	}
}
