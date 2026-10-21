package biz

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"server/pkg/llm"
	"server/pkg/observe"
)

// review_ai.go：分段式 AI 复盘（P1-7，对标 vibe-astock 五段口径；P2-8 扩为六段）。
// 情绪 / 资金 / 题材 / 龙虎榜 / 龙头 / 筹码供给各段独立模型会话，每段只喂自己那块数据；
// 汇总段读各段结论写总评与教训要点。P1-6：窗口终点一律封印到复盘日 15:00；
// P1-5：各段正文与汇总过数字三档核对，留痕随 daily_reviews 落库。
// 落库：daily_reviews(kind='ai') 同日覆盖；每个字段没取到都进 missing，不落 0。

// reviewAIKind 是 daily_reviews 中分段 AI 复盘的 kind。
const reviewAIKind = "ai"

type aiSegment string

const (
	segEmotion aiSegment = "emotion" // 情绪：涨停结构、晋级率、昨日梯队
	segCapital aiSegment = "capital" // 资金：成交额均比、涨跌家数、两融
	segTheme   aiSegment = "theme"   // 题材：题材归并与轮动
	segLHB     aiSegment = "lhb"     // 龙虎榜：席位净买卖+席位画像（P2-8），北向十大作补充
	segLeader  aiSegment = "leader"  // 龙头：连板天梯与昨日梯队强势标的
	segSupply  aiSegment = "supply"  // 筹码供给（P2-8）：90 天解禁窗口 + 近 90 天减持公告
)

var aiSegmentOrder = []aiSegment{segEmotion, segCapital, segTheme, segLHB, segLeader, segSupply}

// NorthTop 北向十大成交一只（龙虎榜段北向补充读数）。
type NorthTop struct {
	Symbol  string
	Name    string
	Channel string
	Rank    int
	Net     *float64 // 净买入（元）
	PctChg  *float64 // 当日涨跌幅（%）
	Amount  float64  // 成交额（元）
}

// LhbSeatRow 龙虎榜席位一行（P2-8：已并席位画像）。
type LhbSeatRow struct {
	Symbol   string
	Reason   string
	SeatName string
	Buy      float64 // 元
	Sell     float64 // 元
	Tag      string  // 画像：INSTITUTION/HOT_MONEY/QUANT/NORTH，空串=未画像
	Alias    string  // 画像别名（如游资称号、某某路），空串=无
}

// UnlockRow 一只股票的一次解禁批次（未来窗口内）。Shares 单位万股，比例与市值源没给到为 nil。
type UnlockRow struct {
	Symbol         string
	FreeDate       time.Time
	LiftType       string
	Shares         float64 // 万股
	BatchHolderNum *int
	RatioPct       *float64 // 占流通股本比例（%）
	TotalRatioPct  *float64 // 占总股本比例（%）
	MarketValue    *float64 // 元
}

type segmentOut struct {
	Text     string   `json:"text"`
	Points   []string `json:"points"`
	Evidence []string `json:"evidence"`
}

func (o *segmentOut) check(ids map[string]bool, seg aiSegment) error {
	if err := checkLen(o.Text, 240, string(seg)+".text"); err != nil {
		return err
	}
	if len(o.Points) < 2 || len(o.Points) > 4 {
		return fmt.Errorf("%s.points 应为 2 到 4 条", seg)
	}
	for i, p := range o.Points {
		if err := checkLen(p, 80, fmt.Sprintf("第 %d 条要点", i+1)); err != nil {
			return err
		}
	}
	return checkEvidence(ids, o.Evidence, 1, string(seg))
}

type reviewSynthesisOut struct {
	Summary string   `json:"summary"`
	Lessons []string `json:"lessons"`
}

func (o *reviewSynthesisOut) check() error {
	if err := checkLen(o.Summary, 300, "summary"); err != nil {
		return err
	}
	if len(o.Lessons) < 2 || len(o.Lessons) > 6 {
		return fmt.Errorf("lessons 应为 2 到 6 条")
	}
	for i, l := range o.Lessons {
		if err := checkLen(l, 80, fmt.Sprintf("第 %d 条教训", i+1)); err != nil {
			return err
		}
	}
	return nil
}

