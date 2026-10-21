// Package biz — emotion.go：短线收盘取数的定稿计算。纯函数，无存储依赖，便于单测。
// 口径对齐 vibe-astock 的短线收盘取数（orchestrator/src/finance/shortline/close_fetch.ts）：
// 晋级率严格按昨日连板数分母、今日更高板分子 join symbol 计算；没取到的块记 missing，不落 0。
package biz

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// PoolRow 是 limit_pool 某天某只的一行（M01 数据源池）。Pool 取 up/down/broken。
type PoolRow struct {
	Symbol      string
	Name        string
	Pool        string
	Consecutive int     // 连板数
	Reason      string  // 涨停原因或所属行业
	OpenCount   int     // 炸板次数
	SealAmount  float64 // 封单额，元
	FirstSealAt *time.Time
	LastSealAt  *time.Time
}

// TierBar 某只当日的收盘与前收，用来算昨日梯队今日表现。
type TierBar struct {
	Close    float64
	PreClose float64 // 交易所前收盘；0 表示缺失
}

// LadderItem 天梯里一层的一只。
type LadderItem struct {
	Symbol      string `json:"symbol"`
	Name        string `json:"name"`
	Reason      string `json:"reason,omitempty"`
	OpenCount   int    `json:"open_count"`
	FirstSealAt string `json:"first_seal_at,omitempty"`
}

// FirstBoardItem 首板名单一只。
type FirstBoardItem struct {
	Symbol      string  `json:"symbol"`
	Name        string  `json:"name"`
	Reason      string  `json:"reason,omitempty"`
	SealAmount  float64 `json:"seal_amount"`
	FirstSealAt string  `json:"first_seal_at,omitempty"`
}

// ThemeItem 题材归并一条（reason 计数 + 代表股）。
type ThemeItem struct {
	Reason    string   `json:"reason"`
	Count     int      `json:"count"`
	MaxHeight int      `json:"max_height"`
	Symbols   []string `json:"symbols"`
}

// PrevTierItem 昨日梯队今日表现一只。
type PrevTierItem struct {
	Symbol  string  `json:"symbol"`
	Name    string  `json:"name"`
	Pct     float64 `json:"pct"`
	Relimit bool    `json:"relimit"`
}

// SealQuality 封板质量。
type SealQuality struct {
	NeverBroken int `json:"never_broken"` // 涨停且 open_count==0
	Resealed    int `json:"resealed"`     // 炸过又封住
	EarlySeal   int `json:"early_seal"`   // 10:00 前首次封板
}

// Frozen 一日定稿读数。标量用指针表示「没取到」，payload 各自可为空。
type Frozen struct {
	Day           time.Time
	UpCount       *int
	DownCount     *int
	BrokenCount   *int
	SealRate      *float64
	MaxHeight     *int
	Promo12       *float64
	Promo23       *float64
	PromoHigh     *float64
	TierAvg       *float64
	TierMedian    *float64
	TierRedRate   *float64
	TierRelimit   *float64
	Ladder        map[string][]LadderItem
	FirstBoard    []FirstBoardItem
	ThemeTree     []ThemeItem
	PrevTier      []PrevTierItem
	SealQ         *SealQuality
	StructureGaps []int
	Partial       bool
	Missing       []string
	Source        string
	CapturedAt    time.Time // 冻结时刻；computeFrozen 计算时不填，读库时回填
}

func intPtr(v int) *int         { return &v }
func f64Ptr(v float64) *float64 { return &v }
func r2(v float64) float64      { return math.Round(v*100) / 100 }

