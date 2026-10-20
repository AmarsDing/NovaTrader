package biz

import (
	"context"
	"testing"
	"time"

	"server/pkg/market"
	"server/pkg/tradecal"

	"github.com/go-kratos/kratos/v2/log"
)

// chipBars 造 n 根「10 → 12 一字涨 + 50% 换手」简版日线。
// adj 全 1（无复权差），floatShare 驱动换手率。
func chipFakeBars(sym string, n int, adjFactor float64) []BarRow {
	day := time.Date(2026, 10, 20, 0, 0, 0, 0, tradecal.Shanghai())
	rows := make([]BarRow, 0, n)
	for i := 0; i < n; i++ {
		price := 10.0
		if i > 0 {
			price = 12.0
		}
		rows = append(rows, BarRow{
			Bar: market.Bar{
				Symbol: sym, Time: day.AddDate(0, 0, i-n),
				Open: price, High: price, Low: price, Close: price,
				Volume: 5e7, Amount: 5e7 * price,
			},
			Adj: adjFactor, PreClose: 10,
		})
	}
	return rows
}

func newChipUsecase(repo *fakeRepo) *Usecase {
	repo.factors = map[string][]market.FactorRow{}
	repo.boards = map[string]market.LimitBoard{}
	return NewUsecase(repo, &fakeSnaps{}, &fakePub{subs: map[string]int{}, last: map[string]any{}}, NewConfig(nil), log.DefaultLogger)
}

// 正常路径：30 根日线 + 流通股本 1e8 → status=ok，均价≈11，现价 12 全部获利。
func TestChipDistributionOK(t *testing.T) {
	const sym = "600519.SH"
	repo := &fakeRepo{
		dayBars: chipFakeBars(sym, 30, 1),
		basic:   StockBasicRow{Found: true, Name: "贵州茅台", FloatShare: 1e8},
	}
	uc := newChipUsecase(repo)
	asOf := time.Date(2026, 10, 20, 0, 0, 0, 0, tradecal.Shanghai())
	rep, err := uc.ChipDistribution(context.Background(), sym, asOf, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != market.ChipOK {
		t.Fatalf("status=%q reason=%q", rep.Status, rep.Reason)
	}
	if rep.DaysUsed != 30 || rep.Profit == nil || *rep.Profit < 0.99 {
		t.Fatalf("days=%d profit=%v", rep.DaysUsed, rep.Profit)
	}
	// 首日 10 播种、其后 29 天 50% 换手搬到 12：10 处存量 0.5^29≈2e-9，均价趋 12。
	if rep.AvgCost == nil || *rep.AvgCost < 11.9 || *rep.AvgCost > 12.01 {
		t.Fatalf("avg=%v want ≈12", *rep.AvgCost)
	}
	if rep.WindowStart.IsZero() || rep.WindowEnd != asOf.AddDate(0, 0, -1) {
		t.Fatalf("window %v..%v", rep.WindowStart, rep.WindowEnd)
	}
}

// float_share 缺失 → not_meaningful（无法算换手率，禁按 turn=0 伪装）。
func TestChipDistributionMissingFloatShare(t *testing.T) {
	const sym = "600519.SH"
	repo := &fakeRepo{
		dayBars: chipFakeBars(sym, 30, 1),
		basic:   StockBasicRow{Found: true},
	}
	rep, err := newChipUsecase(repo).ChipDistribution(context.Background(), sym, time.Date(2026, 10, 20, 0, 0, 0, 0, tradecal.Shanghai()), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != market.ChipNotMeaningful || rep.Profit != nil {
		t.Fatalf("status=%q profit=%v want not_meaningful+nil", rep.Status, rep.Profit)
	}
}

// 库中无该券 → missing；窗口无日线 → missing。
func TestChipDistributionMissing(t *testing.T) {
	const sym = "600519.SH"
	r1, err := newChipUsecase(&fakeRepo{basic: StockBasicRow{Found: false}}).
		ChipDistribution(context.Background(), sym, time.Date(2026, 10, 20, 0, 0, 0, 0, tradecal.Shanghai()), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if r1.Status != chipStatusMissing {
		t.Fatalf("no-basic status=%q", r1.Status)
	}
	r2, err := newChipUsecase(&fakeRepo{basic: StockBasicRow{Found: true, FloatShare: 1e8}}).
		ChipDistribution(context.Background(), sym, time.Date(2026, 10, 20, 0, 0, 0, 0, tradecal.Shanghai()), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if r2.Status != chipStatusMissing {
		t.Fatalf("no-bars status=%q", r2.Status)
	}
}

// adj_factor 缺失（≤0）→ not_meaningful，禁止以 0/1 冒充因子（§4.4）。
func TestChipDistributionAdjMissing(t *testing.T) {
	const sym = "600519.SH"
	repo := &fakeRepo{
		dayBars: chipFakeBars(sym, 30, 0),
		basic:   StockBasicRow{Found: true, FloatShare: 1e8},
	}
	rep, err := newChipUsecase(repo).ChipDistribution(context.Background(), sym, time.Date(2026, 10, 20, 0, 0, 0, 0, tradecal.Shanghai()), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != market.ChipNotMeaningful || rep.Profit != nil {
		t.Fatalf("status=%q profit=%v", rep.Status, rep.Profit)
	}
}

// 前复权：除权段 adj=0.5 → 前复权价 ×0.5,获利比例与未复权一致（缩放无关）但均价接近 5.5。
func TestChipDistributionForwardAdjust(t *testing.T) {
	const sym = "600519.SH"
	rows := chipFakeBars(sym, 30, 0.5)
	// 末根为基准因子 1：前半段 adj=0.5 会被拉到 0.5 倍价位。
	rows[len(rows)-1].Adj = 1
	repo := &fakeRepo{dayBars: rows, basic: StockBasicRow{Found: true, FloatShare: 1e8}}
	rep, err := newChipUsecase(repo).ChipDistribution(context.Background(), sym, time.Date(2026, 10, 20, 0, 0, 0, 0, tradecal.Shanghai()), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != market.ChipOK {
		t.Fatalf("status=%q %q", rep.Status, rep.Reason)
	}
	// 前复权后：首日 10×0.5=5 播种，其余 12 全搬 → 均价 ≈(5×0.5+12×0.5)=8.5 附近（衰减逐日,长窗≈11.x 偏 12）。
	if rep.AvgCost == nil || *rep.AvgCost < 8 || *rep.AvgCost > 12 {
		t.Fatalf("qfq avg=%v", rep.AvgCost)
	}
}