// reviewFacts 构造一段的资料包，只放本段相关的数字事实。标量没取到时不放，记 missing（用事实标签留名）。
func derefFact(id, label string, p *float64, unit string) (Fact, bool) {
	if p == nil {
		return Fact{Label: label}, false
	}
	return Fact{ID: id, Label: label, Value: *p, Unit: unit, Dim: External}, true
}

func iFact(id, label string, p *int, unit string) (Fact, bool) {
	if p == nil {
		return Fact{Label: label}, false
	}
	return Fact{ID: id, Label: label, Value: float64(*p), Unit: unit, Dim: External}, true
}

func segEmotionFacts(f *Frozen) (facts []Fact, missing []string) {
	put := func(fx Fact, ok bool) {
		if ok {
			facts = append(facts, fx)
		} else {
			missing = append(missing, fx.Label)
		}
	}
	put(iFact("F:review.emotion.up", "今日涨停家数", f.UpCount, "家"))
	put(iFact("F:review.emotion.down", "今日跌停家数", f.DownCount, "家"))
	put(iFact("F:review.emotion.broken", "今日炸板家数", f.BrokenCount, "家"))
	put(derefFact("F:review.emotion.seal_rate", "封板率", f.SealRate, "%"))
	put(iFact("F:review.emotion.max_height", "最高连板", f.MaxHeight, "板"))
	put(derefFact("F:review.emotion.promo12", "1进2晋级率", f.Promo12, "%"))
	put(derefFact("F:review.emotion.promo23", "2进3晋级率", f.Promo23, "%"))
	put(derefFact("F:review.emotion.promo_high", "高位晋级率", f.PromoHigh, "%"))
	put(derefFact("F:review.emotion.tier_red", "昨日梯队翻红占比", f.TierRedRate, "%"))
	put(derefFact("F:review.emotion.tier_avg", "昨日梯队平均涨幅", f.TierAvg, "%"))
	if len(f.StructureGaps) > 0 {
		facts = append(facts, Fact{ID: "F:review.emotion.gaps", Label: "连板梯队断层", Text: fmt.Sprintf("%v 板断层", f.StructureGaps), Dim: External})
	}
	if f.SealQ != nil {
		facts = append(facts,
			Fact{ID: "F:review.emotion.seal_never", Label: "未炸板封住", Value: float64(f.SealQ.NeverBroken), Unit: "家", Dim: External},
			Fact{ID: "F:review.emotion.seal_early", Label: "10点前首封", Value: float64(f.SealQ.EarlySeal), Unit: "家", Dim: External})
	}
	return facts, missing
}

func (uc *Usecase) segCapitalFacts(ctx context.Context, day time.Time) (facts []Fact, missing []string) {
	senti, err := uc.reviews.SentimentOn(ctx, day)
	if err != nil {
		return nil, []string{"market_sentiment: " + err.Error()}
	}
	if senti == nil {
		return nil, []string{"market_sentiment"}
	}
	facts = append(facts,
		Fact{ID: "F:review.capital.amount", Label: "两市成交额", Value: senti.Amount / 1e8, Unit: "亿", Dim: Capital},
		Fact{ID: "F:review.capital.advance", Label: "上涨家数", Value: float64(senti.Advance), Unit: "家", Dim: Capital},
		Fact{ID: "F:review.capital.decline", Label: "下跌家数", Value: float64(senti.Decline), Unit: "家", Dim: Capital},
	)
	if amounts, err := uc.reviews.SentimentAmounts(ctx, day, 61); err == nil {
		if avg := meanOf(amounts[:min(len(amounts), 20)]); avg > 0 {
			facts = append(facts, Fact{ID: "F:review.capital.amount_r20", Label: "成交额/20日均值", Value: r2(senti.Amount / avg), Dim: Capital})
		}
		if len(amounts) >= 60 {
			if avg := meanOf(amounts[:60]); avg > 0 {
				facts = append(facts, Fact{ID: "F:review.capital.amount_r60", Label: "成交额/60日均值", Value: r2(senti.Amount / avg), Dim: Capital})
			}
		}
	} else {
		missing = append(missing, "amount_avg")
	}
	if total, ok, err := uc.reviews.MarginTotal(ctx, day); err == nil && ok {
		facts = append(facts, Fact{ID: "F:review.capital.margin", Label: "两融余额", Value: total / 1e8, Unit: "亿", Dim: Capital})
	} else {
		missing = append(missing, "margin")
	}
	return facts, missing
}

