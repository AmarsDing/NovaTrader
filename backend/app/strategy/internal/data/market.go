package data

import (
	"context"
	"fmt"
	"sync"
	"time"

	"server/app/strategy/internal/biz"
	"server/ent"
	"server/ent/marketdata"
	"server/ent/marketsentiment"
	"server/ent/newssentiment"
	"server/ent/stockbasic"
	"server/pkg/market"
	"server/pkg/rules"
	"server/pkg/tradecal"

	"entgo.io/ent/dialect/sql"
	"github.com/go-kratos/kratos/v2/log"
)

const (
	historyDays  = 120 // 往前取多少个自然日的日线，约 80 个交易日
	keepBars     = 60
	snapMaxAge   = 2 * time.Minute
	stageMaxAge  = 7 * 24 * time.Hour
	badNewsLevel = 4    // 重要度达到即算突发利空
	badNewsScore = -0.5 // 情感不高于此值
	badNewsHours = 24
	symbolFilter = 1000 // 代码数超过这个就不加 IN 条件，直接按时间取全市场
)

// marketSource 用库里的日线、Redis 最新快照、情绪截面和情报拼出 rules.Snapshot。
// 只读 asOf 之前的数据；asOf 不是今天时不读 Redis，避免把现在的价格混进过去的快照。
type marketSource struct {
	d        *Data
	log      *log.Helper
	barCache dayBarsCache
}

func NewMarketSource(d *Data, logger log.Logger) biz.MarketSource {
	return &marketSource{d: d, log: log.NewHelper(log.With(logger, "module", "strategy/market"))}
}

// dayBarsCache 按交易日缓存全市场日线。已收盘日线盘内视为不可变：盘后自动采集
// （16:00 日线/16:20 复权）只写当日、查询窗口不含当日；人工 Backfill/Collect RPC
// 盘中同样可能改写更早日线，这类纠正也要换天才被本键感知——陈腐窗口最长一个
// 交易日，重启 strategy 进程即刻失效，口径与取舍记录见 M06 开发进度。
// 最多留两个交易日（今天/昨天），扫描热路径每天只查一次库。
// 读取一律浅拷贝交付 map——pkg/rules 全部消费方只读 Bars 已逐文件核实，
// 切片本体共享不复制；钉死在 market_cache_test.go。
// 串行化约定：当前所有触缓存路径都在 biz 的 scanMu 下（scan/exit 扫描）；
// 未命中路径 put 后不回读缓存（直接克隆手上的 map），key 即便被第三日并发
// put 挤出 keep-2 窗口也不会把 nil 静默交付成空仓。
type dayBarsCache struct {
	mu   sync.Mutex
	days []int64 // 旧 → 新，最多 2 个
	bars map[int64]map[string][]rules.Bar
}

func (c *dayBarsCache) get(day int64) (map[string][]rules.Bar, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m, ok := c.bars[day]
	if !ok {
		return nil, false
	}
	return cloneDayBars(m), true
}

// cloneDayBars 浅拷贝一层 map；深层的 []rules.Bar 按只读合同共享。
func cloneDayBars(m map[string][]rules.Bar) map[string][]rules.Bar {
	out := make(map[string][]rules.Bar, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (c *dayBarsCache) put(day int64, m map[string][]rules.Bar) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.bars == nil {
		c.bars = map[int64]map[string][]rules.Bar{}
	}
	if _, ok := c.bars[day]; !ok {
		c.days = append(c.days, day)
		for len(c.days) > 2 {
			delete(c.bars, c.days[0])
			c.days = c.days[1:]
		}
	}
	c.bars[day] = m
}

func dayStart(t time.Time) time.Time {
	d := t.In(tradecal.Shanghai())
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, tradecal.Shanghai())
}

// localDate 把 date 列读回的 UTC 零点换成同一日期的上海零点。
func localDate(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, tradecal.Shanghai())
}

func listed(asOf time.Time) func(*sql.Selector) {
	return stockbasic.Or(stockbasic.DelistDateIsNil(), stockbasic.DelistDateGT(asOf))
}

func (m *marketSource) Universe(ctx context.Context, asOf time.Time) ([]rules.Snapshot, error) {
	basics, err := m.d.client.StockBasic.Query().Where(listed(asOf)).All(ctx)
	if err != nil {
		return nil, err
	}
	return m.build(ctx, basics, asOf)
}