// computeFrozen 由昨今两池与今日日线计算定稿读数。
// bars 只覆盖昨日涨停股即可；缺的只数计入 missing 名单。
func computeFrozen(day, yday []PoolRow, bars map[string]TierBar, source string) *Frozen {
	f := &Frozen{
		Ladder:     map[string][]LadderItem{},
		FirstBoard: []FirstBoardItem{},
		ThemeTree:  []ThemeItem{},
		PrevTier:   []PrevTierItem{},
		Source:     source,
	}
	todayUps := filterPool(day, "up")
	ydayUps := filterPool(yday, "up")

	if len(day) == 0 {
		f.Partial = true
		f.Missing = append(f.Missing, "limit_pool")
	} else {
		ups, downs, brokens := len(todayUps), len(filterPool(day, "down")), len(filterPool(day, "broken"))
		f.UpCount, f.DownCount, f.BrokenCount = intPtr(ups), intPtr(downs), intPtr(brokens)
		if ups+brokens > 0 {
			f.SealRate = f64Ptr(r2(float64(ups) / float64(ups+brokens) * 100))
		}
		maxH := 0
		for _, r := range todayUps {
			if r.Consecutive > maxH {
				maxH = r.Consecutive
			}
		}
		f.MaxHeight = intPtr(maxH)
		buildLadder(f, todayUps)
		buildFirstBoard(f, todayUps)
		buildThemeTree(f, todayUps)
		f.SealQ = buildSealQuality(todayUps)
		f.StructureGaps = buildGaps(f.Ladder, maxH)
		computePromotion(f, ydayUps, todayUps)
	}
	if len(ydayUps) == 0 {
		f.Partial = true
		f.Missing = append(f.Missing, "limit_pool_prev")
	} else if len(day) > 0 {
		computePrevTier(f, ydayUps, bars, internalUpSet(todayUps))
	}
	if len(f.Missing) > 0 {
		f.Partial = true
	}
	return f
}

func filterPool(rows []PoolRow, pool string) []PoolRow {
	out := make([]PoolRow, 0, len(rows))
	for _, r := range rows {
		if r.Pool == pool {
			out = append(out, r)
		}
	}
	return out
}

func internalUpSet(rows []PoolRow) map[string]bool {
	set := map[string]bool{}
	for _, r := range rows {
		set[r.Symbol] = true
	}
	return set
}

// buildLadder 连板天梯：按高度分组，同高度按首封时间排（早封在前，空时间排最后）。
func buildLadder(f *Frozen, ups []PoolRow) {
	byHeight := map[int][]LadderItem{}
	for _, r := range ups {
		h := r.Consecutive
		if h < 1 {
			h = 1
		}
		byHeight[h] = append(byHeight[h], LadderItem{
			Symbol: r.Symbol, Name: r.Name, Reason: r.Reason, OpenCount: r.OpenCount,
			FirstSealAt: fmtHM(r.FirstSealAt),
		})
	}
	heights := make([]int, 0, len(byHeight))
	for h := range byHeight {
		heights = append(heights, h)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(heights)))
	for _, h := range heights {
		items := byHeight[h]
		sort.SliceStable(items, func(i, j int) bool {
			a, b := items[i].FirstSealAt, items[j].FirstSealAt
			if a == "" || b == "" {
				return b == ""
			}
			return a < b
		})
		f.Ladder[strconv.Itoa(h)] = items
	}
}

func buildFirstBoard(f *Frozen, ups []PoolRow) {
	items := []FirstBoardItem{}
	for _, r := range ups {
		if r.Consecutive > 1 {
			continue
		}
		items = append(items, FirstBoardItem{
			Symbol: r.Symbol, Name: r.Name, Reason: r.Reason, SealAmount: r.SealAmount,
			FirstSealAt: fmtHM(r.FirstSealAt),
		})
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i].FirstSealAt, items[j].FirstSealAt
		if a == "" || b == "" {
			return b == ""
		}
		return a < b
	})
	f.FirstBoard = items
}

// buildThemeTree 按涨停原因归并计数，取 top 20；reason 为空的进「未标注」。
func buildThemeTree(f *Frozen, ups []PoolRow) {
	groups := map[string]*ThemeItem{}
	for _, r := range ups {
		reason := strings.TrimSpace(r.Reason)
		if reason == "" {
			reason = "未标注"
		}
		g := groups[reason]
		if g == nil {
			g = &ThemeItem{Reason: reason, Symbols: []string{}}
			groups[reason] = g
		}
		g.Count++
		if r.Consecutive > g.MaxHeight {
			g.MaxHeight = r.Consecutive
		}
		if len(g.Symbols) < 8 {
			g.Symbols = append(g.Symbols, r.Symbol)
		}
	}
	items := make([]ThemeItem, 0, len(groups))
	for _, g := range groups {
		items = append(items, *g)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Count != items[j].Count {
			return items[i].Count > items[j].Count
		}
		return items[i].Reason < items[j].Reason
	})
	if len(items) > 20 {
		items = items[:20]
	}
	f.ThemeTree = items
}

