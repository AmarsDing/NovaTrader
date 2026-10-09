package rules

import (
	"context"
	"sort"
)

// 硬过滤的淘汰原因。
const (
	DropNoData    = "no_data"
	DropST        = "st"
	DropSuspended = "suspended"
	DropNew       = "new_listing"
	DropAmount    = "low_amount"
	DropLimitUp   = "at_limit_up"
	DropBlacklist = "blacklist"
)

// FunnelParams 是漏斗规则部分的参数。
type FunnelParams struct {
	MinAmount   float64 // 昨日成交额下限，元
	MinListDays int     // 上市自然日下限
	RuleTopN    int     // 规则分排序后保留的数量
}

func DefaultFunnel() FunnelParams {
	return FunnelParams{MinAmount: 5e7, MinListDays: 90, RuleTopN: 100}
}

// LoadFunnel 从参数表读取，键名见 M06 设计文档第 13 节。
func LoadFunnel(get Lookup) FunnelParams {
	d := DefaultFunnel()
	return FunnelParams{
		MinAmount:   pick(get, "m06.min_amount", d.MinAmount),
		MinListDays: int(pick(get, "m06.min_list_days", float64(d.MinListDays))),
		RuleTopN:    int(pick(get, "m06.rule_top_n", float64(d.RuleTopN))),
	}
}

// HardFilter 返回淘汰原因，通过时返回空字符串。black 为 nil 表示没有黑名单。
// 上市日期未知时不按次新淘汰。
func HardFilter(s Snapshot, p FunnelParams, black func(symbol string) bool) string {
	if len(s.Bars) < 2 {
		return DropNoData
	}
	if s.ST {
		return DropST
	}
	if s.Suspended {
		return DropSuspended
	}
	if black != nil && black(s.Symbol) {
		return DropBlacklist
	}
	if !s.ListDate.IsZero() && s.AsOf.Sub(s.ListDate).Hours() < float64(p.MinListDays)*24 {
		return DropNew
	}
	if s.Bars[len(s.Bars)-1].Amount < p.MinAmount {
		return DropAmount
	}
	if s.Today != nil {
		up, _, err := todayLimits(s)
		if err != nil {
			return DropNoData
		}
		if s.Today.Close >= up-eps {
			return DropLimitUp
		}
	}
	return ""
}

// Candidate 是通过规则打分的一只股票。
type Candidate struct {
	Snapshot Snapshot
	Strategy Strategy
	Score    float64
}

// ScreenResult 是漏斗规则部分的结果。Ranked 已按分数从高到低排好并截断。
type ScreenResult struct {
	Ranked  []Candidate
	Dropped map[string]int
	Matched int
}

// Screen 对全部快照做硬过滤、模板筛选和打分。一只股票命中多个模板时取高分那个。
func Screen(ctx context.Context, snaps []Snapshot, strategies []Strategy, p FunnelParams, black func(string) bool) ScreenResult {
	res := ScreenResult{Dropped: map[string]int{}}
	for _, s := range snaps {
		if reason := HardFilter(s, p, black); reason != "" {
			res.Dropped[reason]++
			continue
		}
		var best *Candidate
		for _, st := range strategies {
			if !st.Filter(ctx, s) {
				continue
			}
			score := clamp(st.Score(ctx, s), 0, 100)
			if best == nil || score > best.Score {
				best = &Candidate{Snapshot: s, Strategy: st, Score: score}
			}
		}
		if best != nil {
			res.Ranked = append(res.Ranked, *best)
		}
	}
	res.Matched = len(res.Ranked)
	sort.SliceStable(res.Ranked, func(i, j int) bool {
		a, b := res.Ranked[i], res.Ranked[j]
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		return a.Snapshot.Symbol < b.Snapshot.Symbol
	})
	if p.RuleTopN > 0 && len(res.Ranked) > p.RuleTopN {
		res.Ranked = res.Ranked[:p.RuleTopN]
	}
	return res
}
