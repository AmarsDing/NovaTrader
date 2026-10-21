package biz

import (
	"testing"
	"time"
)

func t9(h, m int) *time.Time {
	t := time.Date(2026, 10, 19, h, m, 0, 0, tradecalShanghaiForTest())
	return &t
}

func tradecalShanghaiForTest() *time.Location {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.UTC
	}
	return loc
}

func rows(pool string, specs ...struct {
	sym  string
	con  int
	rea  string
	open int
	fs   *time.Time
}) []PoolRow {
	out := []PoolRow{}
	for _, s := range specs {
		out = append(out, PoolRow{
			Symbol: s.sym, Name: s.sym, Pool: pool, Consecutive: s.con,
			Reason: s.rea, OpenCount: s.open, FirstSealAt: s.fs,
		})
	}
	return out
}

type r struct {
	sym  string
	con  int
	rea  string
	open int
	fs   *time.Time
}

func up(specs ...r) []PoolRow {
	rows2 := []PoolRow{}
	for _, s := range specs {
		rows2 = append(rows2, PoolRow{
			Symbol: s.sym, Name: s.sym, Pool: "up", Consecutive: s.con,
			Reason: s.rea, OpenCount: s.open, FirstSealAt: s.fs,
		})
	}
	return rows2
}

func TestComputeFrozenPromotion(t *testing.T) {
	// 昨：3 只 1 板（A/B/C）、2 只 2 板（D/E）、1 只 4 板（F）。
	yday := up(
		r{"A", 1, "x", 0, nil}, r{"B", 1, "x", 0, nil}, r{"C", 1, "y", 0, nil},
		r{"D", 2, "x", 0, nil}, r{"E", 2, "y", 0, nil},
		r{"F", 4, "z", 0, nil},
	)
	// 今：A 晋级 2 板，B/C 没封住（不在 up 池）；D 晋级 3 板，E 掉队；F 晋级 5 板。
	day := up(
		r{"A", 2, "x", 0, t9(9, 31)}, r{"G", 1, "x", 0, t9(9, 45)}, r{"H", 1, "y", 2, t9(13, 20)},
		r{"D", 3, "x", 0, t9(9, 30)},
		r{"F", 5, "z", 0, t9(9, 25)},
	)
	day = append(day, PoolRow{Symbol: "Q", Pool: "down", Consecutive: 1})
	day = append(day, PoolRow{Symbol: "R", Pool: "broken", Consecutive: 1, Reason: "x"})
	f := computeFrozen(day, yday, map[string]TierBar{}, "test")

	if f.Promo12 == nil || *f.Promo12 != 33.33 {
		t.Fatalf("promo12 = %v, want 33.33", derefF(f.Promo12))
	}
	if f.Promo23 == nil || *f.Promo23 != 50 {
		t.Fatalf("promo23 = %v, want 50", derefF(f.Promo23))
	}
	if f.PromoHigh == nil || *f.PromoHigh != 100 {
		t.Fatalf("promoHigh = %v, want 100", derefF(f.PromoHigh))
	}
	if derefI(f.UpCount) != 5 || derefI(f.DownCount) != 1 || derefI(f.BrokenCount) != 1 {
		t.Fatalf("counts = %d/%d/%d", derefI(f.UpCount), derefI(f.DownCount), derefI(f.BrokenCount))
	}
	// 封板率 = 5/(5+1)
	if derefF(f.SealRate) != 83.33 {
		t.Fatalf("sealRate = %v, want 83.33", derefF(f.SealRate))
	}
	if derefI(f.MaxHeight) != 5 {
		t.Fatalf("maxHeight = %d, want 5", derefI(f.MaxHeight))
	}
	// 有 5 板无 4 板 → 断层 [4]
	if len(f.StructureGaps) != 1 || f.StructureGaps[0] != 4 {
		t.Fatalf("gaps = %v, want [4]", f.StructureGaps)
	}
	// 首板名单只收 1 板
	if len(f.FirstBoard) != 2 {
		t.Fatalf("firstBoard = %d, want 2", len(f.FirstBoard))
	}
	// 首封时间排序：G(09:45) 在 H(13:20) 前
	if f.FirstBoard[0].Symbol != "G" {
		t.Fatalf("firstBoard[0] = %s, want G", f.FirstBoard[0].Symbol)
	}
	// 天梯 5 板层只 F，3 板层只 D
	if len(f.Ladder["5"]) != 1 || f.Ladder["5"][0].Symbol != "F" {
		t.Fatalf("ladder[5] = %+v", f.Ladder["5"])
	}
	// 封板质量：H 炸过 2 次 → Resealed；其余 4 只 NeverBroken；10 点前早封 4 只（A/G/D/F）
	if f.SealQ == nil || f.SealQ.NeverBroken != 4 || f.SealQ.Resealed != 1 || f.SealQ.EarlySeal != 4 {
		t.Fatalf("sealQ = %+v", f.SealQ)
	}
	// 题材归并：x 3 只（A/G/D）> y/z 各 1
	if len(f.ThemeTree) == 0 || f.ThemeTree[0].Reason != "x" || f.ThemeTree[0].Count != 3 {
		t.Fatalf("theme[0] = %+v", f.ThemeTree)
	}
	// 今日全部日线缺失 → tier 记 missing
	foundTierMissing := false
	for _, m := range f.Missing {
		if m == "tier_bars" {
			foundTierMissing = true
		}
	}
	if !foundTierMissing {
		t.Fatalf("missing = %v, want tier_bars", f.Missing)
	}
	if !f.Partial {
		t.Fatalf("partial should be true when tier bars missing")
	}
	if f.TierAvg != nil {
		t.Fatalf("tierAvg should be nil when all bars missing")
	}
}

