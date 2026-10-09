package risk

import (
	"testing"
	"time"
)

func TestRejectAlert(t *testing.T) {
	p := Defaults()
	p.RejectAlertMin = 4
	p.RejectAlertRate = 0.5
	p.RejectAlertWindow = 5 * time.Minute
	p.RejectAlertCooldown = time.Minute
	now := time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)

	marks := []CheckMark{
		{At: now.Add(-time.Minute), Approved: false},
		{At: now.Add(-time.Minute), Approved: false},
		{At: now.Add(-time.Minute), Approved: true},
	}
	if _, _, fire := RejectAlert(marks, now, p, time.Time{}); fire {
		t.Fatal("below min samples")
	}
	marks = append(marks, CheckMark{At: now, Approved: false})
	n, rate, fire := RejectAlert(marks, now, p, time.Time{})
	if !fire || n != 4 || rate != 0.75 {
		t.Fatalf("n=%d rate=%v fire=%v", n, rate, fire)
	}
	if _, _, fire := RejectAlert(marks, now.Add(30*time.Second), p, now); fire {
		t.Fatal("cooldown")
	}
	if _, _, fire := RejectAlert(marks, now.Add(time.Minute), p, now); !fire {
		t.Fatal("cooldown elapsed")
	}
	old := []CheckMark{{At: now.Add(-10 * time.Minute), Approved: false}}
	if _, _, fire := RejectAlert(append(old, marks...), now, p, time.Time{}); !fire {
		t.Fatal("stale marks must not count")
	}
	p.RejectAlertMin = 0
	if _, _, fire := RejectAlert(marks, now, p, time.Time{}); fire {
		t.Fatal("min 0 disables")
	}
}
