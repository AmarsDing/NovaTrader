package sim

import (
	"strings"
	"testing"

	"server/pkg/brokerif"
)

// 撮合语义对齐 FR-09-04（涨停买不到、跌停卖不出、部分成交、费用和滑点）与
// 需求 66 的手工推演场景。Match 是包内纯函数，本文件钉死逐条分支，防撮合口径漂移。

func q(open, high, low float64, vol int64, up, down float64) Quote {
	return Quote{Open: open, High: high, Low: low, Volume: vol, LimitUp: up, LimitDown: down}
}

var mainQ = q(10, 10.2, 9.9, 1_000_000, 11, 9)

func TestMatchRejectsZeroQtyAndSuspendedQuote(t *testing.T) {
	if f := Match("600519.SH", brokerif.Buy, 0, 0, 0, mainQ, DefaultMatchRule()); f.Reason != "数量为 0" {
		t.Fatalf("zero qty: %+v", f)
	}
	if f := Match("600519.SH", brokerif.Buy, 100, 0, 0, q(10, 10.2, 9.9, 0, 11, 9), DefaultMatchRule()); f.Reason != "停牌或无成交" {
		t.Fatalf("zero volume: %+v", f)
	}
	if f := Match("600519.SH", brokerif.Buy, 100, 0, 0, q(0, 10.2, 9.9, 1000, 11, 9), DefaultMatchRule()); f.Reason != "停牌或无成交" {
		t.Fatalf("zero open: %+v", f)
	}
	if f := Match("600519.SH", brokerif.Side("hold"), 100, 0, 0, mainQ, DefaultMatchRule()); f.Reason != "未知方向" {
		t.Fatalf("unknown side: %+v", f)
	}
}

func TestMatchBuySlippageIsOneTickAboveOpen(t *testing.T) {
	f := Match("600519.SH", brokerif.Buy, 1000, 0, 0, mainQ, DefaultMatchRule())
	if f.Qty != 1000 || f.Price != 10.01 || f.Reason != "" {
		t.Fatalf("buy should fill 1000@10.01, got %+v", f)
	}
	// 卖单镜像：开盘减一个最小价位。
	f = Match("600519.SH", brokerif.Sell, 1000, 1000, 0, mainQ, DefaultMatchRule())
	if f.Qty != 1000 || f.Price != 9.99 || f.Reason != "" {
		t.Fatalf("sell should fill 1000@9.99, got %+v", f)
	}
}

func TestMatchLimitOrderHonorsPriceBarrier(t *testing.T) {
	// 买盘卦 9.99 < 撮合价 10.01：价格未到。
	if f := Match("600519.SH", brokerif.Buy, 100, 0, 9.99, mainQ, DefaultMatchRule()); f.Reason != "价格未到" {
		t.Fatalf("buy below match price must not fill: %+v", f)
	}
	// 限价恰好等于撮合价可成（不含缓冲）。
	if f := Match("600519.SH", brokerif.Buy, 100, 0, 10.01, mainQ, DefaultMatchRule()); f.Qty != 100 || f.Price != 10.01 {
		t.Fatalf("buy at exact limit should fill: %+v", f)
	}
	// 卖盘镜像。
	if f := Match("600519.SH", brokerif.Sell, 100, 100, 10.0, mainQ, DefaultMatchRule()); f.Reason != "价格未到" {
		t.Fatalf("sell above match price must not fill: %+v", f)
	}
	if f := Match("600519.SH", brokerif.Sell, 100, 100, 9.99, mainQ, DefaultMatchRule()); f.Qty != 100 || f.Price != 9.99 {
		t.Fatalf("sell at exact limit should fill: %+v", f)
	}
}

func TestMatchLimitUpDownSealedBars(t *testing.T) {
	// 全程封涨停：Low >= LimitUp，买单一律不成交（FR-09-04 涨停买不到）。
	sealedUp := q(11, 11, 11, 1_000_000, 11, 9)
	if f := Match("600519.SH", brokerif.Buy, 100, 0, 0, sealedUp, DefaultMatchRule()); f.Reason != "涨停封板" {
		t.Fatalf("sealed limit-up must reject buys: %+v", f)
	}
	// 全程封跌停卖不出。
	sealedDown := q(9, 9, 9, 1_000_000, 11, 9)
	if f := Match("600519.SH", brokerif.Sell, 100, 100, 0, sealedDown, DefaultMatchRule()); f.Reason != "跌停封板" {
		t.Fatalf("sealed limit-down must reject sells: %+v", f)
	}
}