func segThemeFacts(f *Frozen) (facts []Fact, missing []string) {
	if len(f.ThemeTree) == 0 {
		missing = append(missing, "theme_tree")
		return facts, missing
	}
	limit := min(len(f.ThemeTree), 5)
	for i := 0; i < limit; i++ {
		t := f.ThemeTree[i]
		facts = append(facts, Fact{
			ID: fmt.Sprintf("F:review.theme.%d", i+1), Label: fmt.Sprintf("题材top%d %s", i+1, t.Reason),
			Text: fmt.Sprintf("%d 家涨停，最高 %d 板，代表 %s", t.Count, t.MaxHeight, strings.Join(t.Symbols[:min(len(t.Symbols), 3)], "、")),
			Dim:  External,
		})
	}
	return facts, missing
}

// seatTagDisplay 席位画像中文名：游资带别名。
func seatTagDisplay(tag, alias string) string {
	name := map[string]string{
		"INSTITUTION": "机构", "HOT_MONEY": "游资", "QUANT": "量化", "NORTH": "北向",
	}[tag]
	if name == "" {
		name = "未画像席位"
	}
	if alias != "" {
		name += "「" + alias + "」"
	}
	return name
}

// lhbSeatAgg 按席位聚合净买与涉及个股（segLHBFacts 用）。
type lhbSeatAgg struct {
	net   float64
	syms  map[string]bool
	tag   string
	alias string
	name  string
}

