package tradecal

import (
	"testing"
	"time"
)

func at(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04:05", s, Shanghai())
	if err != nil {
		panic(err)
	}
	return t
}

func TestHolidayAndMakeup2026(t *testing.T) {
	cases := []struct {
		day  string
		open bool
	}{
		{"2026-10-01", false},
		{"2026-10-07", false},
		{"2026-10-08", true},
		{"2026-10-09", true},
		{"2026-10-10", false},
		{"2026-10-11", false},
		{"2026-02-14", false},
		{"2026-02-16", false},
		{"2026-01-04", false},
		{"2026-01-05", true},
		{"2026-01-01", false},
		{"2025-01-26", false},
		{"2025-01-28", false},
		{"2025-10-08", false},
	}
	for _, tc := range cases {
		open, err := Default.Open(at(tc.day + " 10:00:00"))
		if err != nil {
			t.Fatal(tc.day, err)
		}
		if open != tc.open {
			t.Fatalf("%s open=%v", tc.day, open)
		}
	}
}

func TestUnknownYear(t *testing.T) {
	if _, err := Default.Open(at("2027-03-02 10:00:00")); err == nil {
		t.Fatal("expected missing holiday table")
	}
}

func TestSessionBoundaries(t *testing.T) {
	cases := []struct {
		at      string
		phase   Phase
		late    bool
		trading bool
	}{
		{"2026-10-08 07:29:00", Closed, false, false},
		{"2026-10-08 07:30:00", PreOpen, false, false},
		{"2026-10-08 09:15:00", CallAuction, false, false},
		{"2026-10-08 09:25:00", PreMatch, false, false},
		{"2026-10-08 09:30:00", AMTrading, false, true},
		{"2026-10-08 11:30:00", NoonBreak, false, false},
		{"2026-10-08 13:00:00", PMTrading, false, true},
		{"2026-10-08 14:29:00", PMTrading, false, true},
		{"2026-10-08 14:30:00", PMTrading, true, true},
		{"2026-10-08 15:00:00", PostClose, false, false},
		{"2026-10-08 15:30:00", Closed, false, false},
		{"2026-10-01 10:00:00", Closed, false, false},
	}
	for _, tc := range cases {
		s, err := Default.SessionAt(at(tc.at))
		if err != nil {
			t.Fatal(err)
		}
		if s.Phase != tc.phase || s.Late != tc.late || s.Trading != tc.trading {
			t.Fatalf("%s got %+v", tc.at, s)
		}
	}
}

func TestNextOpenSkipsHoliday(t *testing.T) {
	next, err := Default.NextOpen(at("2026-09-30 16:00:00"))
	if err != nil {
		t.Fatal(err)
	}
	if next.Format("2006-01-02") != "2026-10-08" {
		t.Fatal(next)
	}
}

func TestLastClosedDay(t *testing.T) {
	cases := []struct{ at, want string }{
		{"2026-10-08 15:29:00", "2026-09-30"},
		{"2026-10-08 15:30:00", "2026-10-08"},
		{"2026-10-10 12:00:00", "2026-10-09"},
		{"2026-10-12 09:00:00", "2026-10-09"},
	}
	for _, tc := range cases {
		got, err := Default.LastClosedDay(at(tc.at))
		if err != nil {
			t.Fatal(err)
		}
		if got.Format("2006-01-02") != tc.want {
			t.Fatalf("%s got %s", tc.at, got.Format("2006-01-02"))
		}
	}
}

func TestSessionChange(t *testing.T) {
	a, _ := Default.SessionAt(at("2026-10-08 14:29:00"))
	b, _ := Default.SessionAt(at("2026-10-08 14:30:00"))
	if !Changed(a, b) {
		t.Fatal("late session should be a change")
	}
}