func TestMatchSlipIntoLimitRespectsLimitFillRule(t *testing.T) {
	// 开盘+滑点被钳到涨停价；K 线打开过（Low < LimitUp）。
	up := q(10.04, 10.05, 9.95, 100_000, 10.05, 9.05)
	f := Match("600519.SH", brokerif.Buy, 100, 0, 0, up, DefaultMatchRule())
	if f.Qty != 100 || f.Price != 10.05 {
		t.Fatalf("opened rule fills at clamped limit-up: %+v", f)
	}
	never := MatchRule{SlippageTicks: 1, VolumeCap: 0.1, LimitFill: LimitFillNever}
	if f = Match("600519.SH", brokerif.Buy, 100, 0, 0, up, never); f.Reason != "涨停不排队" {
		t.Fatalf("never rule must not assume queue priority: %+v", f)
	}
	// 卖盘镜像：开盘-滑点钳到跌停。
	down := q(9.01, 9.5, 9.0, 100_000, 11, 9)
	f = Match("600519.SH", brokerif.Sell, 100, 100, 0, down, DefaultMatchRule())
	if f.Qty != 100 || f.Price != 9.0 {
		t.Fatalf("opened rule fills at clamped limit-down: %+v", f)
	}
	if f = Match("600519.SH", brokerif.Sell, 100, 100, 0, down, never); f.Reason != "跌停不排队" {
		t.Fatalf("never rule must not assume queue priority: %+v", f)
	}
}

func TestMatchVolumeCapFloorsToLegalLot(t *testing.T) {
	// 量能上限 300 刚好整手：买单缩量成交 300。
	lowVol := q(10, 10.2, 9.9, 3_000, 11, 9)
	if f := Match("600519.SH", brokerif.Buy, 500, 0, 0, lowVol, DefaultMatchRule()); f.Qty != 300 {
		t.Fatalf("cap 300 should fill 300: %+v", f)
	}
	// 150 → 主板归整 100。
	lowVol.Volume = 1_500
	if f := Match("600519.SH", brokerif.Buy, 500, 0, 0, lowVol, DefaultMatchRule()); f.Qty != 100 {
		t.Fatalf("cap 150 should floor to 100: %+v", f)
	}
	// 99 → 不足一手整单作废。
	lowVol.Volume = 990
	if f := Match("600519.SH", brokerif.Buy, 500, 0, 0, lowVol, DefaultMatchRule()); f.Qty != 0 || f.Reason != "量能不足" {
		t.Fatalf("cap 99 should not partially fill: %+v", f)
	}
	// 卖盘缩量同样归整：250 → 200。
	lowVol.Volume = 2_500
	if f := Match("600519.SH", brokerif.Sell, 1_000, 1_000, 0, lowVol, DefaultMatchRule()); f.Qty != 200 {
		t.Fatalf("sell cap 250 should floor to 200: %+v", f)
	}
	// 主板余股一次卖完合法：50 股全清（n==want==available 不归整）。
	if f := Match("600519.SH", brokerif.Sell, 50, 50, 0, mainQ, DefaultMatchRule()); f.Qty != 50 {
		t.Fatalf("odd-lot full exit must be allowed: %+v", f)
	}
}

func TestMatchOddLotBoards(t *testing.T) {
	lowVol := q(10, 10.2, 9.9, 1_500, 11, 9)
	// 科创板缩量 150：既不足起卖 200 又非全清，整单量能不足。
	if f := Match("688001.SH", brokerif.Sell, 500, 500, 0, lowVol, DefaultMatchRule()); f.Qty != 0 || f.Reason != "量能不足" {
		t.Fatalf("STAR partial below board minimum must not fill: %+v", f)
	}
	// 北交所缩量卖出 350 → 300：roundLot 卖出支（非科创、非全清）按 100 手向下归整——
	// 这是 sim 刻意保守方向（宁可少成不可虚多）；买入侧另走 ashare.RoundBuy
	// 「起买线以上逐股」的不对称规则（见 ashare.go RoundBuy/oddLotBoard 注释及
	// TestMatchBuySideBoardMinimums）。
	lowVol.Volume = 3_500
	if f := Match("430047.BJ", brokerif.Sell, 1_000, 1_000, 0, lowVol, DefaultMatchRule()); f.Qty != 300 {
		t.Fatalf("BJ partial fills conservatively to 100-lots: %+v", f)
	}
}