func (uc *Usecase) segLHBFacts(ctx context.Context, day time.Time) (facts []Fact, missing []string) {
	seats, err := uc.reviews.LhbSeatsOn(ctx, day)
	if err != nil {
		missing = append(missing, "lhb_seat: "+err.Error())
	} else if len(seats) > 0 {
		per := map[string]*lhbSeatAgg{}
		var order []string
		var buyTotal, sellTotal float64
		tagNet := map[string]float64{}
		for _, s := range seats {
			a, ok := per[s.SeatName]
			if !ok {
				a = &lhbSeatAgg{syms: map[string]bool{}, tag: s.Tag, alias: s.Alias, name: s.SeatName}
				per[s.SeatName] = a
				order = append(order, s.SeatName)
			}
			a.net += s.Buy - s.Sell
			a.syms[s.Symbol] = true
			buyTotal += s.Buy
			sellTotal += s.Sell
			tagNet[s.Tag] += s.Buy - s.Sell
		}
		facts = append(facts,
			Fact{ID: "F:review.lhb.seats", Label: "当日上榜席位数", Value: float64(len(per)), Unit: "家", Dim: Capital},
			Fact{ID: "F:review.lhb.seat_net", Label: "龙虎榜席位净买合计", Value: r2((buyTotal - sellTotal) / 1e8), Unit: "亿", Dim: Capital},
		)
		ranked := make([]*lhbSeatAgg, 0, len(per))
		for _, seat := range order {
			ranked = append(ranked, per[seat])
		}
		sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].net > ranked[j].net })
		for i := 0; i < min(len(ranked), 2); i++ {
			a := ranked[i]
			if a.net <= 0 {
				break
			}
			facts = append(facts, Fact{
				ID: fmt.Sprintf("F:review.lhb.buy%d", i+1), Label: fmt.Sprintf("席位净买第%d", i+1),
				Text: fmt.Sprintf("%s（%s）净买 %.2f 亿，涉及 %d 只", seatTagDisplay(a.tag, a.alias), a.name, a.net/1e8, len(a.syms)),
				Dim:  Capital,
			})
		}
		if len(ranked) > 0 {
			if a := ranked[len(ranked)-1]; a.net < 0 {
				facts = append(facts, Fact{
					ID: "F:review.lhb.sell1", Label: "席位净卖第1",
					Text: fmt.Sprintf("%s（%s）净卖 %.2f 亿，涉及 %d 只", seatTagDisplay(a.tag, a.alias), a.name, -a.net/1e8, len(a.syms)),
					Dim:  Capital,
				})
			}
		}
		if net, ok := tagNet["HOT_MONEY"]; ok {
			facts = append(facts, Fact{ID: "F:review.lhb.hot_net", Label: "游资席位净买合计", Value: r2(net / 1e8), Unit: "亿", Dim: Capital})
		}
		if net, ok := tagNet["INSTITUTION"]; ok {
			facts = append(facts, Fact{ID: "F:review.lhb.inst_net", Label: "机构席位净买合计", Value: r2(net / 1e8), Unit: "亿", Dim: Capital})
		}
	} else {
		missing = append(missing, "lhb_seat")
	}
	// 北向十大保留作补充通道读数；席位与北向全缺才算整段没取到。
	rows, herr := uc.reviews.HsgtTop10(ctx, day)
	if herr != nil || len(rows) == 0 {
		missing = append(missing, "hsgt_top10")
		return facts, missing
	}
	// 「没取到≠0」：净买未知（nil）参与排序时排后，不得当 0 插队；合计只加计已知行并标注口径。
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i].Net, rows[j].Net
		if a == nil || b == nil {
			return b != nil
		}
		return *a > *b
	})
	var total float64
	knownCount := 0
	for _, r := range rows {
		if r.Net == nil {
			continue
		}
		total += *r.Net
		knownCount++
	}
	if knownCount == len(rows) {
		facts = append(facts, Fact{ID: "F:review.lhb.north_net", Label: "北向十大净买合计", Value: r2(total / 1e8), Unit: "亿", Dim: Capital})
	} else {
		facts = append(facts, Fact{ID: "F:review.lhb.north_net", Label: "北向十大净买合计",
			Text: fmt.Sprintf("已知 %d 只合计 %.2f 亿；%d 只净买未知", knownCount, r2(total/1e8), len(rows)-knownCount), Dim: Capital})
	}
	for i := 0; i < min(len(rows), 2); i++ {
		r := rows[i]
		fact := Fact{ID: fmt.Sprintf("F:review.lhb.north%d", i+1), Label: fmt.Sprintf("北向净买第%d %s %s", i+1, r.Symbol, r.Name), Dim: Capital}
		if r.Net == nil {
			fact.Text = fmt.Sprintf("净买未知（%s 第%d）", r.Channel, r.Rank)
		} else {
			fact.Text = fmt.Sprintf("净买 %.2f 亿（%s 第%d）", *r.Net/1e8, r.Channel, r.Rank)
		}
		facts = append(facts, fact)
	}
	return facts, missing
}

