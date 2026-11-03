package biz

import (
	"context"
	"errors"
	"testing"
	"time"

	"server/pkg/tradecal"
)

func TestRejectRecordsDefaults(t *testing.T) {
	repo := &fakeRepo{records: &RejectRecords{Total: 2, Items: []RejectRecord{{Rule: "R03"}, {Rule: "R05"}}}}
	rig := newRig(t, repo)
	res, err := rig.e.RejectRecords(context.Background(), RejectRecordsQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if res.End != "2026-10-09" || res.Days != 1 || res.Limit != 50 || res.Offset != 0 {
		t.Fatalf("normalized = end %s days %d limit %d offset %d", res.End, res.Days, res.Limit, res.Offset)
	}
	wantFrom := time.Date(2026, 10, 9, 0, 0, 0, 0, tradecal.Shanghai())
	wantTo := wantFrom.AddDate(0, 0, 1)
	if !repo.recordsFilter.From.Equal(wantFrom) || !repo.recordsFilter.To.Equal(wantTo) {
		t.Fatalf("repo window [%s, %s)", repo.recordsFilter.From, repo.recordsFilter.To)
	}
	if res.Total != 2 || len(res.Items) != 2 {
		t.Fatalf("result = total %d items %d", res.Total, len(res.Items))
	}
}

func TestRejectRecordsClampAndFilters(t *testing.T) {
	repo := &fakeRepo{records: &RejectRecords{}}
	rig := newRig(t, repo)
	res, err := rig.e.RejectRecords(context.Background(), RejectRecordsQuery{
		End:         "2026-10-01",
		Days:        99,
		Rule:        " R03 ",
		Symbol:      " 600519.SH ",
		Operator:    " alice ",
		AccountType: " SIM ",
		Reason:      " 黑名单 ",
		Offset:      -5,
		Limit:       9999,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.End != "2026-10-01" || res.Days != 31 || res.Limit != 200 || res.Offset != 0 {
		t.Fatalf("clamped = end %s days %d limit %d offset %d", res.End, res.Days, res.Limit, res.Offset)
	}
	fl := repo.recordsFilter
	if fl.Rule != "R03" || fl.Symbol != "600519.SH" || fl.Operator != "alice" || fl.AccountType != "SIM" || fl.Reason != "黑名单" {
		t.Fatalf("filters = %+v", fl)
	}
	// end=2026-10-01 回看 31 天（含当天）：from = 10-01 减 30 天 = 09-01
	wantFrom := time.Date(2026, 9, 1, 0, 0, 0, 0, tradecal.Shanghai())
	if !fl.From.Equal(wantFrom) {
		t.Fatalf("from = %s, want %s", fl.From, wantFrom)
	}
}

func TestRejectRecordsInvalidEnd(t *testing.T) {
	rig := newRig(t, nil)
	for _, end := range []string{"2026/10/09", "2026-1-1", "abc"} {
		if _, err := rig.e.RejectRecords(context.Background(), RejectRecordsQuery{End: end}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("end %q: err = %v, want ErrInvalid", end, err)
		}
	}
}

func TestRejectRecordsRepoErrorAndNil(t *testing.T) {
	dbErr := errors.New("db down")
	rig := newRig(t, &fakeRepo{recordsErr: dbErr})
	if _, err := rig.e.RejectRecords(context.Background(), RejectRecordsQuery{}); !errors.Is(err, dbErr) {
		t.Fatalf("err = %v, want repo error", err)
	}
	rig2 := newRig(t, &fakeRepo{})
	res, err := rig2.e.RejectRecords(context.Background(), RejectRecordsQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if res == nil || res.Total != 0 || len(res.Items) != 0 {
		t.Fatalf("nil repo result = %+v", res)
	}
}
