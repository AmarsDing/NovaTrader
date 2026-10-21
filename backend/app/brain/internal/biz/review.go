// review.go：收盘定稿冻结（M02 情绪读数存档 + `daily_review` 终于有写入方）与程序版每日复盘。
// 纪律：没取到写 missing，不落 0（对齐「没取到 ≠ 0」的数据纪律，外部参考 vibe-astock）。
package biz

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"server/pkg/tradecal"
)

// SentimentRow 是 market_sentiment 某日收盘截面。
type SentimentRow struct {
	AsOf    time.Time
	Advance int
	Decline int
	Amount  float64
}

// indexSymbols 参与「指数中长期位置」的宽基。日线源里取不到的自动进 missing。
var indexSymbols = []struct {
	Symbol string
	Name   string
}{
	{"000001.SH", "上证指数"},
	{"399001.SZ", "深证成指"},
	{"399006.SZ", "创业板指"},
	{"000300.SH", "沪深300"},
}

// FreezeEmotion 收盘定稿：昨/今 limit_pool + 今日日线 → emotion_day_frozen。
// 幂等：同一天可重跑（更新解冻行）。
func (uc *Usecase) FreezeEmotion(ctx context.Context, day time.Time) (*Frozen, error) {
	day = shDate(day)
	yday, err := uc.cal.PrevOpen(day)
	if err != nil {
		return nil, err
	}
	todayPool, err := uc.reviews.PoolOn(ctx, day)
	if err != nil {
		return nil, err
	}
	ydayPool, err := uc.reviews.PoolOn(ctx, yday)
	if err != nil {
		return nil, err
	}
	ydayUps := filterPool(ydayPool, "up")
	symbols := make([]string, 0, len(ydayUps))
	for _, r := range ydayUps {
		symbols = append(symbols, r.Symbol)
	}
	bars, err := uc.reviews.BarOn(ctx, symbols, day)
	if err != nil {
		return nil, err
	}
	f := computeFrozen(todayPool, ydayPool, bars, "limit_pool")
	f.Day = day
	if err := uc.reviews.UpsertFrozen(ctx, f); err != nil {
		return nil, err
	}
	return f, nil
}