// segSupplyFacts 筹码供给段（P2-8 解禁/减持视角）：
// 未来 90 天解禁窗口（合计、峰值日、重大占比、与当日涨停股的重叠）+ 近 90 天减持公告。
// 窗口内 0 行分两种：解禁域近日采集成功过 → 「真无解禁」记文本事实；没采过 → missing，不落 0。
func (uc *Usecase) segSupplyFacts(ctx context.Context, day, seal time.Time, frozen *Frozen) (facts []Fact, missing []string) {
	from, to := day, day.AddDate(0, 0, 90)
	rows, err := uc.reviews.UnlockWindow(ctx, from, to)
	switch {
	case err != nil:
		missing = append(missing, "equity_unlock: "+err.Error())
	case len(rows) == 0:
		fresh, ferr := uc.reviews.UnlockFresh(ctx, day, 4)
		if ferr != nil || !fresh {
			missing = append(missing, "equity_unlock")
		} else {
			facts = append(facts, Fact{
				ID: "F:review.supply.u90_none", Label: "未来90天解禁计划",
				Text: "未来 90 天无解禁批次记录（解禁域近 4 日内采集成功）", Dim: Capital,
			})
		}
	default:
		bySym := map[string]bool{}
		byDay := map[string]float64{}
		var dayOrder []string
		var totalShares float64
		for _, r := range rows {
			bySym[r.Symbol] = true
			key := r.FreeDate.Format("2006-01-02")
			if _, ok := byDay[key]; !ok {
				dayOrder = append(dayOrder, key)
			}
			byDay[key] += r.Shares
			totalShares += r.Shares
		}
		facts = append(facts,
			Fact{ID: "F:review.supply.u90_count", Label: "未来90天解禁只数", Value: float64(len(bySym)), Unit: "只", Dim: Capital},
			Fact{ID: "F:review.supply.u90_shares", Label: "未来90天解禁股数合计", Value: r2(totalShares / 1e4), Unit: "亿股", Dim: Capital},
		)
		peak, peakAt := "", ""
		peakV := -1.0
		for _, k := range dayOrder {
			if byDay[k] > peakV {
				peakV, peakAt = byDay[k], k
			}
		}
		peakCount := 0
		for _, r := range rows {
			if r.FreeDate.Format("2006-01-02") == peakAt {
				peakCount++
			}
		}
		peak = fmt.Sprintf("%s 解禁 %s 亿股（%d 只）", peakAt, formatValue(peakV/1e4), peakCount)
		facts = append(facts, Fact{ID: "F:review.supply.u90_peak", Label: "解禁峰值日", Text: peak, Dim: Capital})
		// 占流通比 >20% 的重大供给冲击（TradingAgents lockup_watcher 阈值），最多列 2 条。
		risky := 0
		for _, r := range rows {
			if r.RatioPct == nil || *r.RatioPct <= 20 || risky >= 2 {
				continue
			}
			risky++
			facts = append(facts, Fact{
				ID: fmt.Sprintf("F:review.supply.risky%d", risky), Label: fmt.Sprintf("重大解禁 %s", r.Symbol),
				Text: fmt.Sprintf("%s 解禁占流通 %.1f%%（%s）", r.FreeDate.Format("2006-01-02"), *r.RatioPct, r.LiftType),
				Dim:  Capital,
			})
		}
		// 与当日涨停股的重叠：持股过节/接力前必看的供给悬顶。
		if frozen != nil {
			hitSyms := map[string]bool{}
			for _, items := range frozen.Ladder {
				for _, it := range items {
					hitSyms[it.Symbol] = true
				}
			}
			var overlap []string
			for _, r := range rows {
				if hitSyms[r.Symbol] {
					overlap = append(overlap, fmt.Sprintf("%s（%s）", r.Symbol, r.FreeDate.Format("01-02")))
				}
			}
			if len(overlap) > 0 {
				if len(overlap) > 3 {
					overlap = append(overlap[:3], fmt.Sprintf("等 %d 只", len(overlap)))
				}
				facts = append(facts, Fact{
					ID: "F:review.supply.limited_hit", Label: "当日涨停股90天内解禁",
					Text: fmt.Sprintf("%d 只：%s", min(len(overlap), 4), strings.Join(overlap, "、")), Dim: Capital,
				})
			}
		}
	}
	dec, derr := uc.reviews.HoldingDecreaseRecent(ctx, day.AddDate(0, 0, -90), seal, 20)
	if derr != nil {
		missing = append(missing, "holding_decrease: "+derr.Error())
	} else if len(dec) > 0 {
		facts = append(facts, Fact{ID: "F:review.supply.dec90", Label: "近90天减持公告条数", Value: float64(len(dec)), Unit: "条", Dim: Capital})
		for i := 0; i < min(len(dec), 2); i++ {
			n := dec[i]
			facts = append(facts, Fact{
				ID: fmt.Sprintf("F:review.supply.dec%d", i+1), Label: fmt.Sprintf("减持公告%d %s", i+1, n.Symbol),
				Text: fmt.Sprintf("%s（%s）", n.Title, n.Time.Format("01-02")), Dim: News,
			})
		}
	}
	return facts, missing
}