func buildSealQuality(ups []PoolRow) *SealQuality {
	q := &SealQuality{}
	for _, r := range ups {
		if r.OpenCount == 0 {
			q.NeverBroken++
		} else {
			q.Resealed++
		}
		if r.FirstSealAt != nil && r.FirstSealAt.Hour() < 10 {
			q.EarlySeal++
		}
	}
	return q
}

// buildGaps 2 板及以上区间里缺失的连板层，如 {4板起，无3板} → [3]。
func buildGaps(ladder map[string][]LadderItem, maxH int) []int {
	present := map[int]bool{}
	for k := range ladder {
		if h, err := strconv.Atoi(k); err == nil {
			present[h] = true
		}
	}
	gaps := []int{}
	for h := 2; h < maxH; h++ {
		if !present[h] {
			gaps = append(gaps, h)
		}
	}
	return gaps
}

// computePromotion 晋级率：分子=昨日 k 板今日 k+1 板，分母=昨日 k 板。
// 高位晋级率分母=昨日≥3 板，分子=其中今日仍涨停（连板数更高）。
func computePromotion(f *Frozen, ydayUps, todayUps []PoolRow) {
	c1, c2, high, p1, p2, pHigh := 0, 0, 0, 0, 0, 0
	todayConsec := map[string]int{}
	for _, r := range todayUps {
		todayConsec[r.Symbol] = r.Consecutive
	}
	for _, r := range ydayUps {
		switch {
		case r.Consecutive <= 1:
			c1++
			switch todayConsec[r.Symbol] {
			case 2:
				p1++
			}
		case r.Consecutive == 2:
			c2++
			switch todayConsec[r.Symbol] {
			case 3:
				p2++
			}
		default:
			high++
			if todayConsec[r.Symbol] > r.Consecutive {
				pHigh++
			}
		}
	}
	if c1 > 0 {
		f.Promo12 = f64Ptr(r2(float64(p1) / float64(c1) * 100))
	}
	if c2 > 0 {
		f.Promo23 = f64Ptr(r2(float64(p2) / float64(c2) * 100))
	}
	if high > 0 {
		f.PromoHigh = f64Ptr(r2(float64(pHigh) / float64(high) * 100))
	}
}

// computePrevTier 昨日梯队今日表现：pct 优先按交易所前收算，均/中位/翻红/再涨停汇总。
func computePrevTier(f *Frozen, ydayUps []PoolRow, bars map[string]TierBar, todayUpSet map[string]bool) {
	items := make([]PrevTierItem, 0, len(ydayUps))
	pcts := make([]float64, 0, len(ydayUps))
	missingBars := 0
	for _, r := range ydayUps {
		b, ok := bars[r.Symbol]
		if !ok || b.Close <= 0 || b.PreClose <= 0 {
			missingBars++
			continue
		}
		pct := r2((b.Close - b.PreClose) / b.PreClose * 100)
		items = append(items, PrevTierItem{Symbol: r.Symbol, Name: r.Name, Pct: pct, Relimit: todayUpSet[r.Symbol]})
		pcts = append(pcts, pct)
	}
	if missingBars > 0 {
		f.Partial = true
		f.Missing = append(f.Missing, "tier_bars")
	}
	if len(pcts) == 0 {
		return
	}
	sort.Float64s(pcts)
	sum, red := 0.0, 0
	for _, p := range pcts {
		sum += p
		if p > 0 {
			red++
		}
	}
	f.TierAvg = f64Ptr(r2(sum / float64(len(pcts))))
	f.TierMedian = f64Ptr(pcts[len(pcts)/2])
	f.TierRedRate = f64Ptr(r2(float64(red) / float64(len(pcts)) * 100))
	re := 0
	for _, it := range items {
		if it.Relimit {
			re++
		}
	}
	f.TierRelimit = f64Ptr(r2(float64(re) / float64(len(items)) * 100))
	sort.Slice(items, func(i, j int) bool { return items[i].Pct > items[j].Pct })
	f.PrevTier = items
}

func fmtHM(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format("15:04")
}