// WriteProgramReview 程序版每日复盘（八格）。先确保当天定稿已冻结，再汇总情绪截面、
// 两融、指数位置，写 daily_reviews(kind=program)。当天可重跑覆盖。
func (uc *Usecase) WriteProgramReview(ctx context.Context, day time.Time) error {
	day = shDate(day)
	frozen, err := uc.reviews.FrozenOn(ctx, day)
	if err != nil {
		return err
	}
	if frozen == nil {
		if _, err = uc.FreezeEmotion(ctx, day); err != nil {
			return err
		}
		if frozen, err = uc.reviews.FrozenOn(ctx, day); err != nil {
			return err
		}
	}
	senti, err := uc.reviews.SentimentOn(ctx, day)
	if err != nil {
		return err
	}
	amounts, err := uc.reviews.SentimentAmounts(ctx, day, 60)
	if err != nil {
		return err
	}
	yday5 := day
	for i := 0; i < 5; i++ {
		if p, err2 := uc.cal.PrevOpen(yday5); err2 == nil {
			yday5 = p
		}
	}
	marginNow, marginOK, err := uc.reviews.MarginTotal(ctx, day)
	if err != nil {
		return err
	}
	var marginPrev float64
	if marginOK {
		if p, ok, err2 := uc.reviews.MarginTotal(ctx, yday5); err2 != nil {
			return err2
		} else if !ok {
			marginOK = false
		} else {
			marginPrev = p
		}
	}

	stats := map[string]any{"missing": []string{}}
	missingSet := map[string]bool{}
	for _, m := range frozen.Missing {
		missingSet[m] = true
	}

	var b strings.Builder
	wr := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }

	// 格 1：涨跌家数
	if senti != nil {
		wr("## 涨跌家数")
		wr("上涨 %d / 下跌 %d，两市成交额 %.0f 亿。", senti.Advance, senti.Decline, senti.Amount/1e8)
		stats["advance"] = senti.Advance
		stats["decline"] = senti.Decline
		stats["amount"] = senti.Amount
	} else {
		wr("## 涨跌家数\n没取到（market_sentiment 无当日记录）")
		missingSet["market_sentiment"] = true
	}
	// 格 2：成交额 20/60 日均比
	if len(amounts) >= 20 && senti != nil {
		avg20 := mean(amounts[:20])
		stats["amount_avg20_ratio"] = r2(senti.Amount / avg20)
		if len(amounts) >= 60 {
			stats["amount_avg60_ratio"] = r2(senti.Amount / mean(amounts[:60]))
			wr("成交额为 20 日均值的 %.2f 倍、60 日均值的 %.2f 倍。", stats["amount_avg20_ratio"], stats["amount_avg60_ratio"])
		} else {
			wr("成交额为 20 日均值的 %.2f 倍（60 日均值样本不足）。", stats["amount_avg20_ratio"])
		}
	} else if senti != nil {
		missingSet["amount_history"] = true
	}
	// 格 3：涨停结构与封板率
	wr("## 涨停结构")
	if frozen.UpCount != nil {
		// 「没取到≠0」：封板率无分母（零涨停零炸板）时显式不适用，stats 不输出该键。
		sealParts := "封板率不适用（无涨停无炸板）"
		if frozen.SealRate != nil {
			sealParts = fmt.Sprintf("封板率 %.2f%%", *frozen.SealRate)
		}
		wr("涨停 %d / 跌停 %d / 炸板 %d；%s，最高连板 %d 板。",
			*frozen.UpCount, *frozen.DownCount, *frozen.BrokenCount, sealParts, derefI(frozen.MaxHeight))
		stats["up"], stats["down"], stats["broken"] = *frozen.UpCount, *frozen.DownCount, *frozen.BrokenCount
		stats["max_height"] = derefI(frozen.MaxHeight)
		if frozen.SealRate != nil {
			stats["seal_rate"] = *frozen.SealRate
		}
		if len(frozen.StructureGaps) > 0 {
			wr("梯队断层：%s 板。", joinInts(frozen.StructureGaps))
			stats["structure_gaps"] = frozen.StructureGaps
		}
		if frozen.SealQ != nil {
			wr("封板质量：一直没炸 %d 只，炸后回封 %d 只，10 点前早封 %d 只。",
				frozen.SealQ.NeverBroken, frozen.SealQ.Resealed, frozen.SealQ.EarlySeal)
		}
	} else {
		wr("没取到（limit_pool 无当日记录）")
	}
	// 格 4：昨日涨停今表现
	wr("## 昨日涨停今表现")
	if frozen.TierAvg != nil {
		// 「没取到≠0」：中位/翻红/再板样本不足时显式"不适用"，stats 不输出该键。
		wr("平均 %.2f%%，中位 %s，翻红 %s，再度涨停 %s。",
			*frozen.TierAvg, pctOrNA(frozen.TierMedian), pctOrNA(frozen.TierRedRate), pctOrNA(frozen.TierRelimit))
		stats["tier_avg"] = *frozen.TierAvg
		if frozen.TierMedian != nil {
			stats["tier_median"] = *frozen.TierMedian
		}
		if frozen.TierRedRate != nil {
			stats["tier_red_rate"] = *frozen.TierRedRate
		}
		if frozen.TierRelimit != nil {
			stats["tier_relimit"] = *frozen.TierRelimit
		}
	} else {
		wr("没取到（昨日池或今日日线缺失）")
	}
	// 格 5：晋级
	wr("## 晋级")
	if frozen.Promo12 != nil {
		wr("1进2 %.2f%%；2进3 %s；高位晋级 %s。",
			*frozen.Promo12, pctOrNA(frozen.Promo23), pctOrNA(frozen.PromoHigh))
		stats["promo_12"] = *frozen.Promo12
		if frozen.Promo23 != nil {
			stats["promo_23"] = *frozen.Promo23
		}
		if frozen.PromoHigh != nil {
			stats["promo_high"] = *frozen.PromoHigh
		}
	} else {
		wr("没取到（昨日无连板样本）")
	}
	// 格 6：两融
	wr("## 两融")
	if marginOK && marginPrev > 0 {
		chg := r2((marginNow - marginPrev) / marginPrev * 100)
		wr("融资融券余额 %.0f 亿，较 5 个交易日前 %+0.2f%%。", marginNow/1e8, chg)
		stats["margin_total"] = marginNow
		stats["margin_chg_5d"] = chg
	} else {
		wr("没取到（margin_detail 无当日或前 5 日记录）")
		missingSet["margin"] = true
	}
	// 格 7：指数中长期位置（20/60/250 日线上方只数）
	wr("## 指数位置")
	above := map[int]int{}
	total := 0
	for _, ix := range indexSymbols {
		bars, err2 := uc.market.DailyBars(ctx, ix.Symbol, day, 255)
		if err2 != nil {
			return err2
		}
		if len(bars) < 20 {
			continue
		}
		total++
		for _, n := range []int{20, 60, 250} {
			if len(bars) >= n {
				if bars[len(bars)-1].Close >= meanBars(bars[len(bars)-n:]) {
					above[n]++
				}
			}
		}
	}
	if total == 0 {
		wr("没取到（指数日线不可用，去 market_data 查 1d 数据）")
		missingSet["index_bars"] = true
	} else {
		stats["index_above_20"] = above[20]
		stats["index_above_60"] = above[60]
		stats["index_above_250"] = above[250]
		wr("%d 个宽基中，站上 20/60/250 日线的分别有 %d/%d/%d 个。", total, above[20], above[60], above[250])
	}
	// 格 8：题材结构（top 5）
	if len(frozen.ThemeTree) > 0 {
		wr("## 题材")
		top := frozen.ThemeTree
		if len(top) > 5 {
			top = top[:5]
		}
		names := make([]string, 0, len(top))
		for _, t := range top {
			names = append(names, fmt.Sprintf("%s(%d)", t.Reason, t.Count))
		}
		wr("涨停归并题材前五：%s。", strings.Join(names, "、"))
		stats["themes_top5"] = names
	}

	missing := make([]string, 0, len(missingSet))
	for m := range missingSet {
		missing = append(missing, m)
	}
	sort.Strings(missing)
	stats["missing"] = missing
	if frozen.Partial || len(missing) > 0 {
		wr("\n> 没取到的块：%s。", strings.Join(missing, "、"))
	}

	// M13-T07②：落盘与 strategy.review 事件同一事务（M10 通知/桌面推送消费）。
	title := fmt.Sprintf("程序版复盘 %s", day.Format("01.02"))
	return uc.reviews.SaveProgramReviewWithEvent(ctx, day, b.String(), stats, newTrace(""), title)
}

