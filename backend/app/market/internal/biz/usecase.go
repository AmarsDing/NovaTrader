package biz

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"server/pkg/events"
	"server/pkg/market"
	"server/pkg/metrics"
	"server/pkg/tradecal"

	"github.com/go-kratos/kratos/v2/log"
)

var (
	mRounds  = metrics.Counter("novatrader_market_rounds", "snapshot rounds processed")
	mExpired = metrics.Counter("novatrader_market_expired_snapshots", "snapshots skipped because as_of is too old")
	mBars    = metrics.Counter("novatrader_market_bars_saved", "1m bars written")
	mErrors  = metrics.Counter("novatrader_market_errors", "refresh or job errors")
)

// heatTTL 是实时板块热度的缓存时长。
const heatTTL = 10 * time.Second

// Usecase 持有当天的引擎，并负责所有读写和事件。
type Usecase struct {
	repo  Repo
	snaps SnapshotSource
	pub   Publisher
	cfg   Config
	cal   *tradecal.Calendar
	log   *log.Helper
	watch *Watchlist

	refreshMu sync.Mutex

	mu        sync.RWMutex
	engine    *market.Engine
	adj       map[string]float64
	members   map[string][]string
	names     map[string]string
	impulse   map[string]float64
	impulseOn time.Time
	weights   market.SectorWeights
	heat      []market.SectorHeat
	heatAt    time.Time
}

func NewUsecase(repo Repo, snaps SnapshotSource, pub Publisher, cfg Config, logger log.Logger) *Usecase {
	return &Usecase{
		repo: repo, snaps: snaps, pub: pub, cfg: cfg,
		cal:     tradecal.Default,
		log:     log.NewHelper(logger),
		watch:   NewWatchlist(),
		adj:     map[string]float64{},
		impulse: map[string]float64{},
		weights: market.DefaultSectorWeights(),
	}
}

// Engine 返回当前引擎，未预热时为 nil。
func (u *Usecase) Engine() *market.Engine {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.engine
}

// Watch 返回候选池。
func (u *Usecase) Watch() *Watchlist { return u.watch }

// EnsureDefaults 补上席位标签和外围映射的默认行。
func (u *Usecase) EnsureDefaults(ctx context.Context) error {
	return u.repo.EnsureDefaults(ctx)
}

// Warmup 为 now 所在交易日建引擎：载入日线、昨日连板、昨日情绪阶段、板块成分和阈值配置。非交易日不动现有引擎。
func (u *Usecase) Warmup(ctx context.Context, now time.Time) error {
	day := market.DateOf(now)
	open, err := u.cal.Open(day)
	if err != nil || !open {
		return err
	}
	prev, err := u.cal.PrevOpen(day)
	if err != nil {
		return err
	}
	stocks, err := u.repo.LoadStocks(ctx)
	if err != nil {
		return fmt.Errorf("warmup: stocks: %w", err)
	}
	hist, err := u.repo.DailyHistory(ctx, day, u.cfg.WarmupDays)
	if err != nil {
		return fmt.Errorf("warmup: history: %w", err)
	}
	sealed, err := u.repo.SealedOn(ctx, prev)
	if err != nil {
		return fmt.Errorf("warmup: prev boards: %w", err)
	}
	phase, err := u.repo.ClosePhase(ctx, prev)
	if err != nil {
		return fmt.Errorf("warmup: prev phase: %w", err)
	}
	rules, weights := u.loadParams(ctx)
	members, names, err := u.repo.SectorMembers(ctx, day)
	if err != nil {
		return fmt.Errorf("warmup: sectors: %w", err)
	}
	eng := market.NewEngine(day, market.EngineConfig{
		StaleAfter: u.cfg.StaleAfter, BarGrace: u.cfg.BarGrace,
		Rules: rules, PrevPhase: phase, Calendar: u.cal,
	})
	adj := make(map[string]float64, len(stocks))
	for _, info := range stocks {
		bars := hist[info.Symbol]
		if n := len(bars); n > 0 {
			last := bars[n-1]
			info.Adj = last.Adj
			info.PrevAmount = last.Amount
			var sum float64
			k := 0
			for i := n - 1; i >= 0 && k < 5; i-- {
				sum += float64(bars[i].Volume)
				k++
			}
			info.MA5Volume = sum / float64(k)
		}
		info.PrevConsecutive = sealed[info.Symbol]
		eng.Load(info, bars)
		adj[info.Symbol] = info.Adj
		if adj[info.Symbol] <= 0 {
			adj[info.Symbol] = 1
		}
	}
	u.mu.Lock()
	u.engine, u.adj, u.members, u.names, u.weights = eng, adj, members, names, weights
	u.heat, u.heatAt = nil, time.Time{}
	if !sameDate(u.impulseOn, day) {
		u.impulse = map[string]float64{}
	}
	u.mu.Unlock()
	u.log.Infof("warmup %s: %d stocks, %d sectors, prev phase %q", day.Format("2006-01-02"), len(stocks), len(members), phase)
	return nil
}

