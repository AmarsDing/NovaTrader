package biz

import (
	"strconv"
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

// M05 P0-1 钉样：limit_pool.reason 为空（含纯空白）的涨停股归并进「未标注」桶，
// 不静默丢弃、不虚构题材；桶内计数/最高板/代表股（≤8 只）照常汇总。
func TestComputeFrozenThemeUnlabeled(t *testing.T) {
	unl := []PoolRow{}
	// 10 只 reason 为空：U1 三连板最高，其余一板；代表股按输入序取前 8。
	for i, sym := range []string{"U1", "U2", "U3", "U4", "U5", "U6", "U7", "U8", "U9", "U10"} {
		con := 1
		if i == 0 {
			con = 3
		}
		unl = append(unl, PoolRow{Symbol: sym, Name: sym, Pool: "up", Consecutive: con})
	}
	// 2 只纯空白 reason：应与空串同桶。
	unl = append(unl,
		PoolRow{Symbol: "W1", Name: "W1", Pool: "up", Consecutive: 2, Reason: "   "},
		PoolRow{Symbol: "W2", Name: "W2", Pool: "up", Consecutive: 2, Reason: ""},
	)
	// 1 只有标注的对照组。
	unl = append(unl, PoolRow{Symbol: "A1", Name: "A1", Pool: "up", Consecutive: 1, Reason: "a"})
	f := computeFrozen(unl, nil, map[string]TierBar{}, "test")

	if len(f.ThemeTree) != 2 {
		t.Fatalf("themeTree len = %d, want 2: %+v", len(f.ThemeTree), f.ThemeTree)
	}
	top := f.ThemeTree[0]
	if top.Reason != "未标注" {
		t.Fatalf("theme[0].Reason = %q, want 未标注", top.Reason)
	}
	if top.Count != 12 {
		t.Fatalf("theme[0].Count = %d, want 12（10 空 + 2 空白同桶）", top.Count)
	}
	if top.MaxHeight != 3 {
		t.Fatalf("theme[0].MaxHeight = %d, want 3", top.MaxHeight)
	}
	if len(top.Symbols) != 8 || top.Symbols[0] != "U1" {
		t.Fatalf("theme[0].Symbols = %v, want 首 8 只且以 U1 开头", top.Symbols)
	}
	if f.ThemeTree[1].Reason != "a" || f.ThemeTree[1].Count != 1 {
		t.Fatalf("theme[1] = %+v, want a×1", f.ThemeTree[1])
	}
}

// buildThemeTree 钉样：同排行先按数后按题材名；总数超 20 只截 top20。
func TestThemeTreeTop20(t *testing.T) {
	ups := []PoolRow{}
	// 21 个不同题材各 1 只，其中 t00 再加 1 只成 2 只 → 排头；t20 应被挤出。
	ups = append(ups, PoolRow{Symbol: "S00a", Pool: "up", Consecutive: 1, Reason: "t00"})
	ups = append(ups, PoolRow{Symbol: "S00b", Pool: "up", Consecutive: 1, Reason: "t00"})
	for i := 1; i <= 20; i++ {
		ups = append(ups, PoolRow{Symbol: "S" + twoDigit(i), Pool: "up", Consecutive: 1, Reason: "t" + twoDigit(i)})
	}
	f := &Frozen{}
	buildThemeTree(f, ups)
	if len(f.ThemeTree) != 20 {
		t.Fatalf("themeTree len = %d, want 20", len(f.ThemeTree))
	}
	if f.ThemeTree[0].Reason != "t00" || f.ThemeTree[0].Count != 2 {
		t.Fatalf("theme[0] = %+v, want t00×2", f.ThemeTree[0])
	}
	for _, it := range f.ThemeTree {
		if it.Reason == "t20" {
			t.Fatalf("t20 应被 top20 截出: %+v", f.ThemeTree)
		}
	}
	if f.ThemeTree[1].Reason != "t01" {
		t.Fatalf("同计数按题材名升序, theme[1] = %q, want t01", f.ThemeTree[1].Reason)
	}
}

func twoDigit(i int) string {
	if i < 10 {
		return "0" + strconv.Itoa(i)
	}
	return strconv.Itoa(i)
}
