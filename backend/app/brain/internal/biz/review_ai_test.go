package biz

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"server/pkg/llm"

	"github.com/go-kratos/kratos/v2/log"
)

// review_ai_test.go：P1-7 分段式 AI 复盘（P2-8 扩为六段：+筹码供给）。fakeReviews 预置非 nil 定稿，跳过 FreezeEmotion 路径。

type savedReview struct {
	content string
	stats   map[string]any
	lessons []string
	model   string
}

type fakeReviews struct {
	frozen   *Frozen
	senti    *SentimentRow
	amounts  []float64
	margin   float64
	marginOK bool
	north    []NorthTop
	saved    map[string]savedReview
	pool     []PoolRow
	bars     map[string]TierBar
	// P2-8：龙虎榜席位、解禁窗口与减持公告。
	seats       []LhbSeatRow
	unlocks     []UnlockRow
	unlockFresh bool
	decNews     []Intel
	// P1-7b 周报：按日取数（键 2006-01-02）；非 nil 时优先于上面的单值字段。
	frozenByDay map[string]*Frozen
	sentiByDay  map[string]*SentimentRow
	events      []savedEvent
	// M13-T08：signal 源对账预置。
	signals    []SignalRow
	signalsErr error
	checks     []*Check
}

type savedEvent struct {
	Day, Kind, Title, Body string
}

func (r *fakeReviews) PoolOn(context.Context, time.Time) ([]PoolRow, error)     { return r.pool, nil }
func (r *fakeReviews) BarOn(_ context.Context, _ []string, _ time.Time) (map[string]TierBar, error) {
	return r.bars, nil
}
func (r *fakeReviews) UpsertFrozen(_ context.Context, f *Frozen) error    { r.frozen = f; return nil }
func (r *fakeReviews) FrozenOn(_ context.Context, day time.Time) (*Frozen, error) {
	if r.frozenByDay != nil {
		return r.frozenByDay[day.Format("2006-01-02")], nil
	}
	return r.frozen, nil
}
func (r *fakeReviews) SentimentOn(_ context.Context, day time.Time) (*SentimentRow, error) {
	if r.sentiByDay != nil {
		return r.sentiByDay[day.Format("2006-01-02")], nil
	}
	return r.senti, nil
}
func (r *fakeReviews) SentimentAmounts(_ context.Context, _ time.Time, n int) ([]float64, error) {
	if len(r.amounts) > n {
		return r.amounts[:n], nil
	}
	return r.amounts, nil
}
func (r *fakeReviews) MarginTotal(context.Context, time.Time) (float64, bool, error) {
	return r.margin, r.marginOK, nil
}
func (r *fakeReviews) SaveProgramReview(context.Context, time.Time, string, map[string]any) error {
	return nil
}
func (r *fakeReviews) SaveProgramReviewWithEvent(_ context.Context, day time.Time, content string, stats map[string]any, traceID, title string) error {
	if r.saved == nil {
		r.saved = map[string]savedReview{}
	}
	r.saved[day.Format("2006-01-02")+"|program"] = savedReview{content: content, stats: stats}
	r.events = append(r.events, savedEvent{Day: day.Format("2006-01-02"), Kind: "program", Title: title, Body: content})
	return nil
}
func (r *fakeReviews) SignalsOnDays(context.Context, []time.Time) ([]SignalRow, error) {
	return r.signals, r.signalsErr
}
func (r *fakeReviews) UpsertPredictionCheck(_ context.Context, c *Check) error {
	r.checks = append(r.checks, c)
	return nil
}
func (r *fakeReviews) PredictionChecks(context.Context, int) ([]*Check, error) {
	return nil, nil
}
func (r *fakeReviews) PredictionCheckOn(context.Context, time.Time, string) (*Check, error) {
	return nil, nil
}
func (r *fakeReviews) SaveReview(_ context.Context, day time.Time, kind, content string, stats map[string]any, lessons []string, model string) error {
	if r.saved == nil {
		r.saved = map[string]savedReview{}
	}
	r.saved[day.Format("2006-01-02")+"|"+kind] = savedReview{content, stats, lessons, model}
	return nil
}
func (r *fakeReviews) ReviewOn(_ context.Context, day time.Time, kind string) (string, map[string]any, []string, string, bool, error) {
	s, ok := r.saved[day.Format("2006-01-02")+"|"+kind]
	if !ok {
		return "", nil, nil, "", false, nil
	}
	return s.content, s.stats, s.lessons, s.model, true, nil
}
func (r *fakeReviews) SaveReviewWithEvent(_ context.Context, day time.Time, kind, content string, stats map[string]any, lessons []string, model, traceID, title string) error {
	if r.saved == nil {
		r.saved = map[string]savedReview{}
	}
	r.saved[day.Format("2006-01-02")+"|"+kind] = savedReview{content, stats, lessons, model}
	r.events = append(r.events, savedEvent{Day: day.Format("2006-01-02"), Kind: kind, Title: title, Body: content})
	return nil
}
func (r *fakeReviews) HsgtTop10(context.Context, time.Time) ([]NorthTop, error) {
	return r.north, nil
}
func (r *fakeReviews) LhbSeatsOn(context.Context, time.Time) ([]LhbSeatRow, error) {
	return r.seats, nil
}
func (r *fakeReviews) UnlockWindow(context.Context, time.Time, time.Time) ([]UnlockRow, error) {
	return r.unlocks, nil
}
func (r *fakeReviews) UnlockFresh(context.Context, time.Time, int) (bool, error) {
	return r.unlockFresh, nil
}
func (r *fakeReviews) HoldingDecreaseRecent(context.Context, time.Time, time.Time, int) ([]Intel, error) {
	return r.decNews, nil
}

