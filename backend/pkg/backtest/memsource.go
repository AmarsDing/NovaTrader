package backtest

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Memory 是内存数据源，用于单元测试和夹具。Days 为空时按有日线的日期当交易日。
type Memory struct {
	Days    []time.Time
	Daily   map[string][]DayBar
	Minute  map[string]map[string][]MinBar // 日期 2006-01-02 → 代码 → 分钟线
	NameMap map[string][]NameSpan
	Lists   map[string]time.Time
	Phase   []PhasePoint
}

func (m *Memory) TradingDays(_ context.Context, from, to time.Time) ([]time.Time, error) {
	days := m.Days
	if len(days) == 0 {
		seen := map[string]time.Time{}
		for _, bars := range m.Daily {
			for _, b := range bars {
				seen[dayKey(b.Day)] = b.Day
			}
		}
		for _, d := range seen {
			days = append(days, d)
		}
		sort.Slice(days, func(i, j int) bool { return days[i].Before(days[j]) })
	}
	var out []time.Time
	for _, d := range days {
		if !d.Before(from) && !d.After(to) {
			out = append(out, d)
		}
	}
	return out, nil
}

func (m *Memory) DailyBars(_ context.Context, from, to time.Time) (map[string][]DayBar, error) {
	out := map[string][]DayBar{}
	for sym, bars := range m.Daily {
		for _, b := range bars {
			if !b.Day.Before(from) && !b.Day.After(to) {
				out[sym] = append(out[sym], b)
			}
		}
	}
	return out, nil
}

func (m *Memory) MinuteBars(_ context.Context, day time.Time, symbols []string) (map[string][]MinBar, error) {
	out := map[string][]MinBar{}
	byDay := m.Minute[dayKey(day)]
	for _, s := range symbols {
		if bars := byDay[s]; len(bars) > 0 {
			out[s] = bars
		}
	}
	return out, nil
}

func (m *Memory) Names(context.Context) (map[string][]NameSpan, error) {
	if m.NameMap == nil {
		return map[string][]NameSpan{}, nil
	}
	return m.NameMap, nil
}

func (m *Memory) ListDates(context.Context) (map[string]time.Time, error) {
	if m.Lists == nil {
		return map[string]time.Time{}, nil
	}
	return m.Lists, nil
}

func (m *Memory) Phases(_ context.Context, from, to time.Time) ([]PhasePoint, error) {
	var out []PhasePoint
	for _, p := range m.Phase {
		if !p.AsOf.Before(from) && !p.AsOf.After(to) {
			out = append(out, p)
		}
	}
	return out, nil
}

// Cache 一次读入整段日线、日历、名称史和情绪，分钟线按需读取后留在内存。参数搜索的多次运行共用一份，可并发使用。
type Cache struct {
	inner  Source
	from   time.Time
	to     time.Time
	days   []time.Time
	daily  map[string][]DayBar
	names  map[string][]NameSpan
	lists  map[string]time.Time
	phases []PhasePoint

	mu  sync.Mutex
	min map[string]map[string][]MinBar
	got map[string]map[string]bool
}

// NewCache 读入 cfg 区间（含回看期）的数据。cfg 必须已 Normalize。
func NewCache(ctx context.Context, src Source, cfg Config) (*Cache, error) {
	start, err := ParseDay(cfg.Start)
	if err != nil {
		return nil, err
	}
	end, err := ParseDay(cfg.End)
	if err != nil {
		return nil, err
	}
	c := &Cache{inner: src, from: start.AddDate(0, 0, -(cfg.HistoryBars*3/2 + 10)), to: end,
		min: map[string]map[string][]MinBar{}, got: map[string]map[string]bool{}}
	if c.days, err = src.TradingDays(ctx, start, end); err != nil {
		return nil, err
	}
	if c.daily, err = src.DailyBars(ctx, c.from, end); err != nil {
		return nil, err
	}
	if c.names, err = src.Names(ctx); err != nil {
		return nil, err
	}
	if c.lists, err = src.ListDates(ctx); err != nil {
		return nil, err
	}
	if c.phases, err = src.Phases(ctx, c.from, end.AddDate(0, 0, 1)); err != nil {
		return nil, err
	}
	return c, nil
}

// Days 是缓存区间内的交易日。
func (c *Cache) Days() []time.Time { return c.days }

func (c *Cache) TradingDays(_ context.Context, from, to time.Time) ([]time.Time, error) {
	if len(c.days) == 0 {
		return nil, nil
	}
	if from.Before(c.days[0]) || to.After(c.to) {
		return nil, fmt.Errorf("backtest: cache covers %s..%s, asked %s..%s", dayKey(c.days[0]), dayKey(c.to), dayKey(from), dayKey(to))
	}
	var out []time.Time
	for _, d := range c.days {
		if !d.Before(from) && !d.After(to) {
			out = append(out, d)
		}
	}
	return out, nil
}

func (c *Cache) DailyBars(_ context.Context, from, to time.Time) (map[string][]DayBar, error) {
	out := make(map[string][]DayBar, len(c.daily))
	for sym, bars := range c.daily {
		i := sort.Search(len(bars), func(i int) bool { return !bars[i].Day.Before(from) })
		j := sort.Search(len(bars), func(j int) bool { return bars[j].Day.After(to) })
		if i < j {
			out[sym] = bars[i:j:j]
		}
	}
	return out, nil
}

func (c *Cache) MinuteBars(ctx context.Context, day time.Time, symbols []string) (map[string][]MinBar, error) {
	key := dayKey(day)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.min[key] == nil {
		c.min[key], c.got[key] = map[string][]MinBar{}, map[string]bool{}
	}
	var miss []string
	for _, s := range symbols {
		if !c.got[key][s] {
			miss = append(miss, s)
		}
	}
	if len(miss) > 0 {
		got, err := c.inner.MinuteBars(ctx, day, miss)
		if err != nil {
			return nil, err
		}
		for _, s := range miss {
			c.got[key][s] = true
			if bars := got[s]; len(bars) > 0 {
				c.min[key][s] = bars
			}
		}
	}
	out := make(map[string][]MinBar, len(symbols))
	for _, s := range symbols {
		if bars := c.min[key][s]; len(bars) > 0 {
			out[s] = bars
		}
	}
	return out, nil
}

func (c *Cache) Names(context.Context) (map[string][]NameSpan, error)    { return c.names, nil }
func (c *Cache) ListDates(context.Context) (map[string]time.Time, error) { return c.lists, nil }

func (c *Cache) Phases(_ context.Context, from, to time.Time) ([]PhasePoint, error) {
	var out []PhasePoint
	for _, p := range c.phases {
		if !p.AsOf.Before(from) && !p.AsOf.After(to) {
			out = append(out, p)
		}
	}
	return out, nil
}
