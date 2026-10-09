package kb

import "sort"

// RRFK 是倒数排名融合的平滑常数。
const RRFK = 60

// Fused 是融合后的一条结果。Ranks[i] 是在第 i 路里的名次（从 1 开始），0 表示没出现。
type Fused struct {
	ID    int
	Score float64
	Ranks []int
}

// FuseRRF 合并多路有序 ID：score = Σ 1/(k+名次)。同分时最好名次靠前者优先，再按 ID。
func FuseRRF(k int, lists ...[]int) []Fused {
	byID := map[int]*Fused{}
	var order []*Fused
	for li, list := range lists {
		seen := map[int]bool{}
		rank := 0
		for _, id := range list {
			if seen[id] {
				continue
			}
			seen[id] = true
			rank++
			f := byID[id]
			if f == nil {
				f = &Fused{ID: id, Ranks: make([]int, len(lists))}
				byID[id] = f
				order = append(order, f)
			}
			f.Ranks[li] = rank
			f.Score += 1 / float64(k+rank)
		}
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, b := order[i], order[j]
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if ba, bb := bestRank(a.Ranks), bestRank(b.Ranks); ba != bb {
			return ba < bb
		}
		return a.ID < b.ID
	})
	out := make([]Fused, len(order))
	for i, f := range order {
		out[i] = *f
	}
	return out
}

func bestRank(ranks []int) int {
	best := 0
	for _, r := range ranks {
		if r > 0 && (best == 0 || r < best) {
			best = r
		}
	}
	if best == 0 {
		return int(^uint(0) >> 1)
	}
	return best
}