func pi(v int) *int        { return &v }
func pf(v float64) *float64 { return &v }

func reviewFrozenFixture() *Frozen {
	return &Frozen{
		UpCount: pf60(), DownCount: pi(8), BrokenCount: pi(17),
		SealRate: pf(77.92), MaxHeight: pi(5),
		Promo12: pf(42.5), Promo23: pf(33.3), PromoHigh: pf(50),
		TierRedRate: pf(58), TierAvg: pf(1.85), TierRelimit: pf(9.5),
		Ladder: map[string][]LadderItem{
			"5": {{Symbol: "600001.SH", Name: "五连板龙头", Reason: "算力"}},
			"3": {{Symbol: "600002.SH", Name: "三连助攻", Reason: "机器人"}},
			"1": {{Symbol: "600003.SH", Name: "首板甲"}, {Symbol: "600004.SH", Name: "首板乙"}},
		},
		ThemeTree: []ThemeItem{
			{Reason: "算力", Count: 12, MaxHeight: 5, Symbols: []string{"600001.SH", "600011.SH", "600012.SH"}},
			{Reason: "机器人", Count: 9, MaxHeight: 3, Symbols: []string{"600002.SH", "600021.SH", "600022.SH"}},
		},
		PrevTier: []PrevTierItem{
			{Symbol: "600001.SH", Name: "五连板龙头", Pct: 10.02, Relimit: true},
		},
		SealQ:         &SealQuality{NeverBroken: 40, Resealed: 3, EarlySeal: 33},
		StructureGaps: []int{4},
	}
}

func pf60() *int { return pi(60) }