func segLeaderFacts(f *Frozen) (facts []Fact, missing []string) {
	height := 0
	if f.MaxHeight != nil {
		height = *f.MaxHeight
	}
	ladders := 0
	for h := height; h >= 1 && ladders < 3; h-- {
		items := f.Ladder[fmt.Sprintf("%d", h)]
		if len(items) == 0 {
			continue
		}
		names := make([]string, 0, len(items))
		for _, it := range items[:min(len(items), 4)] {
			names = append(names, fmt.Sprintf("%s %s", it.Symbol, it.Name))
		}
		ladders++
		facts = append(facts, Fact{
			ID: fmt.Sprintf("F:review.leader.ladder%d", h), Label: fmt.Sprintf("%d 板梯队", h),
			Text: fmt.Sprintf("%d 只：%s", len(items), strings.Join(names, "、")), Dim: External,
		})
	}
	if ladders == 0 {
		missing = append(missing, "ladder")
	}
	put := func(fx Fact, ok bool) {
		if ok {
			facts = append(facts, fx)
		} else {
			missing = append(missing, fx.Label)
		}
	}
	put(derefFact("F:review.leader.relimit", "昨日涨停再涨停占比", f.TierRelimit, "%"))
	put(derefFact("F:review.leader.tier_red", "昨日梯队翻红占比", f.TierRedRate, "%"))
	strong := 0
	for _, it := range f.PrevTier {
		if it.Relimit && strong < 3 {
			strong++
			facts = append(facts, Fact{
				ID: fmt.Sprintf("F:review.leader.relimit%d", strong), Label: fmt.Sprintf("昨日涨停今日再涨停 %s %s", it.Symbol, it.Name),
				Text: fmt.Sprintf("今日涨幅 %+0.2f%%", it.Pct), Dim: External,
			})
		}
	}
	return facts, missing
}

