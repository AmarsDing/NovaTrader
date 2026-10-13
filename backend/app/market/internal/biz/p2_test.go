package biz

import (
	"context"
	"testing"
	"time"

	"server/pkg/market"
	"server/pkg/tradecal"

	"github.com/go-kratos/kratos/v2/log"
)

/* p2.go（K 线缺口、昨涨停今溢价、20 日均线占比）的单元测试。 */

type gapRepo struct {
	fakeRepo
	rows []DailyBarDay
}

func (r *gapRepo) DailyBarDays(context.Context, time.Time, time.Time) ([]DailyBarDay, error) {
	return r.rows, nil
}

func shDay(y, m, d int) time.Time {
	return time.Date(y, time.Month(m), d, 0, 0, 0, 0, tradecal.Shanghai())
}

func TestKlineGaps(t *testing.T) {
	// 2026-10-09（五）、10-12（一）、10-13（二）是连续三个交易日；A 三天都有，B 缺 10-12，C 缺两天。
	rows := []DailyBarDay{
		{Symbol: "A", Day: shDay(2026, 10, 9)}, {Symbol: "A", Day: shDay(2026, 10, 12)}, {Symbol: "A", Day: shDay(2026, 10, 13)},
		{Symbol: "B", Day: shDay(2026, 10, 9)}, {Symbol: "B", Day: shDay(2026, 10, 13)},
		{Symbol: "C", Day: shDay(2026, 10, 9)},
	}
	uc := NewUsecase(&gapRepo{rows: rows}, nil, nil, NewConfig(nil), log.DefaultLogger)
	report, err := uc.KlineGaps(context.Background(), shDay(2026, 10, 13), 3)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Found || report.Universe != 3 {
		t.Fatalf("found=%v universe=%d", report.Found, report.Universe)
	}
	if report.MissingTotal != 3 {
		t.Fatalf("missing_total=%d want 3（B 1 天 + C 2 天）", report.MissingTotal)
	}
	if len(report.Days) != 3 || report.Days[0].Day.Format("2006-01-02") != "2026-10-13" {
		t.Fatalf("days 应为倒序三天：%+v", report.Days)
	}
	if report.Days[0].Missing != 1 || report.Days[0].Sample[0] != "C" {
		t.Fatalf("10-13 只缺 C：%+v", report.Days[0])
	}
	if report.Days[2].Missing != 0 {
		t.Fatalf("10-09 无缺口：%+v", report.Days[2])
	}
}

func TestKlineGapsEmpty(t *testing.T) {
	uc := NewUsecase(&gapRepo{}, nil, nil, NewConfig(nil), log.DefaultLogger)
	report, err := uc.KlineGaps(context.Background(), shDay(2026, 10, 13), 3)
	if err != nil {
		t.Fatal(err)
	}
	if report.Found || report.Universe != 0 || len(report.Days) != 3 {
		t.Fatalf("空库也要返回窗口天数：%+v", report)
	}
}