func TestMatchBuySideBoardMinimums(t *testing.T) {
	// 买入侧手数规则与卖出侧刻意不对称（钉死防漂移）：缩量经 ashare.RoundBuy 归整——
	// 科创板起买 200、北交所起买 100，起买线以上逐股；不足起买归零成「量能不足」。
	// 卖出侧口径（非科创/非全清向 100 手归整）由 TestMatchOddLotBoards 钉死。
	lowVol := q(10, 10.2, 9.9, 1_500, 11, 9) // 量能上限 cap 150
	// 科创板 cap 150 < 起买 200 → 归零。
	if f := Match("688001.SH", brokerif.Buy, 500, 0, 0, lowVol, DefaultMatchRule()); f.Qty != 0 || f.Reason != "量能不足" {
		t.Fatalf("STAR buy below 200 minimum must not partially fill: %+v", f)
	}
	// 北交所 cap 150 >= 起买 100 → 逐股保持 150（买侧不向 100 手归整）。
	if f := Match("430047.BJ", brokerif.Buy, 500, 0, 0, lowVol, DefaultMatchRule()); f.Qty != 150 {
		t.Fatalf("BJ buy above minimum keeps per-share precision: %+v", f)
	}
	// 科创板 cap 250 → 逐股保持 250。
	lowVol.Volume = 2_500
	if f := Match("688001.SH", brokerif.Buy, 500, 0, 0, lowVol, DefaultMatchRule()); f.Qty != 250 {
		t.Fatalf("STAR buy above minimum keeps per-share precision: %+v", f)
	}
	// 北交所 cap 50 < 起买 100 → 归零。
	lowVol.Volume = 500
	if f := Match("430047.BJ", brokerif.Buy, 500, 0, 0, lowVol, DefaultMatchRule()); f.Qty != 0 || f.Reason != "量能不足" {
		t.Fatalf("BJ buy below 100 minimum must not partially fill: %+v", f)
	}
}

func TestMatchZeroBoundsNoSealNoClamp(t *testing.T) {
	// 无涨跌幅数据（未知限价）本不该到达本层：FR-09-24「未知涨跌停边界不得撮合或占用
	// 游标」落在编排层入口——trade runner.go:314-318（!known → Rejected）与其
	// runner_test.go missingLimits 用例已有见证。成交/拒单在零边界下不是本层承诺的
	// 合同，本例只钉两条实现性质防回归：① 零边界不产生「封板/不排队」幻影拒绝；
	// ② Clamp 的 up>0/down>0 守卫不钳出零价。
	noLimits := q(10, 10.2, 9.9, 1_000_000, 0, 0)
	for _, side := range []brokerif.Side{brokerif.Buy, brokerif.Sell} {
		f := Match("600519.SH", side, 100, 100, 0, noLimits, DefaultMatchRule())
		if strings.Contains(f.Reason, "封板") || strings.Contains(f.Reason, "不排队") {
			t.Fatalf("zero bounds must not phantom-seal: %+v", f)
		}
		if f.Qty > 0 && f.Price <= 0 {
			t.Fatalf("clamp to zero price is a bug: %+v", f)
		}
	}
}

func TestMatchReasonsAreDistinct(t *testing.T) {
	// 拒绝原因被上游展示/记录用，必须互不混淆：全部去重且非空。
	seen := map[string]bool{}
	cases := []Fill{
		Match("600519.SH", brokerif.Buy, 0, 0, 0, mainQ, DefaultMatchRule()),
		Match("600519.SH", brokerif.Buy, 100, 0, 0, q(10, 10, 10, 0, 11, 9), DefaultMatchRule()),
		Match("600519.SH", brokerif.Buy, 100, 0, 9.99, mainQ, DefaultMatchRule()),
		Match("600519.SH", brokerif.Buy, 100, 0, 0, q(11, 11, 11, 1000, 11, 9), DefaultMatchRule()),
		Match("600519.SH", brokerif.Side("hold"), 100, 0, 0, mainQ, DefaultMatchRule()),
		Match("600519.SH", brokerif.Sell, 1000, 1000, 0, q(10, 10, 10, 990, 11, 9), DefaultMatchRule()),
	}
	for _, f := range cases {
		if f.Qty != 0 || f.Reason == "" {
			t.Fatalf("rejections carry qty 0 + reason: %+v", f)
		}
		if seen[f.Reason] {
			t.Fatalf("duplicate reason %q across distinct branches", f.Reason)
		}
		seen[f.Reason] = true
	}
	// 全部原因不含 fmt 占位残痕（供日志直接引用）。
	for r := range seen {
		if strings.Contains(r, "%") {
			t.Fatalf("reason %q must be fully formatted", r)
		}
	}
}