func sortHeat(rows []market.SectorHeat) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Heat != rows[j].Heat {
			return rows[i].Heat > rows[j].Heat
		}
		return rows[i].SectorCode < rows[j].SectorCode
	})
	for i := range rows {
		rows[i].Rank = i + 1
	}
}

func (u *Usecase) loadParams(ctx context.Context) (market.Rules, market.SectorWeights) {
	raw, err := u.repo.Config(ctx, market.RulesKey)
	if err != nil {
		u.log.Warnf("read %s: %v, using defaults", market.RulesKey, err)
	}
	rules, err := market.ParseRules(raw)
	if err != nil {
		u.log.Warnf("%v, using defaults", err)
	}
	raw, err = u.repo.Config(ctx, market.SectorWeightsKey)
	if err != nil {
		u.log.Warnf("read %s: %v, using defaults", market.SectorWeightsKey, err)
	}
	weights, err := market.ParseSectorWeights(raw)
	if err != nil {
		u.log.Warnf("%v, using defaults", err)
	}
	return rules, weights
}

func sameDate(a, b time.Time) bool {
	return !a.IsZero() && !b.IsZero() && market.DateOf(a).Equal(market.DateOf(b))
}

// ensureEngine 在交易日里保证引擎属于今天。
func (u *Usecase) ensureEngine(ctx context.Context, now time.Time) (*market.Engine, error) {
	eng := u.Engine()
	if eng != nil && sameDate(eng.Day(), now) {
		return eng, nil
	}
	if err := u.Warmup(ctx, now); err != nil {
		return nil, err
	}
	eng = u.Engine()
	if eng == nil || !sameDate(eng.Day(), now) {
		return nil, nil
	}
	return eng, nil
}

// Refresh 读一轮快照并处理：聚合、涨停、指标预览；封口分钟线落库；发事件。
func (u *Usecase) Refresh(ctx context.Context, now time.Time) error {
	u.refreshMu.Lock()
	defer u.refreshMu.Unlock()
	eng, err := u.ensureEngine(ctx, now)
	if err != nil || eng == nil {
		return err
	}
	snaps, err := u.snaps.All(ctx)
	if err != nil {
		mErrors.Inc()
		return fmt.Errorf("refresh: snapshots: %w", err)
	}
	up := eng.Process(snaps, now)
	mRounds.Inc()
	for i := 0; i < up.Expired; i++ {
		mExpired.Inc()
	}
	bars := append(up.Bars, eng.Flush(now)...)
	if len(bars) > 0 {
		if err := u.repo.SaveMinuteBars(ctx, bars, u.adjMap()); err != nil {
			mErrors.Inc()
			u.log.Errorf("save 1m bars: %v", err)
		} else {
			for range bars {
				mBars.Inc()
			}
		}
	}
	if len(up.Limits) > 0 {
		if err := u.saveChangedBoards(ctx, eng, up.Limits); err != nil {
			mErrors.Inc()
			u.log.Errorf("save limit boards: %v", err)
		}
	}
	u.publish(ctx, eng, up, bars, now)
	return nil
}

func (u *Usecase) adjMap() map[string]float64 {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.adj
}