func TestKlineCheckWritesIssues(t *testing.T) {
	// KlineCheck 窗口取近 30 天的存在性：A 满勤，B 只缺当天。
	rows := []DailyBarDay{{Symbol: "A", Day: shDay(2026, 10, 13)}, {Symbol: "B", Day: shDay(2026, 10, 12)}}
	repo := &gapRepo{rows: rows}
	uc := NewUsecase(repo, nil, nil, NewConfig(nil), log.DefaultLogger)
	now := shDay(2026, 10, 13).Add(15*time.Hour + 35*time.Minute)
	if err := uc.KlineCheck(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if len(repo.issues) != 2 {
		t.Fatalf("应写整域汇总 + 逐票共 2 行，got %d", len(repo.issues))
	}
	var whole *QualityIssue
	for i := range repo.issues {
		if repo.issues[i].Symbol == "" {
			whole = &repo.issues[i]
		}
	}
	if whole == nil || whole.Domain != "market" || whole.Check != "completeness" {
		t.Fatalf("缺整域汇总行：%+v", repo.issues)
	}
	if whole.Detail["missing"] != 1 || whole.Detail["universe"] != 2 {
		t.Fatalf("汇总 detail 不对：%+v", whole.Detail)
	}
}

type premiumRepo struct {
	fakeRepo
	boards []market.LimitBoard
	bars   map[string]market.DayBar
	sealed map[string]int
	saved  []PremiumRow
	stored []PremiumRow
}

func (r *premiumRepo) Boards(_ context.Context, day time.Time, dir, status string) ([]market.LimitBoard, error) {
	return r.boards, nil
}
func (r *premiumRepo) DailyBarsOn(context.Context, time.Time) (map[string]market.DayBar, error) {
	return r.bars, nil
}
func (r *premiumRepo) SealedOn(context.Context, time.Time) (map[string]int, error) {
	return r.sealed, nil
}
func (r *premiumRepo) PremiumRows(context.Context, time.Time) ([]PremiumRow, error) {
	return r.stored, nil
}
func (r *premiumRepo) SavePremiums(_ context.Context, rows []PremiumRow) error {
	r.saved = append(r.saved, rows...)
	return nil
}

func TestPremiumSnapshotFromTable(t *testing.T) {
	day := shDay(2026, 10, 13)
	base := shDay(2026, 10, 12)
	repo := &premiumRepo{stored: []PremiumRow{
		{Symbol: "A", Name: "甲", TradeDate: day, BaseDate: base, Consecutive: 2, BaseClose: 22, Open: 23, High: 24, Close: 23.5, LimitAgain: true, Source: SourceMarketAgg, AsOf: day},
		{Symbol: "B", Name: "乙", TradeDate: day, BaseDate: base, Consecutive: 1, BaseClose: 10, Open: 9.8, High: 10.2, Close: 9.6, Source: SourceMarketAgg, AsOf: day},
	}}
	uc := NewUsecase(repo, nil, nil, NewConfig(nil), log.DefaultLogger)
	report, err := uc.PremiumSnapshot(context.Background(), day, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Found || report.Live {
		t.Fatalf("应读表非兜底：found=%v live=%v", report.Found, report.Live)
	}
	if report.Summary.Sample != 2 || report.Summary.LimitAgain != 1 {
		t.Fatalf("summary 样本/再板不对：%+v", report.Summary)
	}
	// A 收盘溢价 23.5/22-1≈0.0682，B 9.6/10-1=-0.04；A 排前
	if len(report.Rows) != 2 || report.Rows[0].Symbol != "A" {
		t.Fatalf("排序应按收盘溢价倒序：%+v", report.Rows)
	}
	if !report.Summary.HasClose || report.Summary.CloseWin != 0.5 {
		t.Fatalf("胜率应为 1/2：%+v", report.Summary)
	}
}

func TestPremiumSnapshotLiveFallback(t *testing.T) {
	day := shDay(2026, 10, 13)
	base := shDay(2026, 10, 12)
	repo := &premiumRepo{
		boards: []market.LimitBoard{
			{Symbol: "A", TradeDate: base, Direction: market.DirUp, Status: market.StatusSealed, LimitPrice: 22, Consecutive: 2},
		},
		bars: map[string]market.DayBar{
			"A": {Symbol: "A", Time: day, Open: 22.5, High: 23.2, Close: 23.1, PreClose: 22},
		},
		sealed: map[string]int{"A": 3},
	}
	repo.stocks = []market.StockInfo{{Symbol: "A", Name: "甲"}}
	uc := NewUsecase(repo, nil, nil, NewConfig(nil), log.DefaultLogger)
	report, err := uc.PremiumSnapshot(context.Background(), day, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Found || !report.Live {
		t.Fatalf("应为实时兜底：found=%v live=%v", report.Found, report.Live)
	}
	row := report.Rows[0]
	if row.Name != "甲" || !row.LimitAgain || row.BaseClose != 22 {
		t.Fatalf("兜底行不对：%+v", row)
	}
	if diff := row.ClosePremium - (23.1/22 - 1); diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("收盘溢价=%v", row.ClosePremium)
	}
}

func TestPremiumSnapshotEmptyBase(t *testing.T) {
	uc := NewUsecase(&premiumRepo{}, nil, nil, NewConfig(nil), log.DefaultLogger)
	report, err := uc.PremiumSnapshot(context.Background(), shDay(2026, 10, 13), 5, 50)
	if err != nil {
		t.Fatal(err)
	}
	if report.Found {
		t.Fatal("昨日无涨停名单时应 found=false")
	}
}

func TestPremiumJobs(t *testing.T) {
	day := shDay(2026, 10, 13)
	base := shDay(2026, 10, 12)
	repo := &premiumRepo{
		boards: []market.LimitBoard{
			{Symbol: "A", TradeDate: base, Direction: market.DirUp, Status: market.StatusSealed, LimitPrice: 22, Consecutive: 2},
		},
		bars:   map[string]market.DayBar{"A": {Symbol: "A", Time: day, Open: 22.5, High: 23.2, Close: 23.1, PreClose: 22}},
		sealed: map[string]int{"A": 3},
	}
	repo.stocks = []market.StockInfo{{Symbol: "A", Name: "甲"}}
	uc := NewUsecase(repo, nil, nil, NewConfig(nil), log.DefaultLogger)
	if err := uc.PremiumClose(context.Background(), day.Add(16*time.Hour+45*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if len(repo.saved) != 1 {
		t.Fatalf("应落 1 行：%+v", repo.saved)
	}
	saved := repo.saved[0]
	if !saved.HasClose || saved.ClosePremium <= 0 || !saved.LimitAgain {
		t.Fatalf("收盘定稿不对：%+v", saved)
	}
}

type breadthRepo struct {
	fakeRepo
	hist   map[string][]market.DayBar
	stored []BreadthRecord
	saved  []BreadthDay
}

func (r *breadthRepo) DailyHistory(context.Context, time.Time, int) (map[string][]market.DayBar, error) {
	return r.hist, nil
}
func (r *breadthRepo) BreadthRange(context.Context, time.Time, time.Time) ([]BreadthRecord, error) {
	return r.stored, nil
}
func (r *breadthRepo) SaveBreadth(_ context.Context, rec BreadthDay) error {
	r.saved = append(r.saved, rec)
	return nil
}

func TestBreadthCompute(t *testing.T) {
	day := shDay(2026, 10, 13)
	mk := func(sym string, closes []float64) []market.DayBar {
		var bars []market.DayBar
		d := day
		for i := len(closes) - 1; i >= 0; i-- {
			bars = append([]market.DayBar{{Symbol: sym, Time: d, Close: closes[i]}}, bars...)
			d = d.AddDate(0, 0, -1)
		}
		return bars
	}
	// A 收盘 110，MA20=100 → 上方；B 收盘 90 → 下方；C 不足 20 根 → 不进样本
	full := make([]float64, 20)
	for i := range full {
		full[i] = 100
	}
	a := append([]float64{}, full...)
	a[19] = 110
	b := append([]float64{}, full...)
	b[19] = 90
	repo := &breadthRepo{hist: map[string][]market.DayBar{
		"A": mk("A", a),
		"B": mk("B", b),
		"C": mk("C", []float64{1, 2, 3}),
	}}
	uc := NewUsecase(repo, nil, nil, NewConfig(nil), log.DefaultLogger)
	if err := uc.BreadthClose(context.Background(), day.Add(16*time.Hour+50*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if len(repo.saved) != 1 {
		t.Fatalf("应落 1 行：%+v", repo.saved)
	}
	rec := repo.saved[0]
	if rec.Total != 2 || rec.Above != 1 || rec.Below != 1 || rec.Ratio != 0.5 {
		t.Fatalf("广度不对：%+v", rec)
	}
}

func TestBreadthSnapshotReadsTable(t *testing.T) {
	day := shDay(2026, 10, 13)
	repo := &breadthRepo{stored: []BreadthRecord{
		{Day: day, Window: 20, Above: 3000, Below: 2000, Total: 5000, Ratio: 0.6, Source: SourceMarketAgg, AsOf: day},
		{Day: shDay(2026, 10, 12), Window: 20, Above: 2500, Below: 2500, Total: 5000, Ratio: 0.5, Source: SourceMarketAgg, AsOf: day},
	}}
	uc := NewUsecase(repo, nil, nil, NewConfig(nil), log.DefaultLogger)
	report, err := uc.BreadthSnapshot(context.Background(), day, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Found || report.Latest.Ratio != 0.6 || report.Latest.Live {
		t.Fatalf("应读表非兜底：%+v", report.Latest)
	}
	if len(report.Days) != 2 || report.Days[1].Ratio != 0.5 {
		t.Fatalf("序列不对：%+v", report.Days)
	}
}
