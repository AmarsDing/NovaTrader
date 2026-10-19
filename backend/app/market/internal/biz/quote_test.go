package biz

import (
	"context"
	"testing"
	"time"

	"server/pkg/market"

	"github.com/go-kratos/kratos/v2/log"
)

func quoteUsecase(repo *fakeRepo, snaps *fakeSnaps) *Usecase {
	return NewUsecase(repo, snaps, &fakePub{}, Config{StaleAfter: 10 * time.Second}, log.DefaultLogger)
}

func quoteBasic() StockBasicRow {
	return StockBasicRow{
		Found: true, Name: "贵州茅台", Market: "SH",
		TotalShare: 1256197800, FloatShare: 1256197800,
		CircMv: 0, PeTtm: 21.5, Pb: 8.1,
		ListDate: time.Date(2001, 8, 27, 0, 0, 0, 0, time.UTC),
	}
}

func todaySnap(last float64) market.Snapshot {
	now := time.Now()
	return market.Snapshot{
		Symbol: "600519.SH", Time: now, AsOf: now, Source: "tdx",
		PreClose: 1800, Open: 1795, High: last + 5, Low: 1790, Last: last,
		Volume: 21000, Amount: 3.78e7,
		Bid: [][2]float64{{last - 0.01, 100}, {last - 0.02, 200}},
		Ask: [][2]float64{{last + 0.01, 100}, {last + 0.02, 200}},
	}
}

// 盘中快照口径：source=snap、涨跌/换手/市值/涨跌停价/五档齐备，收盘时段外不标陈旧。
func TestQuoteSnapshot(t *testing.T) {
	repo := &fakeRepo{basic: quoteBasic()}
	uc := quoteUsecase(repo, &fakeSnaps{snaps: []market.Snapshot{todaySnap(1809)}})
	q, err := uc.Quote(context.Background(), "600519.SH")
	if err != nil {
		t.Fatal(err)
	}
	if !q.Found || q.Source != "snap" || q.Status != "TRADE" {
		t.Fatalf("found/source/status: %+v", q)
	}
	if q.Last != 1809 || q.PreClose != 1800 || q.Chg != 9 {
		t.Fatalf("price block: %+v", q)
	}
	if got := q.PctChg; got < 0.49 || got > 0.51 {
		t.Fatalf("pct_chg want ~0.5 got %v", got)
	}
	if len(q.Bid) != 2 || len(q.Ask) != 2 {
		t.Fatalf("book levels: %+v", q)
	}
	if q.LimitUp != 1980 || q.LimitDown != 1620 || q.NoLimit {
		t.Fatalf("limit prices: %+v", q)
	}
	if q.PeTtm != 21.5 || q.Pb != 8.1 {
		t.Fatalf("statics: %+v", q)
	}
	// 主板母股全流通：换手 = 21000/1256197800≈0.00167%；总市值 = 1809×total_share。
	if q.TurnoverRate <= 0 || q.TotalMv != 1809*1256197800 {
		t.Fatalf("turnover/total_mv: %+v", q)
	}
	// circ_mv 列为 0 时按定义补 last×float_share。
	if q.CircMv != q.Last*float64(1256197800) {
		t.Fatalf("circ_mv fallback: %+v", q)
	}
}

// 无快照回退最近日线：source=1d、盘口为空、不复用快照陈旧逻辑。
func TestQuoteDailyFallback(t *testing.T) {
	repo := &fakeRepo{basic: quoteBasic(), dayBars: []BarRow{
		{Bar: market.Bar{Time: time.Now().AddDate(0, 0, -1), Open: 1780, High: 1805, Low: 1770, Close: 1800, Volume: 25000, Amount: 4.4e7}, PreClose: 1778},
	}}
	uc := quoteUsecase(repo, &fakeSnaps{})
	q, err := uc.Quote(context.Background(), "600519.SH")
	if err != nil {
		t.Fatal(err)
	}
	if q.Source != "1d" || q.Last != 1800 || q.PreClose != 1778 || q.Stale {
		t.Fatalf("fallback block: %+v", q)
	}
	if len(q.Bid) != 0 || len(q.Ask) != 0 {
		t.Fatalf("daily fallback must not fabricate book: %+v", q)
	}
}

// 跨日残留快照不用，回退 1d。
func TestQuoteSkipsCrossDaySnapshot(t *testing.T) {
	repo := &fakeRepo{basic: quoteBasic(), dayBars: []BarRow{
		{Bar: market.Bar{Time: time.Now().AddDate(0, 0, -1), Close: 1800}, PreClose: 1778},
	}}
	old := todaySnap(1809)
	old.Time = time.Now().AddDate(0, 0, -1)
	old.AsOf = old.Time
	uc := quoteUsecase(repo, &fakeSnaps{snaps: []market.Snapshot{old}})
	q, err := uc.Quote(context.Background(), "600519.SH")
	if err != nil {
		t.Fatal(err)
	}
	if q.Source != "1d" {
		t.Fatalf("cross-day snapshot must not win: %+v", q)
	}
}

// 停牌与新股无涨跌幅：状态标记与 no_limit，涨跌停价置零不猜。
func TestQuoteSuspendAndNoLimit(t *testing.T) {
	base := quoteBasic()
	base.Suspended = true
	base.ListDate = time.Now().AddDate(0, 0, -2) // 上市前五日内无涨跌幅
	repo := &fakeRepo{basic: base}
	uc := quoteUsecase(repo, &fakeSnaps{snaps: []market.Snapshot{todaySnap(1809)}})
	q, err := uc.Quote(context.Background(), "600519.SH")
	if err != nil {
		t.Fatal(err)
	}
	if q.Status != "SUSPEND" || !q.NoLimit || q.LimitUp != 0 || q.LimitDown != 0 {
		t.Fatalf("suspend/no_limit: %+v", q)
	}
}

// 未知证券：Found=false、静态全零，不算错误。
func TestQuoteNotFound(t *testing.T) {
	uc := quoteUsecase(&fakeRepo{}, &fakeSnaps{})
	q, err := uc.Quote(context.Background(), "000001.SZ")
	if err != nil {
		t.Fatal(err)
	}
	if q.Found || q.Source != "" || q.Last != 0 || q.Name != "" {
		t.Fatalf("not found row: %+v", q)
	}
}
