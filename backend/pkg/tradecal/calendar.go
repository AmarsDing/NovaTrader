// Package tradecal 是全系统唯一的交易日和时段判断。
// 其他服务不要自己用时钟猜「现在是不是盘中」。
package tradecal

import (
	"fmt"
	"time"
)

// 放假与调休来自国务院办公厅通知：
// 2025 年：国办发明电〔2024〕12号；2026 年：国办发明电〔2025〕7号。
// 未公布放假安排的年份直接报错，避免把节假日当成交易日。
// 调休上班的周六周日交易所照常休市，只在 Note 里留说明。

var shanghai *time.Location

func location() *time.Location {
	if shanghai == nil {
		loc, err := time.LoadLocation("Asia/Shanghai")
		if err != nil {
			panic(err)
		}
		shanghai = loc
	}
	return shanghai
}

// Shanghai 返回上海时区。
func Shanghai() *time.Location { return location() }

type dayMark struct {
	open bool
	note string
}

// Calendar 保存已公布年份的休市和调休上班日。
type Calendar struct {
	years  map[int]struct{}
	closed map[string]string
	makeup map[string]string
}

// Default 是内置的 2025–2026 年日历。
var Default = newCalendar()

func newCalendar() *Calendar {
	location()
	c := &Calendar{
		years:  map[int]struct{}{2025: {}, 2026: {}},
		closed: map[string]string{},
		makeup: map[string]string{},
	}
	c.closeRange(2025, "2025-01-01", "2025-01-01", "元旦")
	c.closeRange(2025, "2025-01-28", "2025-02-04", "春节")
	c.closeRange(2025, "2025-04-04", "2025-04-06", "清明")
	c.closeRange(2025, "2025-05-01", "2025-05-05", "劳动节")
	c.closeRange(2025, "2025-05-31", "2025-06-02", "端午")
	c.closeRange(2025, "2025-10-01", "2025-10-08", "国庆中秋")
	c.work("2025-01-26", "春节调休上班")
	c.work("2025-02-08", "春节调休上班")
	c.work("2025-04-27", "劳动节调休上班")
	c.work("2025-09-28", "国庆调休上班")
	c.work("2025-10-11", "国庆调休上班")

	c.closeRange(2026, "2026-01-01", "2026-01-03", "元旦")
	c.closeRange(2026, "2026-02-15", "2026-02-23", "春节")
	c.closeRange(2026, "2026-04-04", "2026-04-06", "清明")
	c.closeRange(2026, "2026-05-01", "2026-05-05", "劳动节")
	c.closeRange(2026, "2026-06-19", "2026-06-21", "端午")
	c.closeRange(2026, "2026-09-25", "2026-09-27", "中秋")
	c.closeRange(2026, "2026-10-01", "2026-10-07", "国庆")
	c.work("2026-01-04", "元旦调休上班")
	c.work("2026-02-14", "春节调休上班")
	c.work("2026-02-28", "春节调休上班")
	c.work("2026-05-09", "劳动节调休上班")
	c.work("2026-09-20", "国庆调休上班")
	c.work("2026-10-10", "国庆调休上班")
	return c
}

func (c *Calendar) closeRange(year int, start, end, note string) {
	c.years[year] = struct{}{}
	from := mustDate(start)
	to := mustDate(end)
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		c.closed[d.Format("2006-01-02")] = note
	}
}

func (c *Calendar) work(day, note string) {
	c.makeup[day] = note
	delete(c.closed, day)
}

func mustDate(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02", s, location())
	if err != nil {
		panic(err)
	}
	return t
}

// Open 判断这一天是否开市。年份没有放假表时返回错误。
func (c *Calendar) Open(t time.Time) (bool, error) {
	d := t.In(location())
	if _, ok := c.years[d.Year()]; !ok {
		return false, fmt.Errorf("tradecal: no holiday table for %d", d.Year())
	}
	key := d.Format("2006-01-02")
	if _, closed := c.closed[key]; closed {
		return false, nil
	}
	if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
		return false, nil
	}
	return true, nil
}

// Note 返回休市或调休说明。普通交易日为空。
func (c *Calendar) Note(t time.Time) string {
	key := t.In(location()).Format("2006-01-02")
	if n, ok := c.closed[key]; ok {
		return n
	}
	if n, ok := c.makeup[key]; ok {
		return n
	}
	return ""
}

// NextOpen 返回严格晚于 t 所在日期的下一个交易日。
func (c *Calendar) NextOpen(t time.Time) (time.Time, error) {
	d := date(t)
	for i := 0; i < 40; i++ {
		d = d.AddDate(0, 0, 1)
		open, err := c.Open(d)
		if err != nil {
			return time.Time{}, err
		}
		if open {
			return d, nil
		}
	}
	return time.Time{}, fmt.Errorf("tradecal: no open day within 40 days of %s", t.In(location()).Format("2006-01-02"))
}

// PrevOpen 返回严格早于 t 所在日期的上一个交易日。
func (c *Calendar) PrevOpen(t time.Time) (time.Time, error) {
	d := date(t)
	for i := 0; i < 40; i++ {
		d = d.AddDate(0, 0, -1)
		open, err := c.Open(d)
		if err != nil {
			return time.Time{}, err
		}
		if open {
			return d, nil
		}
	}
	return time.Time{}, fmt.Errorf("tradecal: no open day within 40 days before %s", t.In(location()).Format("2006-01-02"))
}

// LastClosedDay 返回最近一个已收盘的交易日：交易日 15:30 之后为当天，否则为上一个交易日。
// 盘后数据是否最新以它为准。
func (c *Calendar) LastClosedDay(t time.Time) (time.Time, error) {
	open, err := c.Open(t)
	if err != nil {
		return time.Time{}, err
	}
	d := t.In(location())
	if open && d.Hour()*60+d.Minute() >= 15*60+30 {
		return date(t), nil
	}
	return c.PrevOpen(t)
}

func date(t time.Time) time.Time {
	d := t.In(location())
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, location())
}
