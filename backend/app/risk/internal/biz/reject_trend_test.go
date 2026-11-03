package biz

import (
	"context"
	"errors"
	"testing"
	"time"

	"server/pkg/tradecal"
)

// BE-11：默认参数 —— end 空取今天（rig 时钟 2026-10-09 上海），days<=0 取 7，序列连续补零。
func TestRejectTrendDefaults(t *testing.T) {
	repo := &fakeRepo{}
	rig := newRig(t, repo)
	end, days, trend, err := rig.e.RejectTrend(context.Background(), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if end != "2026-10-09" || days != 7 {
		t.Fatalf("end=%s days=%d", end, days)
	}
	if len(trend.Days) != 7 {
		t.Fatalf("want 7 days, got %d", len(trend.Days))
	}
	for i, d := range trend.Days {
		want := time.Date(2026, 10, 3+i, 0, 0, 0, 0, tradecal.Shanghai()).Format("2006-01-02")
		if d.Date != want || d.Checks != 0 || d.Rejected != 0 {
			t.Fatalf("day %d: %+v, want %s zeros", i, d, want)
		}
	}
	wantFrom := time.Date(2026, 10, 3, 0, 0, 0, 0, tradecal.Shanghai())
	wantTo := time.Date(2026, 10, 10, 0, 0, 0, 0, tradecal.Shanghai())
	if !repo.trendFrom.Equal(wantFrom) || !repo.trendTo.Equal(wantTo) {
		t.Fatalf("repo window [%s, %s)", repo.trendFrom, repo.trendTo)
	}
}

// BE-11：稀疏聚合回填 —— 无数据日补 0，rules 聚合透传。
func TestRejectTrendBackfill(t *testing.T) {
	repo := &fakeRepo{trend: &RejectTrend{
		Days:  []RejectDay{{Date: "2026-10-06", Checks: 5, Rejected: 2}, {Date: "2026-10-08", Checks: 4, Rejected: 1}},
		Rules: map[string]int{"R01": 2, "R12": 1},
	}}
	rig := newRig(t, repo)
	end, days, trend, err := rig.e.RejectTrend(context.Background(), "2026-10-08", 3)
	if err != nil {
		t.Fatal(err)
	}
	if end != "2026-10-08" || days != 3 {
		t.Fatalf("end=%s days=%d", end, days)
	}
	want := []RejectDay{
		{Date: "2026-10-06", Checks: 5, Rejected: 2},
		{Date: "2026-10-07"},
		{Date: "2026-10-08", Checks: 4, Rejected: 1},
	}
	for i, d := range trend.Days {
		if d != want[i] {
			t.Fatalf("day %d: %+v, want %+v", i, d, want[i])
		}
	}
	if trend.Rules["R01"] != 2 || trend.Rules["R12"] != 1 || len(trend.Rules) != 2 {
		t.Fatalf("rules=%v", trend.Rules)
	}
}

// BE-11：非法日期 → ErrInvalid（service 映射 400）。
func TestRejectTrendInvalidEnd(t *testing.T) {
	rig := newRig(t, &fakeRepo{})
	for _, bad := range []string{"2026-10", "10-09", "2026-13-01", "abc"} {
		if _, _, _, err := rig.e.RejectTrend(context.Background(), bad, 7); !errors.Is(err, ErrInvalid) {
			t.Fatalf("end=%q: err=%v, want ErrInvalid", bad, err)
		}
	}
}

// BE-11：days 上限 31、负数回默认 7。
func TestRejectTrendDaysClamp(t *testing.T) {
	rig := newRig(t, &fakeRepo{})
	_, days, trend, err := rig.e.RejectTrend(context.Background(), "2026-10-09", 99)
	if err != nil {
		t.Fatal(err)
	}
	if days != 31 || len(trend.Days) != 31 || trend.Days[0].Date != "2026-09-09" {
		t.Fatalf("days=%d first=%s len=%d", days, trend.Days[0].Date, len(trend.Days))
	}
	_, days, trend, err = rig.e.RejectTrend(context.Background(), "2026-10-09", -5)
	if err != nil {
		t.Fatal(err)
	}
	if days != 7 || len(trend.Days) != 7 {
		t.Fatalf("days=%d len=%d", days, len(trend.Days))
	}
}

// BE-11：data 层错误透传；nil 结果不 panic。
func TestRejectTrendRepoErrorAndNil(t *testing.T) {
	dbErr := errors.New("db unavailable")
	rig := newRig(t, &fakeRepo{trendErr: dbErr})
	if _, _, _, err := rig.e.RejectTrend(context.Background(), "", 7); !errors.Is(err, dbErr) {
		t.Fatalf("err=%v", err)
	}
	rig = newRig(t, &fakeRepo{})
	_, _, trend, err := rig.e.RejectTrend(context.Background(), "2026-10-09", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(trend.Days) != 2 || trend.Rules != nil {
		t.Fatalf("trend=%+v", trend)
	}
}
