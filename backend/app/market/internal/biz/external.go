// M13-T12 外围/情绪量化因子：截面任务（Checkpoint/Close → persistCross）给每票
// 挂 sector.* / external.* 因子键。
//
// 数据纪律（钉死，验收口径）：
//   - sector.heat / sector.pct_chg 取自当次 persistCross 落库的同一份当日板块热度截面
//     （与 sector_heat 表该 as_of 行同源）；票不在任何上榜板块成分内 → 两键不出，
//     缺键即缺失，禁止 0 冒充（M02 §4.3 注：下游按缺键判不可判定）。
//   - external.usdcnh_chg / external.gold_chg 用 overseas 表最近两个有效收盘实算——
//     M01 上游对汇率 pct_chg 列写死 0（pyworker ak_global），该列禁止继承；
//     收盘缺失/样本不足/取数失败整体缺键缺失（缺键 + 告警留痕），绝不写 0。
package biz

import (
	"context"
	"sort"
	"time"

	"server/library/dataformat"
	"server/pkg/market"
)

// 因子键名与 brain/internal/biz/facts.go factorCatalog 的消费键一一对齐（M05 §59）。
const (
	FactorSectorHeat = "sector.heat"
	FactorSectorPct  = "sector.pct_chg"
	FactorUSDCNHChg  = "external.usdcnh_chg"
	FactorGoldChg    = "external.gold_chg"
)

// externalCodes 是外围因子键 → 候选标的代码（按序取第一个算得出来的）。
// 覆盖 M01 已采与规划代码：汇率 USDCNH（已采，pct_chg 写死 0 故实算）；
// 黄金等 M01 补采后自动生效，无需再改这里。
var externalCodes = []struct {
	key   string
	codes []string
}{
	{FactorUSDCNHChg, []string{"USDCNH", "USDCNY"}},
	{FactorGoldChg, []string{"XAU", "XAUUSD", "GC", "AU9999", "GOLD"}},
}

// attachExternal 给截面行挂外围/板块因子键（M13-T12 写入点）。
// repo 失败只告警：原生截面照常入库，外围键整体缺失（缺键），不阻断落库。
func (u *Usecase) attachExternal(ctx context.Context, day, asOf time.Time, heat []market.SectorHeat, rows []market.FactorRow) {
	ext := u.externalChgs(ctx, day, asOf)
	var bySym map[string]market.SectorHeat
	if len(heat) > 0 {
		u.mu.RLock()
		bySym = sectorBySymbol(u.members, heat)
		u.mu.RUnlock()
	}
	for i := range rows {
		if rows[i].Values == nil {
			continue
		}
		if h, ok := bySym[rows[i].Symbol]; ok {
			rows[i].Values[FactorSectorHeat] = h.Heat
			rows[i].Values[FactorSectorPct] = h.AvgPct
		}
		for k, v := range ext {
			rows[i].Values[k] = v
		}
	}
}

// sectorBySymbol 把每票映射到当日「热度最高」的上榜板块：heat 已由 RankSectors 按
// 热度降序（同热度板块代码升序）排好，首个命中成分的板块即该票归属，确定性。
// 未上榜板块（成分不足等）不产键——缺失不是 0。
func sectorBySymbol(members map[string][]string, heat []market.SectorHeat) map[string]market.SectorHeat {
	bySym := map[string]market.SectorHeat{}
	for _, h := range heat {
		for _, sym := range members[h.SectorCode] {
			if _, ok := bySym[sym]; !ok {
				bySym[sym] = h
			}
		}
	}
	return bySym
}

// externalChgs 从 overseas 表实算外围涨跌幅（%），返回只装算得出的键。
// 取数失败整组缺失：（警告留痕 + 缺键）。窗口取 14 天，跨长假也凑得齐两个交易日收盘。
func (u *Usecase) externalChgs(ctx context.Context, day, asOf time.Time) map[string]float64 {
	quotes, err := u.repo.OverseasQuotes(ctx, day.AddDate(0, 0, -14))
	if err != nil {
		u.log.Warnf("external factors: overseas quotes unavailable: %v", err)
		return nil
	}
	out := map[string]float64{}
	for _, e := range externalCodes {
		r := latestCloseChg(quotes, asOf, e.codes...)
		if v, ok := r.Value(); ok {
			out[e.key] = v
			continue
		}
		u.log.Warnf("external factor %s %s", e.key, r)
	}
	return out
}

// latestCloseChg 按候选代码序返回第一个算得出的涨跌幅；都缺失时保留最有信息量的原因。
func latestCloseChg(quotes []market.Quote, asOf time.Time, codes ...string) dataformat.Result[float64] {
	results := make([]dataformat.Result[float64], 0, len(codes))
	for _, code := range codes {
		results = append(results, closeChg(quotes, asOf, code))
	}
	return dataformat.First(results...)
}

// closeChg 用 code 最近两个「TradeDate 不同、Close>0、AsOf≤asOf」的收盘算涨跌幅%。
// 数据纪律：Close 为 NULL/≤0 或样本不足两个 → 缺失（WhyEmpty）；PctChg 列不参与
// （M01 上游对汇率写死 0，继承即冒充）。同日多条取 AsOf 最晚一行。
func closeChg(quotes []market.Quote, asOf time.Time, code string) dataformat.Result[float64] {
	type dayClose struct {
		close float64
		asOf  time.Time
	}
	byDay := map[string]dayClose{}
	for _, q := range quotes {
		if q.Code != code || q.Close == nil || *q.Close <= 0 {
			continue
		}
		if !asOf.IsZero() && !q.AsOf.IsZero() && q.AsOf.After(asOf) {
			continue // 审计口径：截面 as_of 之后才到手的行不进计算（防回看未来）。
		}
		key := market.DateOf(q.TradeDate).Format("2006-01-02")
		cur, ok := byDay[key]
		if !ok || q.AsOf.After(cur.asOf) {
			byDay[key] = dayClose{close: *q.Close, asOf: q.AsOf}
		}
	}
	if len(byDay) == 0 {
		return dataformat.Missing[float64](dataformat.WhyEmpty, code+" 无有效收盘样本")
	}
	days := make([]string, 0, len(byDay))
	for key := range byDay {
		days = append(days, key)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(days)))
	if len(days) < 2 {
		return dataformat.Missing[float64](dataformat.WhyEmpty, code+" 仅1个有效收盘，无法算涨跌幅")
	}
	cur, prev := byDay[days[0]].close, byDay[days[1]].close
	if prev <= 0 {
		return dataformat.Missing[float64](dataformat.WhyEmpty, code+" 基准收盘非正")
	}
	return dataformat.OK((cur/prev - 1) * 100)
}
