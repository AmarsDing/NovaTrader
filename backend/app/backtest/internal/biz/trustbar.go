// W8c：回测信任条最小判据计算（UI 宪法 R-UI-20/23 配套；契约锚见 W8a trust_bar 注记）。
//
// 判据口径（最小实现）：
//   - blind_verified（盲测）：前瞻自检通过 + 无样本外衰减 + 成交笔数达准入下限
//     + 任务跑在冻结数据集上，四条同时成立才算通过；
//   - pbo_verified（过拟合概率）：按报告形态取源——optimize/evolve 走 CSCV
//     （evaluate 随评估现采候选×段矩阵，见 pkg/backtest/segments.go；段缺失/
//     有效候选不足/不可算一律未验证，objective≠sharpe 时矩阵度量的选择过程与
//     任务实际选择错位，同样保未验证不假绿）；walkforward 按窗计 OOS 失效率——
//     有效窗（Test.Rounds>0，分位成功判据沿用 48db0d2 语义：无成交窗不作分位样本）
//     ≥ PboMinWindows 且失效率（Test.Sharpe<0 占比）< 50% 才算通过。
//
// computeTrustBar 是纯函数：同一份任务+报告得到同一份判据；报告缺失/缺结构时
// 对应判据位保零值（没取到≠0）。
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

// pboCSCVThreshold 是 CSCV PBO 的通过线：PBO ≥ 该值判未过（最优参数靠运气的概率过半）。
// 与 pboOOSFailureThreshold 分属两种判据（前者=单点搜索的候选×段 PBO，后者=滚动
// 前推窗失效率），凑巧同值但语义独立，别并成一个数。
const pboCSCVThreshold = 0.5

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
	// PBO 判据按任务数据形态取源（一份报告只挂一类搜索结果，分支互斥）：
	//   - optimize/evolve（单点搜索）：候选×段矩阵走 CSCV（W8c 接线；段指标缺失/
	//     有效候选不足/计算失败 → 保未验证，不回填不硬解）；
	//   - walkforward（滚动前推）：有效窗 OOS 失效率（口径沿用 48db0d2 分位成功判据）。
	switch {
	case rep.Optimize != nil:
		tb.PboVerified = cscvVerified(rep.Optimize.Points, rep.Optimize.Objective)
	case rep.Evolve != nil:
		tb.PboVerified = cscvVerified(rep.Evolve.Points, rep.Evolve.Objective)
	case rep.WalkForward != nil:
		valid, bad := 0, 0
		for _, w := range rep.WalkForward.Windows {
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

// cscvVerified 用候选×段时间绩效矩阵走 CSCV（W8c.b 引擎）判 PBO 是否过线。
// 判据池 = 有效候选（best() 只在有效点里选——PBO 问的是「这次实际选择靠不靠运气」，
// 无效点不在选择集内，也就不进矩阵）；段缺失/长度不齐的行剔除（存量旧报告未带段
// 指标 → 不可判 → false 未验证；宁缺毋滥，不截断统计）。矩阵按段 Sharpe 排名，
// 任务用其他 objective（annual/calmar）做出的选择与矩阵度量错位，判据不冒名认证，
// 保未验证（空 objective = 旧报告/默认口径，按 sharpe 放行）。CSCV 报形态/不足错
// 一律 false——判据不可算与判据不过线同显「未验证」，前端永远不见假绿（R-UI-20/23）。
func cscvVerified(pts []backtest.Point, objective string) bool {
	if objective != "" && objective != "sharpe" {
		return false
	}
	seg := 0
	for _, p := range pts {
		if p.Valid && len(p.Segments) >= 4 {
			seg = len(p.Segments)
			break
		}
	}
	if seg == 0 {
		return false
	}
	matrix := make([][]float64, 0, len(pts))
	for _, p := range pts {
		if !p.Valid || len(p.Segments) != seg {
			continue
		}
		matrix = append(matrix, p.Segments)
	}
	res, err := backtest.CSCV(matrix)
	if err != nil {
		return false
	}
	return res.PBO < pboCSCVThreshold
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