func reviewReply(req llm.Request) (string, error) {
	switch req.Task {
	case "review.emotion":
		return `{"text":"涨停60家、跌停8家，封板率77.92%，短线情绪偏暖。","points":["最高连板5板，梯队完整","1进2晋级率42.5%，晋级积极","未炸板封住40只"],"evidence":["F:review.emotion.up"]}`, nil
	case "review.capital":
		return `{"text":"两市成交额11200亿，上涨3200家、下跌1601家，较20日均值为1.12倍，资金偏活跃。","points":["成交额11200亿温和放量","上涨3200家明显多于下跌1601家"],"evidence":["F:review.capital.amount"]}`, nil
	case "review.theme":
		return `{"text":"题材集中于算力与机器人：算力12家涨停最高5板，机器人9家3板。","points":["算力12家涨停为当日主线","机器人9家跟随"],"evidence":["F:review.theme.1"]}`, nil
	case "review.lhb":
		return `{"text":"龙虎榜席位净买合计1.1亿，游资「拉萨天团」净买2.1亿、机构席位净卖1.9亿；北向十大净买合计7.5亿。","points":["游资席位净买2.1亿是强势信号","机构席位净卖1.9亿需防分歧"],"evidence":["F:review.lhb.seat_net"]}`, nil
	case "review.leader":
		return `{"text":"5板龙头引领、3板有助攻，4板断层；昨日涨停今日再涨停占9.5%。","points":["龙头五连板，4板梯队断层","昨日梯队翻红58%，短线氛围偏暖"],"evidence":["F:review.leader.ladder5"]}`, nil
	case "review.supply":
		return `{"text":"未来90天有2只股票解禁合计2亿股，重大解禁占流通25.5%；当日涨停股中有1只90天内解禁；近90天减持公告2条。","points":["重大解禁占流通25.5%形成供给压力","涨停股中有解禁悬顶，接力需谨慎"],"evidence":["F:review.supply.u90_shares"]}`, nil
	case "review.synthesis":
		return `{"summary":"情绪偏暖、资金活跃、题材聚焦算力，北向净买7.5亿，龙头五连板梯队完整，整体偏多但需防高位分歧。","lessons":["关注算力持续性，晋级率42.5%是情绪风向标","昨日梯队翻红58%，断板股修复是明日观察点","北向净买7.5亿方向明确，跟随但不盲目高位接力"]}`, nil
	}
	return "", fmt.Errorf("unexpected task %s", req.Task)
}

func newReviewEnv(t *testing.T, reply func(llm.Request) (string, error)) (*Usecase, *fakeLLM, *fakeReviews) {
	t.Helper()
	e := newEnv(reply)
	rv := &fakeReviews{
		frozen:   reviewFrozenFixture(),
		senti:    &SentimentRow{Advance: 3200, Decline: 1601, Amount: 1.12e12},
		amounts:  flatAmounts(60, 1e12),
		margin:   1.61e12,
		marginOK: true,
		north: []NorthTop{
			{Symbol: "600519.SH", Name: "贵州茅台", Channel: "SH", Rank: 1, Net: pf(5.2e8), PctChg: pf(1.86), Amount: 3.1e9},
			{Symbol: "300750.SZ", Name: "宁德时代", Channel: "SZ", Rank: 2, Net: pf(3.4e8), PctChg: pf(-0.42), Amount: 2.8e9},
			{Symbol: "603259.SH", Name: "药明康德", Channel: "SH", Rank: 5, Net: pf(-1.1e8), Amount: 1.9e9},
		},
		seats: []LhbSeatRow{
			{Symbol: "600001.SH", Reason: "日涨幅偏离值达7%", SeatName: "东方财富证券拉萨团结路第二证券营业部", Buy: 3.2e8, Sell: 1.1e8, Tag: "HOT_MONEY", Alias: "拉萨天团"},
			{Symbol: "600001.SH", Reason: "日涨幅偏离值达7%", SeatName: "机构专用", Buy: 2.0e8, Sell: 3.9e8, Tag: "INSTITUTION"},
			{Symbol: "600002.SH", Reason: "连续三个交易日收盘价偏离值累计达20%", SeatName: "华泰证券量化交易席位", Buy: 1.4e8, Sell: 0.5e8, Tag: "QUANT"},
		},
		unlocks: []UnlockRow{
			{Symbol: "600001.SH", FreeDate: day2026_11_15(), LiftType: "定向增发机构配售", Shares: 12000, RatioPct: pf(25.5)},
			{Symbol: "600019.SH", FreeDate: day2026_10_26(), LiftType: "首发原股东限售股份", Shares: 8000, RatioPct: pf(3.2)},
		},
		unlockFresh: true,
		decNews: []Intel{
			{ID: "N:801", Title: "大股东拟减持不超过2%股份", Symbol: "600019.SH", Time: time.Date(2026, 10, 6, 12, 0, 0, 0, locShanghai())},
			{ID: "N:802", Title: "股东减持计划实施完成", Symbol: "600030.SH", Time: time.Date(2026, 9, 20, 12, 0, 0, 0, locShanghai())},
		},
		saved: map[string]savedReview{},
	}
	uc := NewUsecase(NewSettings(nil), e.llm, e.market, e.decisions, e.briefings, rv, stubLlmCalls{}, e.progress, nil, log.DefaultLogger)
	uc.now = func() time.Time { return time.Date(2026, 10, 16, 15, 50, 0, 0, locShanghai()) }
	return uc, e.llm, rv
}

