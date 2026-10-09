package biz

import (
	"context"
	"sync"
	"testing"
	"time"

	"server/pkg/events"
	"server/pkg/market"
	"server/pkg/tradecal"

	"github.com/go-kratos/kratos/v2/log"
)

type fakeRepo struct {
	mu        sync.Mutex
	stocks    []market.StockInfo
	hist      map[string][]market.DayBar
	sealed    map[string]int
	minute    []market.Bar
	daily     []market.DayBar
	factors   map[string][]market.FactorRow
	boards    map[string]market.LimitBoard
	sentiment []market.Sentiment
	heat      int
}

func (f *fakeRepo) LoadStocks(context.Context) ([]market.StockInfo, error) { return f.stocks, nil }
func (f *fakeRepo) DailyHistory(context.Context, time.Time, int) (map[string][]market.DayBar, error) {
	return f.hist, nil
}
func (f *fakeRepo) SealedOn(context.Context, time.Time) (map[string]int, error) { return f.sealed, nil }
func (f *fakeRepo) ClosePhase(context.Context, time.Time) (market.Phase, error) {
	return market.PhaseWarm, nil
}
func (f *fakeRepo) Config(context.Context, string) (string, error) { return "", nil }
func (f *fakeRepo) SaveMinuteBars(_ context.Context, bars []market.Bar, _ map[string]float64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.minute = append(f.minute, bars...)
	return nil
}
func (f *fakeRepo) SaveDailyIfAbsent(_ context.Context, bars []market.DayBar) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.daily = append(f.daily, bars...)
	return nil
}
func (f *fakeRepo) SaveFactors(_ context.Context, _ time.Time, kind string, rows []market.FactorRow) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.factors[kind] = append(f.factors[kind], rows...)
	return nil
}
func (f *fakeRepo) SaveBoards(_ context.Context, boards []market.LimitBoard) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, b := range boards {
		f.boards[b.Symbol+b.Direction] = b
	}
	return nil
}
func (f *fakeRepo) SaveSentiment(_ context.Context, s market.Sentiment) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sentiment = append(f.sentiment, s)
	return nil
}
func (f *fakeRepo) SaveSectorHeat(context.Context, time.Time, time.Time, []market.SectorHeat) error {
	f.heat++
	return nil
}
func (f *fakeRepo) SectorMembers(context.Context, time.Time) (map[string][]string, map[string]string, error) {
	return nil, nil, nil
}
func (f *fakeRepo) MoneyFlows(context.Context, time.Time, int) (map[string][]market.Flow, error) {
	return nil, nil
}
func (f *fakeRepo) DayAmounts(context.Context, time.Time) (map[string]float64, error) {
	return nil, nil
}
func (f *fakeRepo) LhbSeats(context.Context, time.Time) (map[string][]market.Seat, error) {
	return nil, nil
}
func (f *fakeRepo) SeatTags(context.Context) (map[string]string, error) { return nil, nil }
func (f *fakeRepo) EnsureDefaults(context.Context) error                { return nil }
func (f *fakeRepo) OverseasQuotes(context.Context, time.Time) ([]market.Quote, error) {
	return nil, nil
}
func (f *fakeRepo) OverseasMappings(context.Context) ([]market.Mapping, error) { return nil, nil }
func (f *fakeRepo) Bars(context.Context, string, string, time.Time, time.Time, int) ([]BarRow, error) {
	return nil, nil
}
func (f *fakeRepo) FactorsAt(context.Context, time.Time, string, time.Time, []string) ([]FactorRecord, error) {
	return nil, nil
}
func (f *fakeRepo) SentimentAt(context.Context, time.Time, time.Time) (market.Sentiment, bool, error) {
	return market.Sentiment{}, false, nil
}
func (f *fakeRepo) Boards(context.Context, time.Time, string, string) ([]market.LimitBoard, error) {
	return nil, nil
}
func (f *fakeRepo) SectorHeatAt(context.Context, time.Time, time.Time, int) ([]market.SectorHeat, error) {
	return nil, nil
}

type fakeSnaps struct{ snaps []market.Snapshot }

func (f *fakeSnaps) All(context.Context) ([]market.Snapshot, error) { return f.snaps, nil }

type fakePub struct {
	mu   sync.Mutex
	subs map[string]int
	last map[string]any
}

func (p *fakePub) Publish(_ context.Context, subject string, payload any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.subs[subject]++
	p.last[subject] = payload
	return nil
}

