package biz

import (
	"testing"
	"time"

	"server/pkg/rules"
)

func TestAttribute(t *testing.T) {
	cases := []struct {
		c    Candidate
		want string
	}{
		{Candidate{SignalStatus: "expired"}, AttrUnfilled},
		{Candidate{SignalStatus: "done"}, AttrEmotion},
		{Candidate{SignalStatus: "done", ExitKind: rules.ExitBadNews}, AttrNews},
		{Candidate{SignalStatus: "done", NewsDim: 80}, AttrNews},
		{Candidate{SignalStatus: "done"}, AttrIndicator},
	}
	phases := []string{string(rules.StageWarm), string(rules.StageIce), string(rules.StageWarm), string(rules.StageWarm), string(rules.StageWarm)}
	for i, c := range cases {
		if got := attribute(c.c, phases[i]); got != c.want {
			t.Fatalf("%d: got %s want %s", i, got, c.want)
		}
	}
}

func TestPeriodRange(t *testing.T) {
	d := time.Date(2026, 10, 9, 15, 0, 0, 0, time.Local) // 周五
	start, end, err := PeriodRange("week", d)
	if err != nil || start.Format("2006-01-02") != "2026-10-05" || end.Format("2006-01-02") != "2026-10-11" {
		t.Fatalf("%v %v %v", start, end, err)
	}
	start, end, err = PeriodRange("month", d)
	if err != nil || start.Day() != 1 || end.Day() != 31 {
		t.Fatalf("%v %v", start, end)
	}
	if _, _, err := PeriodRange("year", d); err == nil {
		t.Fatal("year 应拒绝")
	}
}

func TestShadowMonotonic(t *testing.T) {
	s := func(score, t3 float64) Shadow {
		return Shadow{Score: &score, RetT3: &t3, Status: "done"}
	}
	up := shadowStats([]Shadow{s(0.1, 0.01), s(0.5, 0.02), s(0.9, 0.03)})
	if !up.Monotonic || up.Horizons[1].Count != 3 {
		t.Fatalf("%+v", up)
	}
	down := shadowStats([]Shadow{s(10, 0.05), s(90, -0.01)})
	if down.Monotonic {
		t.Fatal("高分收益更低应标为不单调")
	}
	if down.Buckets[0].Avg[3] != 0.05 || down.Buckets[4].Avg[3] != -0.01 {
		t.Fatalf("%+v", down.Buckets)
	}
}
