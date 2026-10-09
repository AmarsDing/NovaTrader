package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"server/pkg/tradecal"
)

func sh(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04:05", s, tradecal.Shanghai())
	if err != nil {
		panic(err)
	}
	return t
}

func TestSkipsHolidayAndMakeupWeekend(t *testing.T) {
	mem := NewMemory()
	var ran []string
	job := Job{
		Name: "scan", Spec: "0 10 * * *", TradingDayOnly: true, MaxDelay: time.Hour,
		Run: func(context.Context) error {
			ran = append(ran, "scan")
			return nil
		},
	}
	s := New(tradecal.Default, mem, job)
	if err := s.Tick(context.Background(), sh("2026-10-01 10:05:00")); err != nil {
		t.Fatal(err)
	}
	if len(ran) != 0 {
		t.Fatal("holiday must not run")
	}
	if err := s.Tick(context.Background(), sh("2026-10-10 10:05:00")); err != nil {
		t.Fatal(err)
	}
	if len(ran) != 0 {
		t.Fatal("exchange is closed on a makeup Saturday")
	}
	if err := s.Tick(context.Background(), sh("2026-10-09 10:05:00")); err != nil {
		t.Fatal(err)
	}
	if len(ran) != 1 {
		t.Fatal("trading day should run")
	}
	if err := s.Tick(context.Background(), sh("2026-10-09 10:20:00")); err != nil {
		t.Fatal(err)
	}
	if len(ran) != 1 {
		t.Fatal("same slot ran twice")
	}
}

func TestCatchUpRetryAndDependency(t *testing.T) {
	mem := NewMemory()
	attempts := 0
	parent := Job{
		Name: "parent", Spec: "0 9 * * *", TradingDayOnly: true,
		Retry: 1, Timeout: time.Second,
		Run: func(context.Context) error {
			attempts++
			if attempts == 1 {
				return errors.New("temporary")
			}
			return nil
		},
	}
	childRan := 0
	child := Job{
		Name: "child", Spec: "5 9 * * *", TradingDayOnly: true, DependsOn: []string{"parent"},
		Run: func(context.Context) error {
			childRan++
			return nil
		},
	}
	s := New(tradecal.Default, mem, child, parent)
	if err := s.Tick(context.Background(), sh("2026-10-08 09:06:00")); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || childRan != 1 {
		t.Fatalf("attempts=%d child=%d", attempts, childRan)
	}
}