// FrozenEmotionData 供 API 读取某日收盘定稿。未冻结的报 ErrNotFound。
func (uc *Usecase) FrozenEmotionData(ctx context.Context, day time.Time) (*Frozen, error) {
	f, err := uc.reviews.FrozenOn(ctx, shDate(day))
	if err != nil {
		return nil, err
	}
	if f == nil {
		return nil, fmt.Errorf("%w: %s 无收盘定稿", ErrNotFound, shDate(day).Format("2006-01-02"))
	}
	return f, nil
}

// ListPredictionChecks 供 API 读最近 n 日对账战绩。
func (uc *Usecase) ListPredictionChecks(ctx context.Context, n int) ([]*Check, error) {
	if n <= 0 || n > 120 {
		n = 20
	}
	return uc.reviews.PredictionChecks(ctx, n)
}

// RunPredictionCheck 手动补跑某日（对账日）明日验证。
func (uc *Usecase) RunPredictionCheck(ctx context.Context, day time.Time) (*Check, error) {
	return uc.VerifyPredictions(ctx, day)
}

// shDate 把任意时刻归一到上海时区的日期零点。
func shDate(t time.Time) time.Time {
	t = t.In(tradecal.Shanghai())
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, tradecal.Shanghai())
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := 0.0
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

func meanBars(bars []Bar) float64 {
	if len(bars) == 0 {
		return 0
	}
	s := 0.0
	for _, b := range bars {
		s += b.Close
	}
	return s / float64(len(bars))
}

// derefF 哨兵转换：nil → 0，只用于消费方把 0 当缺失的场合。
// 对外输出（复盘文案/stats 键）禁止走这里——nil 必须显式缺失（sealText / stats 不输出键）。
func derefF(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

func derefI(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func pctOrNA(p *float64) string {
	if p == nil {
		return "样本不足"
	}
	return fmt.Sprintf("%.2f%%", *p)
}

func joinInts(xs []int) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = fmt.Sprintf("%d", x)
	}
	return strings.Join(parts, "、")
}
