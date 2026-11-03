package biz

import (
	"context"
	"sort"
	"time"
)

/* ---------- BE-02 信号状态变迁历史 ---------- */

// SignalEvent 是 signal_events 的一行：一次状态变迁（From 为空表示创建事件）。
type SignalEvent struct {
	ID       int
	SignalID int
	From     string
	To       string
	Reason   string
	Actor    string
	TraceID  string
	At       time.Time
}

// SignalEvents 返回信号的状态变迁流水。未知信号返回 ErrNotFound（子资源随主资源 404）。
func (uc *Usecase) SignalEvents(ctx context.Context, id int) ([]SignalEvent, error) {
	if _, err := uc.signals.Get(ctx, id); err != nil {
		return nil, err
	}
	return uc.signals.Events(ctx, id)
}

/* ---------- BE-12 miss_reason 漏斗按日聚合 ---------- */

const (
	missFunnelDefaultDays = 7
	missFunnelMaxDays     = 30
)

// MissFunnelDayRow 是一天的漏斗聚合：候选总数、落选数与 miss_reason 分布。
type MissFunnelDayRow struct {
	Day     time.Time
	Total   int
	Missed  int
	Reasons map[string]int
}

// MissFunnel 聚合近 days 个日历日（含今天，上海时区）的落选漏斗。
// days 非正取默认 7，超上限截到 30；结果按日期升序，无候选的日期缺席（不补零）。
func (uc *Usecase) MissFunnel(ctx context.Context, days int, pool string) ([]MissFunnelDayRow, error) {
	if days <= 0 {
		days = missFunnelDefaultDays
	}
	if days > missFunnelMaxDays {
		days = missFunnelMaxDays
	}
	to := tradeDay(time.Now())
	from := to.AddDate(0, 0, -(days - 1))
	rows, err := uc.candidates.MissFunnel(ctx, from, to, pool)
	if err != nil {
		return nil, err
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Day.Before(rows[j].Day) })
	return rows, nil
}