func (u *Usecase) saveChangedBoards(ctx context.Context, eng *market.Engine, evs []market.LimitEvent) error {
	changed := map[string]struct{}{}
	for _, ev := range evs {
		changed[ev.Symbol] = struct{}{}
	}
	var boards []market.LimitBoard
	for _, b := range eng.Boards() {
		if _, ok := changed[b.Symbol]; ok {
			boards = append(boards, b)
		}
	}
	return u.repo.SaveBoards(ctx, boards)
}

func (u *Usecase) publish(ctx context.Context, eng *market.Engine, up market.Update, bars []market.Bar, now time.Time) {
	send := func(subject string, payload any) {
		if err := u.pub.Publish(ctx, subject, payload); err != nil {
			u.log.Warnf("publish %s: %v", subject, err)
		}
	}
	for _, a := range up.Alerts {
		send(events.SubjectMarketAlert, a)
	}
	if len(up.Limits) > 0 {
		send(events.SubjectMarketLimit, map[string]any{"events": up.Limits})
	}
	watch := u.watch.Symbols(now)
	if len(watch) > 0 {
		in := make(map[string]struct{}, len(watch))
		for _, s := range watch {
			in[s] = struct{}{}
		}
		var wb []market.Bar
		for _, b := range bars {
			if _, ok := in[b.Symbol]; ok {
				wb = append(wb, b)
			}
		}
		if len(wb) > 0 {
			send(events.SubjectMarketBar, map[string]any{"bars": wb})
		}
		var changed []string
		for _, s := range up.Changed {
			if _, ok := in[s]; ok {
				changed = append(changed, s)
			}
		}
		if len(changed) > 0 {
			send(events.SubjectMarketFactor, map[string]any{"as_of": now, "rows": eng.Cross(changed)})
		}
	}
	send(events.SubjectMarketState, eng.State(now))
}

// SectorHeatLive 返回实时板块热度，10 秒内复用。
func (u *Usecase) SectorHeatLive(now time.Time) []market.SectorHeat {
	eng := u.Engine()
	if eng == nil {
		return nil
	}
	u.mu.RLock()
	if u.heat != nil && now.Sub(u.heatAt) < heatTTL {
		h := u.heat
		u.mu.RUnlock()
		return h
	}
	members, impulse, weights := u.members, u.impulse, u.weights
	u.mu.RUnlock()
	h := market.RankSectors(members, eng.MemberDays(), impulse, weights)
	u.mu.Lock()
	u.heat, u.heatAt = h, now
	u.mu.Unlock()
	return h
}

// SectorName 返回板块名称。
func (u *Usecase) SectorName(code string) string {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.names[code]
}

// Checkpoint 把盘中截面落库：因子、涨跌停、情绪、板块热度。as_of 为检查点时刻。
func (u *Usecase) Checkpoint(ctx context.Context, asOf time.Time) error {
	eng, err := u.ensureEngine(ctx, asOf)
	if err != nil || eng == nil {
		return err
	}
	return u.persistCross(ctx, eng, KindIntraday, asOf, eng.Cross(nil))
}

func (u *Usecase) persistCross(ctx context.Context, eng *market.Engine, kind string, asOf time.Time, rows []market.FactorRow) error {
	for i := range rows {
		rows[i].AsOf = asOf
	}
	if err := u.repo.SaveFactors(ctx, eng.Day(), kind, rows); err != nil {
		return fmt.Errorf("%s factors: %w", kind, err)
	}
	boards := eng.Boards()
	for i := range boards {
		boards[i].AsOf = asOf
	}
	if err := u.repo.SaveBoards(ctx, boards); err != nil {
		return fmt.Errorf("%s boards: %w", kind, err)
	}
	if err := u.repo.SaveSentiment(ctx, eng.Sentiment(asOf)); err != nil {
		return fmt.Errorf("%s sentiment: %w", kind, err)
	}
	u.mu.Lock()
	u.heat = nil
	u.mu.Unlock()
	if err := u.repo.SaveSectorHeat(ctx, eng.Day(), asOf, u.SectorHeatLive(asOf)); err != nil {
		return fmt.Errorf("%s sector heat: %w", kind, err)
	}
	return nil
}