func TestComputeFrozenPrevTier(t *testing.T) {
	yday := up(r{"A", 1, "x", 0, nil}, r{"B", 2, "x", 0, nil}, r{"C", 2, "y", 0, nil})
	day := up(r{"C", 3, "y", 0, t9(9, 31)}) // C 再度涨停
	bars := map[string]TierBar{
		"A": {Close: 11, PreClose: 10}, // +10%
		"B": {Close: 9, PreClose: 10},  // -10%
		"C": {Close: 11, PreClose: 10}, // +10%
	}
	f := computeFrozen(day, yday, bars, "test")
	if f.TierAvg == nil || *f.TierAvg != 3.33 {
		t.Fatalf("tierAvg = %v, want 3.33", derefF(f.TierAvg))
	}
	if derefF(f.TierMedian) != 10 {
		t.Fatalf("tierMedian = %v, want 10", derefF(f.TierMedian))
	}
	if derefF(f.TierRedRate) != 66.67 {
		t.Fatalf("tierRedRate = %v, want 66.67", derefF(f.TierRedRate))
	}
	if derefF(f.TierRelimit) != 33.33 {
		t.Fatalf("tierRelimit = %v, want 33.33", derefF(f.TierRelimit))
	}
	if len(f.PrevTier) != 3 || f.PrevTier[0].Pct != 10 {
		t.Fatalf("prevTier order wrong: %+v", f.PrevTier)
	}
}

func TestComputeFrozenEmpty(t *testing.T) {
	f := computeFrozen(nil, nil, nil, "test")
	if !f.Partial {
		t.Fatalf("partial should be true")
	}
	has := map[string]bool{}
	for _, m := range f.Missing {
		has[m] = true
	}
	if !has["limit_pool"] || !has["limit_pool_prev"] {
		t.Fatalf("missing = %v, want limit_pool + limit_pool_prev", f.Missing)
	}
	if f.UpCount != nil || f.Promo12 != nil {
		t.Fatalf("scalars must stay nil when nothing fetched")
	}
}
