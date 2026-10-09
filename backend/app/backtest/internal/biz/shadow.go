package biz

import (
	"context"
	"math"
	"time"

	"server/pkg/backtest"
	"server/pkg/tradecal"
)

var shadowHorizons = []int{1, 3, 5}

// SyncShadow 把新研判写入影子表，再为未完成的行补参考价和收益。返回新增和更新的行数。
func (uc *Usecase) SyncShadow(ctx context.Context) (added, updated int, err error) {
	after, err := uc.repo.MaxShadowDecision(ctx)
	if err != nil {
		return 0, 0, err
	}
	for {
		rows, err := uc.repo.NewDecisions(ctx, after, 500)
		if err != nil {
			return added, updated, err
		}
		if len(rows) == 0 {
			break
		}
		if err := uc.repo.InsertShadows(ctx, rows); err != nil {
			return added, updated, err
		}
		added += len(rows)
		after = rows[len(rows)-1].DecisionID
	}
	open, err := uc.repo.OpenShadows(ctx)
	if err != nil {
		return added, updated, err
	}
	closed, err := uc.LastClosed()
	if err != nil {
		return added, updated, err
	}
	for _, s := range open {
		changed, err := uc.fillShadow(ctx, &s, closed)
		if err != nil {
			uc.log.Warnf("shadow %d %s: %v", s.DecisionID, s.Symbol, err)
			continue
		}
		if changed {
			if err := uc.repo.UpdateShadow(ctx, s); err != nil {
				return added, updated, err
			}
			updated++
		}
	}
	return added, updated, nil
}

// refDay 是研判之后第一个开盘的交易日：09:30 前的研判用当日，其余用下一交易日。
func refDay(decided time.Time) (time.Time, error) {
	t := decided.In(tradecal.Shanghai())
	d := backtest.Day(t)
	open, err := tradecal.Default.Open(d)
	if err != nil {
		return time.Time{}, err
	}
	if open && t.Hour()*60+t.Minute() < 9*60+30 {
		return d, nil
	}
	n, err := tradecal.Default.NextOpen(d)
	return backtest.Day(n), err
}

// fillShadow 参考日记为第 1 日；第 N 个交易日停牌时用此前最近的收盘。收益按复权因子折算。
func (uc *Usecase) fillShadow(ctx context.Context, s *Shadow, closed time.Time) (bool, error) {
	ref := s.RefDate
	if ref == nil {
		d, err := refDay(s.DecidedAt)
		if err != nil {
			return false, err
		}
		ref = &d
	}
	if ref.After(closed) {
		return false, nil
	}
	days, err := uc.src.TradingDays(ctx, *ref, closed)
	if err != nil {
		return false, err
	}
	if len(days) > 5 {
		days = days[:5]
	}
	bars, err := uc.repo.DayBars(ctx, s.Symbol, *ref, days[len(days)-1])
	if err != nil {
		return false, err
	}
	if len(bars) == 0 || !bars[0].Day.Equal(*ref) {
		return false, nil
	}
	changed := false
	if s.RefDate == nil {
		s.RefDate, changed = ref, true
	}
	refAdj := adjOf(bars[0])
	if s.RefPrice == nil {
		p := bars[0].Open
		s.RefPrice, changed = &p, true
	}
	retAt := func(n int) *float64 {
		if n > len(days) {
			return nil
		}
		target := days[n-1]
		var last *backtest.DayBar
		for i := range bars {
			if !bars[i].Day.After(target) {
				last = &bars[i]
			}
		}
		if last == nil || *s.RefPrice <= 0 {
			return nil
		}
		r := math.Round((last.Close*adjOf(*last)/(*s.RefPrice*refAdj)-1)*1e6) / 1e6
		return &r
	}
	set := func(dst **float64, n int) {
		if *dst == nil {
			if v := retAt(n); v != nil {
				*dst, changed = v, true
			}
		}
	}
	set(&s.RetT1, 1)
	set(&s.RetT3, 3)
	set(&s.RetT5, 5)
	status := "pending"
	switch {
	case s.RetT5 != nil:
		status = "done"
	case s.RetT1 != nil:
		status = "partial"
	}
	if status != s.Status {
		s.Status, changed = status, true
	}
	return changed, nil
}

func adjOf(b backtest.DayBar) float64 {
	if b.Adj <= 0 {
		return 1
	}
	return b.Adj
}

type HorizonStat struct {
	Horizon   int
	Count     int
	AvgReturn float64
	WinRate   float64
}

type BucketStat struct {
	Bucket string
	Count  int
	Avg    map[int]float64
}

type ShadowStats struct {
	Total     int
	Pending   int
	Horizons  []HorizonStat
	Buckets   []BucketStat
	Monotonic bool
}

var bucketNames = []string{"0-20", "20-40", "40-60", "60-80", "80-100"}

// score100 把 0–1 的置信度换成 0–100。
func score100(v float64) float64 {
	if v <= 1 {
		return v * 100
	}
	return v
}

func (uc *Usecase) ShadowStats(ctx context.Context, f ShadowFilter) (*ShadowStats, error) {
	rows, err := uc.repo.Shadows(ctx, f)
	if err != nil {
		return nil, err
	}
	return shadowStats(rows), nil
}

func shadowStats(rows []Shadow) *ShadowStats {
	out := &ShadowStats{Total: len(rows)}
	type acc struct {
		n    int
		sum  float64
		wins int
	}
	hz := map[int]*acc{}
	bk := make([]map[int]*acc, len(bucketNames))
	bcount := make([]int, len(bucketNames))
	for i := range bk {
		bk[i] = map[int]*acc{}
	}
	for _, r := range rows {
		if r.Status != "done" {
			out.Pending++
		}
		b := -1
		if r.Score != nil {
			b = int(score100(*r.Score) / 20)
			if b > 4 {
				b = 4
			}
			if b < 0 {
				b = 0
			}
			bcount[b]++
		}
		for _, h := range shadowHorizons {
			v := map[int]*float64{1: r.RetT1, 3: r.RetT3, 5: r.RetT5}[h]
			if v == nil {
				continue
			}
			a := hz[h]
			if a == nil {
				a = &acc{}
				hz[h] = a
			}
			a.n++
			a.sum += *v
			if *v > 0 {
				a.wins++
			}
			if b >= 0 {
				ba := bk[b][h]
				if ba == nil {
					ba = &acc{}
					bk[b][h] = ba
				}
				ba.n++
				ba.sum += *v
			}
		}
	}
	for _, h := range shadowHorizons {
		st := HorizonStat{Horizon: h}
		if a := hz[h]; a != nil && a.n > 0 {
			st.Count, st.AvgReturn, st.WinRate = a.n, a.sum/float64(a.n), float64(a.wins)/float64(a.n)
		}
		out.Horizons = append(out.Horizons, st)
	}
	out.Monotonic = true
	prev, seen := 0.0, false
	for i, name := range bucketNames {
		bs := BucketStat{Bucket: name, Count: bcount[i], Avg: map[int]float64{}}
		for _, h := range shadowHorizons {
			if a := bk[i][h]; a != nil && a.n > 0 {
				bs.Avg[h] = a.sum / float64(a.n)
			}
		}
		if a := bk[i][3]; a != nil && a.n > 0 {
			if seen && bs.Avg[3] < prev {
				out.Monotonic = false
			}
			prev, seen = bs.Avg[3], true
		}
		out.Buckets = append(out.Buckets, bs)
	}
	return out
}
