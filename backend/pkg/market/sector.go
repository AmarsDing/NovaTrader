package market

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// SectorWeights 是板块热度的权重，存 strategy_config 的 market.sector.weights。
type SectorWeights struct {
	Pct             float64 `json:"pct"`
	Limit           float64 `json:"limit"`
	Breadth         float64 `json:"breadth"`
	Overseas        float64 `json:"overseas"`
	MinMembers      int     `json:"min_members"`
	LeaderMinAmount float64 `json:"leader_min_amount"`
}

const SectorWeightsKey = "market.sector.weights"

func DefaultSectorWeights() SectorWeights {
	return SectorWeights{Pct: 1, Limit: 1, Breadth: 5, Overseas: 0.5, MinMembers: 5, LeaderMinAmount: 1e8}
}

// ParseSectorWeights 解析配置，缺的字段沿用默认值。
func ParseSectorWeights(raw string) (SectorWeights, error) {
	w := DefaultSectorWeights()
	if raw == "" {
		return w, nil
	}
	if err := json.Unmarshal([]byte(raw), &w); err != nil {
		return DefaultSectorWeights(), fmt.Errorf("market: sector weights: %w", err)
	}
	return w, nil
}

// MemberDay 是板块成分股当天的状态。
type MemberDay struct {
	Symbol      string
	PctChg      float64
	Amount      float64
	Suspended   bool
	UpStatus    string
	Consecutive int
	FirstSealAt time.Time
	SealAmount  float64
}

// SectorHeat 是一个板块的热度。
type SectorHeat struct {
	SectorCode      string  `json:"sector_code"`
	Rank            int     `json:"rank"`
	Heat            float64 `json:"heat"`
	AvgPct          float64 `json:"avg_pct"`
	UpLimitCount    int     `json:"up_limit_count"`
	AdvanceRatio    float64 `json:"advance_ratio"`
	Amount          float64 `json:"amount"`
	MemberCount     int     `json:"member_count"`
	LeaderSymbol    string  `json:"leader_symbol"`
	LeaderReason    string  `json:"leader_reason"`
	OverseasImpulse float64 `json:"overseas_impulse"`
}

// RankSectors 计算热度并排名。members 为板块代码 → 成分股代码，impulse 为外围冲击（可为空）。
// 有效成员不足 MinMembers 的板块不排名。
func RankSectors(members map[string][]string, day map[string]MemberDay, impulse map[string]float64, w SectorWeights) []SectorHeat {
	var out []SectorHeat
	for code, syms := range members {
		h := SectorHeat{SectorCode: code, OverseasImpulse: impulse[code]}
		var sumPct float64
		var adv int
		var limits []MemberDay
		var best *MemberDay
		for _, sym := range syms {
			m, ok := day[sym]
			if !ok || m.Suspended {
				continue
			}
			h.MemberCount++
			sumPct += m.PctChg
			h.Amount += m.Amount
			if m.PctChg > 0 {
				adv++
			}
			if m.UpStatus == StatusSealed {
				h.UpLimitCount++
				limits = append(limits, m)
			}
			if m.Amount >= w.LeaderMinAmount && (best == nil || m.PctChg > best.PctChg) {
				mm := m
				best = &mm
			}
		}
		if h.MemberCount < w.MinMembers {
			continue
		}
		h.AvgPct = sumPct / float64(h.MemberCount)
		h.AdvanceRatio = float64(adv) / float64(h.MemberCount)
		h.Heat = w.Pct*h.AvgPct + w.Limit*float64(h.UpLimitCount) + w.Breadth*h.AdvanceRatio + w.Overseas*h.OverseasImpulse
		if len(limits) > 0 {
			sort.Slice(limits, func(i, j int) bool { return leaderLess(limits[i], limits[j]) })
			h.LeaderSymbol = limits[0].Symbol
			h.LeaderReason = fmt.Sprintf("%d连板", limits[0].Consecutive)
		} else if best != nil {
			h.LeaderSymbol = best.Symbol
			h.LeaderReason = "涨幅最高"
		}
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Heat != out[j].Heat {
			return out[i].Heat > out[j].Heat
		}
		return out[i].SectorCode < out[j].SectorCode
	})
	for i := range out {
		out[i].Rank = i + 1
	}
	return out
}

// leaderLess：连板高者优先；同高首次封板早者优先；再比封单额。
func leaderLess(a, b MemberDay) bool {
	if a.Consecutive != b.Consecutive {
		return a.Consecutive > b.Consecutive
	}
	if !a.FirstSealAt.Equal(b.FirstSealAt) {
		if a.FirstSealAt.IsZero() {
			return false
		}
		if b.FirstSealAt.IsZero() {
			return true
		}
		return a.FirstSealAt.Before(b.FirstSealAt)
	}
	if a.SealAmount != b.SealAmount {
		return a.SealAmount > b.SealAmount
	}
	return a.Symbol < b.Symbol
}