// GenerateAIReview 生成分段式 AI 复盘（P1-7）。定稿先有（没有就即时冻结），各段独立会话，
// 任一段失败只降级该段；写 daily_reviews(kind='ai')，同日覆盖。
func (uc *Usecase) GenerateAIReview(ctx context.Context, day time.Time) (retErr error) {
	finish := observe.ForService("brain").Op("review_ai").Begin()
	observedSaved, observedDegraded := false, false
	defer func() { finishObservation(finish, retErr, observedSaved, observedDegraded, false) }()
	day = shDate(day)
	frozen, err := uc.reviews.FrozenOn(ctx, day)
	if err != nil {
		return fmt.Errorf("brain: frozen: %w", err)
	}
	if frozen == nil {
		if _, err = uc.FreezeEmotion(ctx, day); err != nil {
			return fmt.Errorf("brain: freeze emotion: %w", err)
		}
		if frozen, err = uc.reviews.FrozenOn(ctx, day); err != nil {
			return fmt.Errorf("brain: frozen reload: %w", err)
		}
		if frozen == nil {
			return fmt.Errorf("brain: %s 的定稿仍为空，分段复盘无输入", day.Format("2006-01-02"))
		}
	}
	seal := sealOf(day) // P1-6：所有读数都是当日封盘截面
	trace := newTrace("")
	decisionID, err := uc.decisions.Begin(ctx, "review_ai", "", trace)
	if err != nil {
		return fmt.Errorf("brain: begin decision: %w", err)
	}
	start := uc.now()

	var allFacts []Fact
	missingSet := map[string]bool{}
	for _, m := range frozen.Missing {
		missingSet[m] = true
	}
	segments := map[string]any{}
	var trail []EvidenceItem
	var models, tags []string
	omitted, degradedCount := 0, 0

	builders := map[aiSegment]func(context.Context) ([]Fact, []string){
		segEmotion: func(context.Context) ([]Fact, []string) { return segEmotionFacts(frozen) },
		segCapital: func(ctx context.Context) ([]Fact, []string) { return uc.segCapitalFacts(ctx, day) },
		segTheme:   func(context.Context) ([]Fact, []string) { return segThemeFacts(frozen) },
		segLHB:     func(ctx context.Context) ([]Fact, []string) { return uc.segLHBFacts(ctx, day) },
		segLeader:  func(context.Context) ([]Fact, []string) { return segLeaderFacts(frozen) },
		segSupply:  func(ctx context.Context) ([]Fact, []string) { return uc.segSupplyFacts(ctx, day, seal, frozen) },
	}

	for _, seg := range aiSegmentOrder {
		facts, missing := builders[seg](ctx)
		for _, m := range missing {
			missingSet[m] = true
		}
		entry := map[string]any{"points": []string{}, "evidence": []string{}, "degraded": false}
		if len(facts) == 0 {
			entry["text"] = "本段没取到数据（" + strings.Join(missing, "、") + "），未调用模型。"
			entry["degraded"] = true
			segments[string(seg)] = entry
			degradedCount++
			continue
		}
		allFacts = append(allFacts, facts...)
		pack := FactPack{AsOf: seal, Facts: facts}
		user := renderPack(&pack)
		p := reviewSegmentPrompts[seg]
		ids := pack.ids()
		nums := newNumberSet(facts, p.System, user)
		out, resp, err := callJSON(ctx, uc, callSpec{
			task: "review." + string(seg), prompt: p, prio: llm.Review, trace: trace, decision: decisionID, user: user,
		}, func(o *segmentOut) error { return o.check(ids, seg) })
		if err != nil {
			reason := "模型不可用：" + err.Error()
			if errors.Is(err, llm.ErrInvalid) {
				reason = "模型输出两次不合格：" + invalidReason(err)
			}
			entry["text"] = "本段模型未参与：" + reason
			entry["degraded"] = true
			degradedCount++
			uc.log.Warnf("ai review %s segment %s degraded: %s", day.Format("2006-01-02"), seg, reason)
		} else {
			models, tags = append(models, resp.Model), append(tags, p.Tag())
			items, n := scrubBriefingText(nums, "review."+string(seg), &out.Text, out.Points)
			omitted += n
			trail = append(trail, items...)
			entry["text"], entry["points"], entry["evidence"] = out.Text[:len(out.Text)], out.Points, out.Evidence
			// scrub 已在闭包里原地改好了；把修好的文本再落回去
			entry["text"], entry["points"] = out.Text, out.Points
		}
		segments[string(seg)] = entry
	}

	summary, lessons := uc.synthesizeReview(ctx, trace, decisionID, seal, segments, allFacts, &trail, &omitted, &models, &tags)
	observedDegraded = degradedCount > 0

	stats := map[string]any{
		"segments":          segments,
		"evidence":          trail,
		"omitted_count":     omitted,
		"model":             strings.Join(models, ","),
		"sealed_at":         seal.Format("2006-01-02 15:04"),
		"partial":           len(missingSet) > 0 || degradedCount > 0,
		"degraded_segments": degradedCount,
		"missing": func() []string {
			var out []string
			for m := range missingSet {
				out = append(out, m)
			}
			sort.Strings(out)
			return out
		}(),
	}
	pctx, pcancel := persistCtx(ctx)
	defer pcancel()
	model := strings.Join(models, ",")
	// M13-T07②：AI 日复盘落盘与 strategy.review 事件同一事务（trace 沿用本次研判链路）。
	title := "AI 日复盘 " + day.Format("01.02")
	if err := uc.reviews.SaveReviewWithEvent(pctx, day, reviewAIKind, summary, stats, lessons, model, trace, title); err != nil {
		return fmt.Errorf("brain: save ai review: %w", err)
	}
	if err := uc.decisions.Finish(pctx, decisionID, DecisionRecord{
		Kind: "review_ai", TraceID: trace,
		Content:       toMap(map[string]any{"summary": summary, "segments": segments, "lessons": lessons}),
		PromptVersion: strings.Join(tags, ","), Model: model,
		Degraded: degradedCount > 0, LatencyMS: int(uc.now().Sub(start).Milliseconds()),
	}); err != nil {
		return fmt.Errorf("brain: finish decision: %w", err)
	}
	observedSaved = true
	return nil
}