func (m *marketSource) Snapshots(ctx context.Context, symbols []string, asOf time.Time) ([]rules.Snapshot, error) {
	if len(symbols) == 0 {
		return nil, nil
	}
	basics, err := m.d.client.StockBasic.Query().Where(stockbasic.StockCodeIn(symbols...), listed(asOf)).All(ctx)
	if err != nil {
		return nil, err
	}
	return m.build(ctx, basics, asOf)
}

func (m *marketSource) build(ctx context.Context, basics []*ent.StockBasic, asOf time.Time) ([]rules.Snapshot, error) {
	if len(basics) == 0 {
		return nil, nil
	}
	codes := make([]string, len(basics))
	for i, b := range basics {
		codes[i] = b.StockCode
	}
	bars, err := m.dailyBars(ctx, codes, asOf)
	if err != nil {
		return nil, err
	}
	today := m.todayBars(ctx, codes, asOf)
	stage := m.stage(ctx, asOf)
	bad, err := m.badNews(ctx, codes, asOf)
	if err != nil {
		return nil, err
	}
	out := make([]rules.Snapshot, 0, len(basics))
	for _, b := range basics {
		s := rules.Snapshot{
			Symbol: b.StockCode, Name: b.StockName, ST: b.StFlag, Suspended: b.SuspendFlag,
			AsOf: asOf, Bars: bars[b.StockCode], Today: today[b.StockCode], Stage: stage,
			NegativeNews: bad[b.StockCode],
		}
		if b.ListDate != nil {
			s.ListDate = localDate(*b.ListDate)
		}
		out = append(out, s)
	}
	return out, nil
}

// dailyBars 取 asOf 当天之前的日线。小批量（≤symbolFilter）按代码 IN 查询、每次集合
// 不同不走缓存；全市场调研（>symbolFilter）省略 IN、结果与代码集无关，按交易日缓存
// 最多两天——陈腐窗口与失效口径见 dayBarsCache 注释，记录见 M06 开发进度。
func (m *marketSource) dailyBars(ctx context.Context, codes []string, asOf time.Time) (map[string][]rules.Bar, error) {
	day := dayStart(asOf)
	if len(codes) > symbolFilter {
		key := day.Unix()
		if cached, ok := m.barCache.get(key); ok {
			return cached, nil
		}
		bars, err := m.queryDailyBars(ctx, day, nil)
		if err != nil {
			return nil, err
		}
		m.barCache.put(key, bars)
		// 与命中路径同一份浅拷贝口径交付。不回头 get——put/get 是两次独立加锁，
		// 若 key 恰被第三日并发 put 挤出 keep-2 窗口，回读会把 nil 静默交付成空仓。
		return cloneDayBars(bars), nil
	}
	return m.queryDailyBars(ctx, day, codes)
}

// queryDailyBars 查 day 之前的日线并按复权因子换成以最后一根为基准的前复权价。
// codes 为空（nil）时省略 IN 条件、按时间取全市场。
func (m *marketSource) queryDailyBars(ctx context.Context, day time.Time, codes []string) (map[string][]rules.Bar, error) {
	q := m.d.client.MarketData.Query().Where(
		marketdata.Freq("1d"),
		marketdata.BarTimeGTE(day.AddDate(0, 0, -historyDays)),
		marketdata.BarTimeLT(day),
	)
	if len(codes) > 0 {
		q = q.Where(marketdata.SymbolIn(codes...))
	}
	rows, err := q.Order(marketdata.BySymbol(), marketdata.ByBarTime()).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("daily bars: %w", err)
	}
	raw := map[string][]*ent.MarketData{}
	for _, r := range rows {
		if r.Close == nil || r.Open == nil || r.High == nil || r.Low == nil {
			continue
		}
		raw[r.Symbol] = append(raw[r.Symbol], r)
	}
	out := make(map[string][]rules.Bar, len(raw))
	for sym, list := range raw {
		if len(list) > keepBars {
			list = list[len(list)-keepBars:]
		}
		base := list[len(list)-1].AdjFactor
		if base <= 0 {
			base = 1
		}
		bars := make([]rules.Bar, len(list))
		for i, r := range list {
			k := 1.0
			if r.AdjFactor > 0 {
				k = r.AdjFactor / base
			}
			b := rules.Bar{
				Time: r.BarTime, Open: *r.Open * k, High: *r.High * k, Low: *r.Low * k, Close: *r.Close * k,
			}
			if r.Volume != nil {
				b.Volume = *r.Volume
			}
			if r.Amount != nil {
				b.Amount = *r.Amount
			}
			bars[i] = b
		}
		out[sym] = bars
	}
	return out, nil
}

