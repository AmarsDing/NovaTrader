package scheduler

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type field struct {
	any  bool
	vals map[int]bool
}

func parseSpec(spec string) ([5]field, error) {
	parts := strings.Fields(spec)
	if len(parts) != 5 {
		return [5]field{}, fmt.Errorf("scheduler: want 5 cron fields, got %q", spec)
	}
	ranges := [][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 7}}
	var out [5]field
	for i, part := range parts {
		f, err := parseField(part, ranges[i][0], ranges[i][1])
		if err != nil {
			return out, err
		}
		if i == 4 {
			if f.vals[7] {
				f.vals[0] = true
				delete(f.vals, 7)
			}
		}
		out[i] = f
	}
	return out, nil
}

func parseField(s string, min, max int) (field, error) {
	f := field{vals: map[int]bool{}}
	if s == "*" {
		f.any = true
		return f, nil
	}
	step := 1
	body := s
	if left, right, ok := strings.Cut(s, "/"); ok {
		body = left
		n, err := strconv.Atoi(right)
		if err != nil || n <= 0 {
			return f, fmt.Errorf("scheduler: bad step %q", s)
		}
		step = n
		if body == "*" {
			body = fmt.Sprintf("%d-%d", min, max)
		}
	}
	for _, piece := range strings.Split(body, ",") {
		from, to := piece, piece
		if a, b, ok := strings.Cut(piece, "-"); ok {
			from, to = a, b
		}
		start, err1 := strconv.Atoi(from)
		end, err2 := strconv.Atoi(to)
		if err1 != nil || err2 != nil || start < min || end > max || start > end {
			return f, fmt.Errorf("scheduler: bad field %q", s)
		}
		for n := start; n <= end; n += step {
			f.vals[n] = true
		}
	}
	return f, nil
}

func (f field) has(n int) bool {
	return f.any || f.vals[n]
}

func match(spec [5]field, t time.Time) bool {
	dom := spec[2].has(t.Day())
	dow := spec[4].has(int(t.Weekday()))
	dayOK := dom && dow
	if !spec[2].any && !spec[4].any {
		dayOK = dom || dow
	}
	return spec[0].has(t.Minute()) && spec[1].has(t.Hour()) && spec[3].has(int(t.Month())) && dayOK
}

// Previous 返回不晚于 now 的最近一次触发时刻，精度到分钟。最多回看 48 小时。
func Previous(spec string, now time.Time) (time.Time, error) {
	parsed, err := parseSpec(spec)
	if err != nil {
		return time.Time{}, err
	}
	t := now.Truncate(time.Minute)
	limit := t.Add(-48 * time.Hour)
	for !t.Before(limit) {
		if match(parsed, t) {
			return t, nil
		}
		t = t.Add(-time.Minute)
	}
	return time.Time{}, fmt.Errorf("scheduler: no slot within 48h of %s", now.Format(time.RFC3339))
}