// synthesizeReview 汇总段：读各段结论写总评与教训。数字核对用全部段事实包与各段文本的并集（P1-5）。
// 可用段不足 3 段或模型失败：用可用段要点拼兜底总评，summary 标记 degraded 语义（stats.partial 已体现）。
func (uc *Usecase) synthesizeReview(ctx context.Context, trace string, decisionID int, seal time.Time,
	segments map[string]any, allFacts []Fact, trail *[]EvidenceItem, omitted *int, models, tags *[]string) (string, []string) {
	var usable []string
	for _, seg := range aiSegmentOrder {
		if e, ok := segments[string(seg)]; ok {
			if m, ok2 := e.(map[string]any); ok2 && m["degraded"] == true {
				continue
			}
			if m, ok2 := e.(map[string]any); ok2 {
				usable = append(usable, fmt.Sprintf("[%s] %s\n要点：%s", seg, m["text"], strings.Join(toStrings(m["points"]), "；")))
			}
		}
	}
	if len(usable) < 3 {
		return "可用复盘段不足 3 段，未调用汇总模型：" + strings.Join(fallbackPoints(segments), "；"), fallbackPoints(segments)
	}
	user := "【今日分段复盘结论】\n" + strings.Join(usable, "\n")
	user += "\n把上面各段读成一段总评和 2 到 6 条给明天的教训。"
	nums := newNumberSet(allFacts, reviewSynthesisPrompt.System, user)
	out, resp, err := callJSON(ctx, uc, callSpec{
		task: "review.synthesis", prompt: reviewSynthesisPrompt, prio: llm.Review, trace: trace, decision: decisionID, user: user,
	}, func(o *reviewSynthesisOut) error { return o.check() })
	if err != nil {
		uc.log.Warnf("ai review synthesis degraded: %v", err)
		return "汇总段模型未参与。各段要点：" + strings.Join(fallbackPoints(segments), "；"), fallbackPoints(segments)
	}
	*models, *tags = append(*models, resp.Model), append(*tags, reviewSynthesisPrompt.Tag())
	items, n := scrubBriefingText(nums, "review.synthesis", &out.Summary, out.Lessons)
	*omitted += n
	*trail = append(*trail, items...)
	return out.Summary, out.Lessons
}

// scrubBriefingText 把一段正文 + 要点列表过三档核对并原地修复，给留痕补上字段归属。
func scrubBriefingText(nums numberSet, field string, text *string, points []string) ([]EvidenceItem, int) {
	fields := []scrubField{{field + ".text", text}}
	for i := range points {
		fields = append(fields, scrubField{fmt.Sprintf("%s.points[%d]", field, i), &points[i]})
	}
	return scrubFields(nums, fields)
}

// fallbackPoints 汇总兜底：取各可用段的第一条要点。
func fallbackPoints(segments map[string]any) []string {
	var out []string
	for _, seg := range aiSegmentOrder {
		if e, ok := segments[string(seg)]; ok {
			m, ok2 := e.(map[string]any)
			if !ok2 || m["degraded"] == true {
				continue
			}
			if pts := toStrings(m["points"]); len(pts) > 0 {
				out = append(out, pts[0])
			}
		}
	}
	if len(out) == 0 {
		out = []string{"今日无可用的复盘要点"}
	}
	return out
}

func toStrings(v any) []string {
	if arr, ok := v.([]string); ok {
		return arr
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func meanOf(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var s float64
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

// Review 读某日某 kind 复盘（program 程序版 / ai 分段版 / ai_week 周报）。
// kind=ai_week 时 date 对齐到本周最后交易日，effDay 返回实际落行的日期。没写过报 ErrNotFound。
func (uc *Usecase) Review(ctx context.Context, day time.Time, kind string) (content string, stats map[string]any, lessons []string, model string, effDay time.Time, err error) {
	if kind != "program" && kind != reviewAIKind && kind != reviewWeekKind {
		return "", nil, nil, "", time.Time{}, badRequest("kind 只能是 program、ai 或 ai_week")
	}
	effDay = shDate(day)
	if kind == reviewWeekKind {
		if effDay, err = uc.weekLastOpenDay(effDay); err != nil {
			return "", nil, nil, "", time.Time{}, fmt.Errorf("brain: week end: %w", err)
		}
	}
	content, stats, lessons, model, found, err := uc.reviews.ReviewOn(ctx, effDay, kind)
	if err != nil {
		return "", nil, nil, "", time.Time{}, err
	}
	if !found {
		return "", nil, nil, "", time.Time{}, fmt.Errorf("%w: %s 没有 kind=%s 的复盘", ErrNotFound, effDay.Format("2006-01-02"), kind)
	}
	return content, stats, lessons, model, effDay, nil
}
