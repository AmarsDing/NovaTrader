// W8c：回测信任条最小判据计算（UI 宪法 R-UI-20/23 配套；契约锚见 W8a trust_bar 注记）。
//
// 判据口径（最小实现）：
//   - blind_verified（盲测）：前瞻自检通过 + 无样本外衰减 + 成交笔数达准入下限
//     + 任务跑在冻结数据集上，四条同时成立才算通过；
//   - pbo_verified（过拟合概率）：滚动前推任务按窗计 OOS 失效率——有效窗（Test.Rounds>0，
//     分位成功判据沿用 48db0d2 语义：无成交窗不作分位样本）≥ PboMinWindows 且
//     失效率（Test.Sharpe<0 占比）< 50% 才算通过。单点 OOS 的 optimize/evolve 无
//     多窗样本，判据位保 false=未验证，不拿单点冒充分位读数。
//
// computeTrustBar 是纯函数：同一份任务+报告得到同一份判据；报告缺失/缺结构时
// 对应判据位保零值（没取到≠0）。CSCV 全量 PBO 实现留账（需按段×候选矩阵）。
package biz

import (
	"strings"
	"time"

	"server/pkg/backtest"
)

// PboMinWindows 是 pbo 判据的最小有效窗数：少于该窗数不产出分位读数（未验证）。
const PboMinWindows = 4

// pboOOSFailureThreshold 是失效率上限：≥ 该值判为过拟合风险未通过。
const pboOOSFailureThreshold = 0.5

// flagOOSDecay 是报告 flags 里「样本外衰减」的固定文案（与 report.go 展示口径同源）。
const flagOOSDecay = "样本外衰减"

// computeTrustBar 由任务与报告计算信任条判据位与已声明的描述字段。
// rep 不得为 nil；调用方对报告缺失/解析失败保持零值并跳过本函数。
func computeTrustBar(t *Task, rep *backtest.Report, cfg Settings) TrustBar {
	tb := TrustBar{
		TradeCount:        int32(rep.Metrics.Rounds),
		DatasetManifestID: firstNonEmpty(rep.DatasetManifestID, t.Spec.ManifestID),
		DatasetHash:       rep.DatasetManifestHash,
	}
	if !t.Start.IsZero() {
		tb.WindowStart = t.Start.Format(time.DateOnly)
	}
	if !t.End.IsZero() {
		tb.WindowEnd = t.End.Format(time.DateOnly)
	}
	if len(rep.Assumptions) > 0 {
		tb.CostModel = strings.Join(rep.Assumptions, "；")
	}
	if c := rep.Config.Fee.CommissionRate; c > 0 {
		tb.FeeBps = c * 10000 // 佣金费率（小数）→ bps；口径见 CostModel 声明。
	}
	// 盲测判据：四条同立。
	lookaheadPass := rep.Lookahead != nil && rep.Lookahead.Pass
	tb.BlindVerified = lookaheadPass &&
		!hasFlag(flagsOf(rep), flagOOSDecay) &&
		rep.Metrics.Rounds >= cfg.MinTrades &&
		strings.TrimSpace(t.Spec.ManifestID) != ""
	// PBO 判据：滚动前推多窗失效率。
	if wf := rep.WalkForward; wf != nil {
		valid, bad := 0, 0
		for _, w := range wf.Windows {
			if w.Test.Rounds <= 0 {
				continue // 无成交窗不作分位样本（分位成功判据沿用 48db0d2：SUCCEEDED>0）。
			}
			valid++
			if w.Test.Sharpe < 0 {
				bad++
			}
		}
		if valid >= PboMinWindows {
			tb.PboVerified = float64(bad)/float64(valid) < pboOOSFailureThreshold
		}
	}
	return tb
}

func hasFlag(flags []string, want string) bool {
	for _, f := range flags {
		if f == want {
			return true
		}
	}
	return false
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