// Auction 在 09:25 撮合后落竞价因子，as_of = 09:25。
func (u *Usecase) Auction(ctx context.Context, now time.Time) error {
	eng, err := u.ensureEngine(ctx, now)
	if err != nil || eng == nil {
		return err
	}
	if err := u.Refresh(ctx, now); err != nil {
		u.log.Warnf("auction refresh: %v", err)
	}
	rows := eng.AuctionCross()
	asOf := eng.Day().Add(9*time.Hour + 25*time.Minute)
	for i := range rows {
		rows[i].AsOf = asOf
	}
	return u.repo.SaveFactors(ctx, eng.Day(), KindAuction, rows)
}

// Close 收盘定稿：合成日线（不覆盖 M01 已写的）、收盘因子、涨跌停、情绪、板块，as_of = 15:00。
func (u *Usecase) Close(ctx context.Context, now time.Time) error {
	eng, err := u.ensureEngine(ctx, now)
	if err != nil || eng == nil {
		return err
	}
	if err := u.Refresh(ctx, now); err != nil {
		u.log.Warnf("close refresh: %v", err)
	}
	daily, rows := eng.CloseDay()
	if err := u.repo.SaveDailyIfAbsent(ctx, daily); err != nil {
		return fmt.Errorf("close daily bars: %w", err)
	}
	return u.persistCross(ctx, eng, KindClose, market.CloseTime(eng.Day()), rows)
}

// Overseas 在开盘前算外围冲击，写 sector_heat（as_of = now），并供盘中热度使用。
func (u *Usecase) Overseas(ctx context.Context, now time.Time) error {
	day := market.DateOf(now)
	open, err := u.cal.Open(day)
	if err != nil || !open {
		return err
	}
	prev, err := u.cal.PrevOpen(day)
	if err != nil {
		return err
	}
	quotes, err := u.repo.OverseasQuotes(ctx, prev.AddDate(0, 0, -7))
	if err != nil {
		return err
	}
	maps, err := u.repo.OverseasMappings(ctx)
	if err != nil {
		return err
	}
	impulse := market.Impulse(quotes, maps, prev, now)
	u.mu.Lock()
	u.impulse, u.impulseOn = impulse, day
	u.heat = nil
	weights := u.weights
	u.mu.Unlock()
	rows := make([]market.SectorHeat, 0, len(impulse))
	for code, v := range impulse {
		rows = append(rows, market.SectorHeat{SectorCode: code, OverseasImpulse: v, Heat: weights.Overseas * v})
	}
	sortHeat(rows)
	return u.repo.SaveSectorHeat(ctx, day, now, rows)
}

// Capital 在 M01 的资金流、龙虎榜入库后算资金与席位因子。as_of = now，这些数据盘后才公开。
func (u *Usecase) Capital(ctx context.Context, now time.Time) error {
	day := market.DateOf(now)
	open, err := u.cal.Open(day)
	if err != nil || !open {
		return err
	}
	flows, err := u.repo.MoneyFlows(ctx, day, 5)
	if err != nil {
		return err
	}
	amounts, err := u.repo.DayAmounts(ctx, day)
	if err != nil {
		return err
	}
	seats, err := u.repo.LhbSeats(ctx, day)
	if err != nil {
		return err
	}
	tags, err := u.repo.SeatTags(ctx)
	if err != nil {
		return err
	}
	all := map[string]market.Values{}
	for sym, f := range flows {
		all[sym] = market.FlowFactors(amounts[sym], f)
	}
	for sym, s := range seats {
		v := all[sym]
		if v == nil {
			v = market.Values{}
			all[sym] = v
		}
		for k, x := range market.LhbFactors(s, tags) {
			v[k] = x
		}
	}
	asOf := now.Truncate(time.Minute)
	rows := make([]market.FactorRow, 0, len(all))
	for sym, v := range all {
		rows = append(rows, market.FactorRow{Symbol: sym, AsOf: asOf, Values: v})
	}
	return u.repo.SaveFactors(ctx, day, KindCapital, rows)
}