// todayBars 读 Redis 最新快照。只在 asOf 与快照同一天、快照不晚于 asOf 且未过期时使用。
func (m *marketSource) todayBars(ctx context.Context, codes []string, asOf time.Time) map[string]*rules.Bar {
	out := map[string]*rules.Bar{}
	if m.d.Redis == nil || !dayStart(asOf).Equal(dayStart(time.Now())) {
		return out
	}
	vals, err := m.d.Redis.HMGet(ctx, market.SnapshotKey, codes...).Result()
	if err != nil {
		m.log.Warnf("redis snapshots: %v", err)
		return out
	}
	for i, v := range vals {
		raw, ok := v.(string)
		if !ok {
			continue
		}
		s, err := market.ParseSnapshot([]byte(raw))
		if err != nil || s.Expired(asOf, snapMaxAge) || s.Time.After(asOf) || !dayStart(s.Time).Equal(dayStart(asOf)) {
			continue
		}
		if s.Last <= 0 || s.Open <= 0 {
			continue
		}
		out[codes[i]] = &rules.Bar{
			Time: s.Time, Open: s.Open, High: s.High, Low: s.Low, Close: s.Last,
			Volume: s.Volume, Amount: s.Amount,
		}
	}
	return out
}

// stage 取 asOf 之前最近一次未过期的情绪截面。没有时按 WARM。
func (m *marketSource) stage(ctx context.Context, asOf time.Time) rules.Stage {
	row, err := m.d.client.MarketSentiment.Query().
		Where(marketsentiment.AsOfLTE(asOf), marketsentiment.AsOfGT(asOf.Add(-stageMaxAge)), marketsentiment.Stale(false)).
		Order(marketsentiment.ByAsOf(sql.OrderDesc())).
		First(ctx)
	if err != nil {
		if !ent.IsNotFound(err) {
			m.log.Warnf("market sentiment: %v", err)
		}
		return rules.StageWarm
	}
	return rules.Stage(row.Phase)
}

func (m *marketSource) badNews(ctx context.Context, codes []string, asOf time.Time) (map[string]bool, error) {
	q := m.d.client.NewsSentiment.Query().Where(
		newssentiment.PublishTimeGTE(asOf.Add(-badNewsHours*time.Hour)),
		newssentiment.PublishTimeLTE(asOf),
		newssentiment.ImportanceGTE(badNewsLevel),
		newssentiment.SentimentScoreLTE(badNewsScore),
		newssentiment.StockCodeNotNil(),
	)
	if len(codes) <= symbolFilter {
		q = q.Where(newssentiment.StockCodeIn(codes...))
	}
	hit, err := q.Unique(true).Select(newssentiment.FieldStockCode).Strings(ctx)
	if err != nil {
		return nil, fmt.Errorf("bad news: %w", err)
	}
	out := make(map[string]bool, len(hit))
	for _, c := range hit {
		out[c] = true
	}
	return out, nil
}

// DailyCloses 按 from 前最后一根日线的复权因子折算，除权日落在区间内也不会算出假涨跌。
func (m *marketSource) DailyCloses(ctx context.Context, symbol string, from, to time.Time) (map[string]float64, error) {
	rows, err := m.d.client.MarketData.Query().
		Where(marketdata.Symbol(symbol), marketdata.Freq("1d"),
			marketdata.BarTimeGTE(dayStart(from)), marketdata.BarTimeLT(dayStart(to).AddDate(0, 0, 1))).
		All(ctx)
	if err != nil {
		return nil, err
	}
	base := 0.0
	prev, err := m.d.client.MarketData.Query().
		Where(marketdata.Symbol(symbol), marketdata.Freq("1d"), marketdata.BarTimeLT(dayStart(from))).
		Order(marketdata.ByBarTime(sql.OrderDesc())).
		First(ctx)
	switch {
	case err == nil:
		base = prev.AdjFactor
	case !ent.IsNotFound(err):
		return nil, err
	}
	out := make(map[string]float64, len(rows))
	for _, r := range rows {
		if r.Close == nil {
			continue
		}
		px := *r.Close
		if base > 0 && r.AdjFactor > 0 {
			px *= r.AdjFactor / base
		}
		out[dayStart(r.BarTime).Format("2006-01-02")] = px
	}
	return out, nil
}
