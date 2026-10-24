package data

import (
	"context"
	"fmt"
	"sync"
	"time"

	"server/app/strategy/internal/biz"
	"server/ent"
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
	symbolFilter = 1000 // 代码数超过这个就不加 IN 条件，直接按时间取全市场
)

// marketSource 用库里的证券字典、market 服务日线、Redis 最新快照拼出 rules.Snapshot。
// 只读 asOf 之前的数据；asOf 不是今天时不读 Redis，避免把现在的价格混进过去的快照。
//
// M13-T01：行情数据归 M01/M02 所有，日线经 market(2021) 只读 HTTP 按代码取，
// 本服务不再直读 market_data 表（横切越权清理）。情绪与利空也在 biz 层改成
// SentimentSource / NewsSource 端口，本结构不再回填 Stage / NegativeNews——
// 未挂上阶段时 Stage 为空，融合系数按 1.0 且显式标记，恒 WARM 的编造口径已去除。
type marketSource struct {
	d        *Data
	bars     *marketClient
	log      *log.Helper
	barCache dayBarsCache
}

// NewMarketSource 装配行情源。bars 为 nil 时日线查询显式失败（不编数）。
func NewMarketSource(d *Data, bars *marketClient, logger log.Logger) biz.MarketSource {
	return &marketSource{d: d, bars: bars, log: log.NewHelper(log.With(logger, "module", "strategy/market"))}
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
	out := make([]rules.Snapshot, 0, len(basics))
	for _, b := range basics {
		s := rules.Snapshot{
			Symbol: b.StockCode, Name: b.StockName, ST: b.StFlag, Suspended: b.SuspendFlag,
			AsOf: asOf, Bars: bars[b.StockCode], Today: today[b.StockCode],
		}
		if b.ListDate != nil {
			s.ListDate = localDate(*b.ListDate)
		}
		out = append(out, s)
	}
	return out, nil
}

// dailyBars 取 asOf 当天之前的日线。小批量（≤symbolFilter）批量直拉、不走缓存；
// 全市场调研（>symbolFilter）结果按交易日缓存最多两天——陈腐窗口与失效口径见
// dayBarsCache 注释，记录见 M06 开发进度。取数失败整体显式报错，不缺口交付。
func (m *marketSource) dailyBars(ctx context.Context, codes []string, asOf time.Time) (map[string][]rules.Bar, error) {
	day := dayStart(asOf)
	if len(codes) > symbolFilter {
		key := day.Unix()
		if cached, ok := m.barCache.get(key); ok {
			return cached, nil
		}
		bars, err := m.fetchBars(ctx, codes, day)
		if err != nil {
			return nil, err
		}
		m.barCache.put(key, bars)
		// 与命中路径同一份浅拷贝口径交付。不回头 get——put/get 是两次独立加锁，
		// 若 key 恰被第三日并发 put 挤出 keep-2 窗口，回读会把 nil 静默交付成空仓。
		return cloneDayBars(bars), nil
	}
	return m.fetchBars(ctx, codes, day)
}

// fetchBars 经 market HTTP 批量拉日线（M13-T01：替代旧直读 market_data；
// W2 留账收敛：批量 POST 分包替代按代码 HTTP conc 8 读放大）。批量端任一非 2xx
// 整体错误、不降级逐码重试；窗口内无行的码在结果中缺席（缺席即缺数，与旧口径同）。
func (m *marketSource) fetchBars(ctx context.Context, codes []string, day time.Time) (map[string][]rules.Bar, error) {
	if m.bars == nil {
		return nil, fmt.Errorf("daily bars: %w", errNoMarketAddr)
	}
	return m.bars.dailyBarsBatch(ctx, codes, day)
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

// DailyCloses 按 from 前最后一根日线的复权因子折算，除权日落在区间内也不会算出假涨跌。
// 日线经 market HTTP（M13-T01），原来直读 market_data 的口径不变。
func (m *marketSource) DailyCloses(ctx context.Context, symbol string, from, to time.Time) (map[string]float64, error) {
	rows, err := m.bars.closesRaw(ctx, symbol, from, to)
	if err != nil {
		return nil, err
	}
	base := 0.0
	lo, hi := dayStart(from), dayStart(to)
	out := make(map[string]float64)
	for _, r := range rows {
		t, err := time.Parse(time.RFC3339, r.GetTime())
		if err != nil {
			return nil, fmt.Errorf("market bars %s time %q: %w", symbol, r.GetTime(), err)
		}
		day := dayStart(t)
		if day.Before(lo) {
			base = r.GetAdjFactor()
			continue
		}
		if day.After(hi) {
			continue
		}
		px := r.GetClose()
		if base > 0 && r.GetAdjFactor() > 0 {
			px *= r.GetAdjFactor() / base
		}
		out[day.Format("2006-01-02")] = px
	}
	return out, nil
}