// 一只股票从开盘到封板、到收盘定稿，验证落库和事件的完整链路。
func TestUsecaseDayLifecycle(t *testing.T) {
	sh := tradecal.Shanghai()
	day := time.Date(2026, 9, 15, 0, 0, 0, 0, sh)
	if open, err := tradecal.Default.Open(day); err != nil || !open {
		t.Fatalf("%s must be a trading day: %v", day.Format("2006-01-02"), err)
	}
	const sym = "600000.SH"
	var hist []market.DayBar
	for i := 30; i >= 1; i-- {
		d := day.AddDate(0, 0, -i)
		hist = append(hist, market.DayBar{Symbol: sym, Time: d, Open: 10, High: 10.2, Low: 9.8, Close: 10, PreClose: 10, Volume: 1e6, Amount: 1e7, Adj: 1})
	}
	repo := &fakeRepo{
		stocks:  []market.StockInfo{{Symbol: sym, Name: "浦发银行", FloatShare: 1e9, ListDate: day.AddDate(-10, 0, 0)}},
		hist:    map[string][]market.DayBar{sym: hist},
		sealed:  map[string]int{},
		factors: map[string][]market.FactorRow{},
		boards:  map[string]market.LimitBoard{},
	}
	snaps := &fakeSnaps{}
	pub := &fakePub{subs: map[string]int{}, last: map[string]any{}}
	uc := NewUsecase(repo, snaps, pub, NewConfig(nil), log.DefaultLogger)
	ctx := context.Background()

	if err := uc.Warmup(ctx, day.Add(8*time.Hour+40*time.Minute)); err != nil {
		t.Fatal(err)
	}
	var vol int64
	tick := func(hh, mm, ss int, last float64, sealed bool) {
		t.Helper()
		ts := day.Add(time.Duration(hh)*time.Hour + time.Duration(mm)*time.Minute + time.Duration(ss)*time.Second)
		vol += 10000
		s := market.Snapshot{
			Symbol: sym, Time: ts, AsOf: ts, PreClose: 10, Open: 10.2, High: last, Low: 10.1, Last: last,
			Volume: vol, Amount: float64(vol) * last, Bid: [][2]float64{{last - 0.01, 100}},
		}
		if sealed {
			s.Bid = [][2]float64{{last, 5e5}}
		} else {
			s.Ask = [][2]float64{{last + 0.01, 100}}
		}
		idx := market.Snapshot{Symbol: "000001.SH", Time: ts, AsOf: ts, PreClose: 3000, Open: 3000, High: 3010, Low: 2995, Last: 3005, Volume: vol, Amount: 1}
		snaps.snaps = []market.Snapshot{s, idx}
		if err := uc.Refresh(ctx, ts.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	tick(9, 30, 5, 10.5, false)
	tick(9, 31, 5, 10.8, false)
	tick(9, 32, 5, 11.0, true)
	tick(9, 33, 5, 11.0, true)

	if len(repo.minute) < 3 {
		t.Fatalf("closed 1m bars = %d, want ≥ 3", len(repo.minute))
	}
	if pub.subs[events.SubjectMarketAlert] == 0 || pub.subs[events.SubjectMarketLimit] == 0 {
		t.Fatalf("limit-up must publish alert and limit events: %v", pub.subs)
	}
	if pub.subs[events.SubjectMarketState] != 4 {
		t.Fatalf("market.state per round = %d, want 4", pub.subs[events.SubjectMarketState])
	}
	if b, ok := repo.boards[sym+market.DirUp]; !ok || b.Status != market.StatusSealed {
		t.Fatalf("board = %+v", repo.boards)
	}

	uc.SetWatchlist("strategy", []string{sym}, time.Hour)
	before := pub.subs[events.SubjectMarketFactor]
	tick(9, 34, 5, 11.0, true)
	if pub.subs[events.SubjectMarketFactor] == before {
		t.Fatal("watched symbol must get market.factor.updated")
	}

	cp := day.Add(10 * time.Hour)
	if err := uc.Checkpoint(ctx, cp); err != nil {
		t.Fatal(err)
	}
	rows := repo.factors[KindIntraday]
	if len(rows) != 1 || !rows[0].AsOf.Equal(cp) || rows[0].Values["up_sealed"] != 1 {
		t.Fatalf("intraday factors = %+v", rows)
	}
	if _, ok := rows[0].Values["ma5"]; !ok {
		t.Fatal("intraday row must carry indicator preview")
	}

	if err := uc.Close(ctx, day.Add(15*time.Hour+5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if len(repo.daily) != 1 || repo.daily[0].Close != 11 || repo.daily[0].PreClose != 10 {
		t.Fatalf("daily = %+v", repo.daily)
	}
	closeRows := repo.factors[KindClose]
	if len(closeRows) != 1 || !closeRows[0].AsOf.Equal(market.CloseTime(day)) {
		t.Fatalf("close factors = %+v", closeRows)
	}
	last := repo.sentiment[len(repo.sentiment)-1]
	if last.UpCount != 1 || !last.AsOf.Equal(market.CloseTime(day)) {
		t.Fatalf("close sentiment = %+v", last)
	}
	// 收盘重复执行不重复产出。
	if err := uc.Close(ctx, day.Add(15*time.Hour+6*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if len(repo.daily) != 2 || repo.daily[1].Close != 11 {
		t.Fatalf("second close must re-emit the same daily bar (upsert is idempotent): %+v", repo.daily)
	}
}