func locShanghai() *time.Location {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.UTC
	}
	return loc
}

func flatAmounts(n int, v float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func reviewDay(t *testing.T) time.Time { return time.Date(2026, 10, 16, 0, 0, 0, 0, locShanghai()) }

// 解禁 fixture 的两个 free_date：复盘日 2026-10-16 +10/+30 天，都在 90 天窗口内。
func day2026_10_26() time.Time { return time.Date(2026, 10, 26, 0, 0, 0, 0, locShanghai()) }
func day2026_11_15() time.Time { return time.Date(2026, 11, 15, 0, 0, 0, 0, locShanghai()) }

func TestGenerateAIReviewHappy(t *testing.T) {
	uc, l, rv := newReviewEnv(t, reviewReply)
	day := reviewDay(t)
	if err := uc.GenerateAIReview(context.Background(), day); err != nil {
		t.Fatal(err)
	}
	s, ok := rv.saved["2026-10-16|ai"]
	if !ok {
		t.Fatalf("未落 daily_reviews(ai)：%v", rv.saved)
	}
	if !strings.Contains(s.content, "情绪偏暖") {
		t.Fatalf("summary = %q", s.content)
	}
	if len(s.lessons) < 2 || len(s.lessons) > 6 {
		t.Fatalf("lessons = %v", s.lessons)
	}
	segs, _ := s.stats["segments"].(map[string]any)
	if len(segs) != 6 {
		t.Fatalf("segments = %v", s.stats["segments"])
	}
	for _, name := range []string{"emotion", "capital", "theme", "lhb", "leader", "supply"} {
		m, _ := segs[name].(map[string]any)
		if m == nil || m["degraded"].(bool) {
			t.Fatalf("段 %s 意外降级：%v", name, segs[name])
		}
	}
	// 龙虎榜段应带真实席位事实（P2-8），不再只有北向替代读数。
	lhbFacts := segs["lhb"].(map[string]any)["text"].(string)
	if !strings.Contains(lhbFacts, "拉萨") {
		t.Fatalf("lhb 段应引用席位画像：%q", lhbFacts)
	}
	supplyText := segs["supply"].(map[string]any)["text"].(string)
	if !strings.Contains(supplyText, "解禁") {
		t.Fatalf("supply 段应含解禁内容：%q", supplyText)
	}
	if got := s.stats["omitted_count"].(int); got != 0 {
		t.Fatalf("omitted_count = %d（happy case 应全部有出处）", got)
	}
	if s.stats["partial"].(bool) || s.stats["degraded_segments"].(int) != 0 {
		t.Fatalf("stats = %v", s.stats)
	}
	tasks := l.tasks()
	for _, task := range []string{"review.emotion", "review.capital", "review.theme", "review.lhb", "review.leader", "review.supply", "review.synthesis"} {
		if tasks[task] != 1 {
			t.Fatalf("calls = %v", tasks)
		}
	}
	if s.model == "" || !strings.Contains(s.model, "fake-") {
		t.Fatalf("model = %q", s.model)
	}
}

func TestGenerateAIReviewSegmentFailureDegradesOnlyThatSegment(t *testing.T) {
	uc, _, rv := newReviewEnv(t, func(req llm.Request) (string, error) {
		if req.Task == "review.capital" {
			return "", fmt.Errorf("boom")
		}
		return reviewReply(req)
	})
	if err := uc.GenerateAIReview(context.Background(), reviewDay(t)); err != nil {
		t.Fatal(err)
	}
	s := rv.saved["2026-10-16|ai"]
	segs := s.stats["segments"].(map[string]any)
	if !segs["capital"].(map[string]any)["degraded"].(bool) {
		t.Fatalf("capital 段应降级：%v", segs["capital"])
	}
	if !strings.Contains(segs["capital"].(map[string]any)["text"].(string), "模型不可用") {
		t.Fatalf("capital text = %v", segs["capital"])
	}
	if s.stats["degraded_segments"].(int) != 1 || !s.stats["partial"].(bool) {
		t.Fatalf("stats = %v", s.stats)
	}
	// 其余五段照常参与，汇总段仍应被调用（可用段 5 ≥ 3）。
	if !strings.Contains(s.content, "情绪偏暖") {
		t.Fatalf("汇总不应被单段失败拖垮，summary = %q", s.content)
	}
}

func TestGenerateAIReviewNoDataSkipsSegment(t *testing.T) {
	uc, l, rv := newReviewEnv(t, reviewReply)
	rv.senti = nil // 资金段没数据
	rv.north = nil // 龙虎榜段北向没数据
	rv.seats = nil // 龙虎榜段席位也没数据，整段才算没取到
	if err := uc.GenerateAIReview(context.Background(), reviewDay(t)); err != nil {
		t.Fatal(err)
	}
	s := rv.saved["2026-10-16|ai"]
	segs := s.stats["segments"].(map[string]any)
	for _, name := range []string{"capital", "lhb"} {
		m := segs[name].(map[string]any)
		if !m["degraded"].(bool) || !strings.Contains(m["text"].(string), "没取到数据") {
			t.Fatalf("%s 段应标记没取到数据：%v", name, m)
		}
	}
	tasks := l.tasks()
	if tasks["review.capital"] != 0 || tasks["review.lhb"] != 0 {
		t.Fatalf("无数据段不应调模型：%v", tasks)
	}
	// 可用段 4 段（emotion/theme/leader/supply），够调汇总。
	if tasks["review.synthesis"] != 1 {
		t.Fatalf("4 段可用应仍调汇总：%v", tasks)
	}
	if got := s.stats["degraded_segments"].(int); got != 2 {
		t.Fatalf("degraded_segments = %d", got)
	}
}

// P2-8：席位有数、北向没数 → 段不降级，missing 只记 hsgt_top10。
func TestGenerateAIReviewLhbSeatsWithoutNorth(t *testing.T) {
	uc, _, rv := newReviewEnv(t, func(req llm.Request) (string, error) {
		if req.Task == "review.lhb" {
			return `{"text":"龙虎榜席位净买合计1.1亿，游资净买2.1亿、机构净卖1.9亿。","points":["游资席位净买2.1亿方向偏多","机构净卖1.9亿需防分歧"],"evidence":["F:review.lhb.seat_net"]}`, nil
		}
		return reviewReply(req)
	})
	rv.north = nil
	if err := uc.GenerateAIReview(context.Background(), reviewDay(t)); err != nil {
		t.Fatal(err)
	}
	s := rv.saved["2026-10-16|ai"]
	segs := s.stats["segments"].(map[string]any)
	if segs["lhb"].(map[string]any)["degraded"].(bool) {
		t.Fatalf("席位有数时 lhb 段不应降级：%v", segs["lhb"])
	}
	missing, _ := s.stats["missing"].([]string)
	if !hasStr(missing, "hsgt_top10") || hasStr(missing, "lhb_seat") {
		t.Fatalf("应只记 hsgt_top10 缺失：%v", missing)
	}
}

// P2-8：解禁窗口空但近日采集成功过 → 「真无解禁」是文本事实，段不降级。
func TestGenerateAIReviewSupplyEmptyWindowFresh(t *testing.T) {
	uc, _, rv := newReviewEnv(t, func(req llm.Request) (string, error) {
		if req.Task == "review.supply" {
			return `{"text":"未来90天无解禁批次记录，筹码供给面无增量压力。","points":["90天窗口无解禁，供给面干净","无减持公告，持股过节无供给悬顶"],"evidence":["F:review.supply.u90_none"]}`, nil
		}
		return reviewReply(req)
	})
	rv.unlocks = nil
	rv.decNews = nil
	rv.unlockFresh = true
	if err := uc.GenerateAIReview(context.Background(), reviewDay(t)); err != nil {
		t.Fatal(err)
	}
	s := rv.saved["2026-10-16|ai"]
	segs := s.stats["segments"].(map[string]any)
	if segs["supply"].(map[string]any)["degraded"].(bool) {
		t.Fatalf("真无解禁不应降级：%v", segs["supply"])
	}
	missing, _ := s.stats["missing"].([]string)
	if hasStr(missing, "equity_unlock") {
		t.Fatalf("真无解禁不应记 equity_unlock 缺失：%v", missing)
	}
}

// P2-8：窗口空且近日没采集成功 → 没取到≠0，段降级记缺失。
func TestGenerateAIReviewSupplyUnknownStateDegrades(t *testing.T) {
	uc, l, rv := newReviewEnv(t, reviewReply)
	rv.unlocks = nil
	rv.decNews = nil
	rv.unlockFresh = false
	if err := uc.GenerateAIReview(context.Background(), reviewDay(t)); err != nil {
		t.Fatal(err)
	}
	s := rv.saved["2026-10-16|ai"]
	segs := s.stats["segments"].(map[string]any)
	m := segs["supply"].(map[string]any)
	if !m["degraded"].(bool) || !strings.Contains(m["text"].(string), "没取到数据") {
		t.Fatalf("采集没跑过应标没取到数据：%v", m)
	}
	missing, _ := s.stats["missing"].([]string)
	if !hasStr(missing, "equity_unlock") {
		t.Fatalf("应记 equity_unlock 缺失：%v", missing)
	}
	if l.tasks()["review.supply"] != 0 {
		t.Fatalf("无数据段不应调模型：%v", l.tasks())
	}
}

func hasStr(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func TestGenerateAIReviewScrubCountsFabricatedNumbers(t *testing.T) {
	uc, _, rv := newReviewEnv(t, func(req llm.Request) (string, error) {
		if req.Task == "review.synthesis" {
			// 999 不在任何事实包出现 → 三档核对替换为「（略※）」并计数。
			return `{"summary":"情绪偏暖，预计明天有999只涨停。","lessons":["关注晋级率42.5%","北向净买7.5亿方向明确"]}`, nil
		}
		return reviewReply(req)
	})
	if err := uc.GenerateAIReview(context.Background(), reviewDay(t)); err != nil {
		t.Fatal(err)
	}
	s := rv.saved["2026-10-16|ai"]
	if !strings.Contains(s.content, "（略※）") {
		t.Fatalf("无出处数字应被替换：%q", s.content)
	}
	if s.stats["omitted_count"].(int) < 1 {
		t.Fatalf("omitted_count 应 ≥1：%v", s.stats["omitted_count"])
	}
	trail, _ := s.stats["evidence"].([]EvidenceItem)
	found := false
	for _, it := range trail {
		if it.Verdict == "omitted" && it.Span == "999" {
			found = true
		}
	}
	if !found {
		t.Fatalf("留痕应包含 999 的 omitted 记录：%v", trail)
	}
}

func TestReviewReadoutAndKindGuard(t *testing.T) {
	uc, _, _ := newReviewEnv(t, reviewReply)
	day := reviewDay(t)
	if err := uc.GenerateAIReview(context.Background(), day); err != nil {
		t.Fatal(err)
	}
	content, stats, lessons, model, _, err := uc.Review(context.Background(), day, "ai")
	if err != nil {
		t.Fatal(err)
	}
	if content == "" || len(stats) == 0 || len(lessons) == 0 || model == "" {
		t.Fatalf("readout = (%q, %v, %v, %q)", content, stats, lessons, model)
	}
	if _, _, _, _, _, err := uc.Review(context.Background(), day, "weekly"); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("非法 kind 应报 bad request：%v", err)
	}
	if _, _, _, _, _, err := uc.Review(context.Background(), day.AddDate(0, 0, -1), "ai"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("没写过的日子应报 not found：%v", err)
	}
}
