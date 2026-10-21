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

// review_weekly_test.go：P1-7b AI 周报。窗口=本 ISO 周 ≤day 的交易日；行落在本周最后交易日；
// 落库与 strategy.review 事件同事务（fake 捕获 events）；整周无数据不调模型不发事件。

func weeklyFrozen(up, down, height int, seal, promo float64, themes ...ThemeItem) *Frozen {
	return &Frozen{
		UpCount: pi(up), DownCount: pi(down), BrokenCount: pi(3),
		SealRate: pf(seal), MaxHeight: pi(height), Promo12: pf(promo),
		ThemeTree: themes,
	}
}

// weeklyFixtures 2026-10-12(一) ~ 10-16(五) 五个交易日，算力贯穿、周五最高 5 板。
func weeklyFixtures() (map[string]*Frozen, map[string]*SentimentRow) {
	frozen := map[string]*Frozen{
		"2026-10-12": weeklyFrozen(40, 12, 3, 70.5, 30, ThemeItem{Reason: "算力", Count: 8, MaxHeight: 3}),
		"2026-10-13": weeklyFrozen(45, 9, 4, 72, 38, ThemeItem{Reason: "算力", Count: 10, MaxHeight: 4}, ThemeItem{Reason: "机器人", Count: 6, MaxHeight: 2}),
		"2026-10-14": weeklyFrozen(38, 15, 3, 66.8, 28, ThemeItem{Reason: "算力", Count: 7, MaxHeight: 3}),
		"2026-10-15": weeklyFrozen(52, 10, 4, 75.2, 41, ThemeItem{Reason: "算力", Count: 9, MaxHeight: 4}),
		"2026-10-16": weeklyFrozen(60, 8, 5, 77.92, 42.5, ThemeItem{Reason: "算力", Count: 12, MaxHeight: 5}, ThemeItem{Reason: "机器人", Count: 9, MaxHeight: 3}),
	}
	senti := map[string]*SentimentRow{}
	for d := range frozen {
		senti[d] = &SentimentRow{Advance: 3200, Decline: 1601, Amount: 1e12}
	}
	return frozen, senti
}

func weeklyReply(req llm.Request) (string, error) {
	if req.Task == "review.weekly" {
		return `{"summary":"本周情绪逐步回暖：日均涨停47家、平均封板率72.48%，周五最高5板，算力贯穿全周5天上榜累计46家涨停。日均成交10000亿，与20日均值基本持平。","lessons":["晋级率从28%回升到42.5%，情绪修复信号明确","算力5天上榜累计46家，主线地位稳固，下周盯龙头5板是否掉队","日均成交10000亿未明显放量，突破需增量确认"]}`, nil
	}
	return "", fmt.Errorf("unexpected task %s", req.Task)
}

func newWeeklyEnv(t *testing.T, reply func(llm.Request) (string, error)) (*Usecase, *fakeLLM, *fakeReviews) {
	t.Helper()
	e := newEnv(reply)
	frozen, senti := weeklyFixtures()
	rv := &fakeReviews{
		frozenByDay: frozen,
		sentiByDay:  senti,
		amounts:     flatAmounts(25, 1e12),
		saved:       map[string]savedReview{},
	}
	uc := NewUsecase(NewSettings(nil), e.llm, e.market, e.decisions, e.briefings, rv, stubLlmCalls{}, e.progress, nil, log.DefaultLogger)
	uc.now = func() time.Time { return time.Date(2026, 10, 16, 15, 55, 0, 0, locShanghai()) }
	return uc, e.llm, rv
}

func weeklyDay(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.ParseInLocation("2006-01-02", s, locShanghai())
	if err != nil {
		t.Fatalf("bad date %s", s)
	}
	return d
}

func TestGenerateWeeklyReviewHappy(t *testing.T) {
	uc, l, rv := newWeeklyEnv(t, weeklyReply)
	if err := uc.GenerateWeeklyReview(context.Background(), weeklyDay(t, "2026-10-16")); err != nil {
		t.Fatal(err)
	}
	s, ok := rv.saved["2026-10-16|ai_week"]
	if !ok {
		t.Fatalf("周报应落在本周最后交易日 2026-10-16：%v", rv.saved)
	}
	if !strings.Contains(s.content, "情绪逐步回暖") {
		t.Fatalf("summary = %q", s.content)
	}
	if len(s.lessons) < 2 || len(s.lessons) > 6 {
		t.Fatalf("lessons = %v", s.lessons)
	}
	if got := s.stats["day_count"].(int); got != 5 {
		t.Fatalf("day_count = %v", got)
	}
	if got := s.stats["usable_days"].(int); got != 5 {
		t.Fatalf("usable_days = %v", got)
	}
	if got := s.stats["expected_days"].(int); got != 5 {
		t.Fatalf("expected_days = %v", got)
	}
	if s.stats["partial"].(bool) || s.stats["degraded"].(bool) {
		t.Fatalf("happy 不应 partial/degraded：%v", s.stats)
	}
	if got := s.stats["omitted_count"].(int); got != 0 {
		t.Fatalf("omitted_count = %d（应全部有出处）", got)
	}
	if l.tasks()["review.weekly"] != 1 {
		t.Fatalf("review.weekly 应恰好调一次：%v", l.tasks())
	}
	if s.model == "" || !strings.Contains(s.model, "fake-") {
		t.Fatalf("model = %q", s.model)
	}
	// 同事务事件：一周一条 strategy.review，标题带周复盘、正文=总评。
	if len(rv.events) != 1 {
		t.Fatalf("events = %v", rv.events)
	}
	ev := rv.events[0]
	if ev.Kind != "ai_week" || ev.Day != "2026-10-16" || !strings.Contains(ev.Title, "周复盘") || ev.Body != s.content {
		t.Fatalf("event = %+v", ev)
	}
	// 读出口：选本周任意一天都对齐到同一行。
	content, _, _, _, effDay, err := uc.Review(context.Background(), weeklyDay(t, "2026-10-15"), "ai_week")
	if err != nil {
		t.Fatal(err)
	}
	if effDay.Format("2006-01-02") != "2026-10-16" || content != s.content {
		t.Fatalf("weekly snap readout = (%s, %q)", effDay.Format("2006-01-02"), content)
	}
}

func TestGenerateWeeklyReviewMidweekSnapsToWeekEnd(t *testing.T) {
	uc, _, rv := newWeeklyEnv(t, weeklyReply)
	// 周三手动重跑：窗口 = 周一~周三，行仍落在本周五。
	if err := uc.GenerateWeeklyReview(context.Background(), weeklyDay(t, "2026-10-14")); err != nil {
		t.Fatal(err)
	}
	s, ok := rv.saved["2026-10-16|ai_week"]
	if !ok {
		t.Fatalf("周中重跑也应落在本周最后交易日：%v", rv.saved)
	}
	if got := s.stats["day_count"].(int); got != 3 {
		t.Fatalf("day_count = %v", got)
	}
	if got := s.stats["expected_days"].(int); got != 5 {
		t.Fatalf("expected_days = %v", got)
	}
	if got := s.stats["usable_days"].(int); got != 3 {
		t.Fatalf("usable_days = %v", got)
	}
	if len(rv.events) != 1 || rv.events[0].Day != "2026-10-16" {
		t.Fatalf("events = %v", rv.events)
	}
}

func TestGenerateWeeklyReviewNoDataSkipsModelAndEvent(t *testing.T) {
	e := newEnv(weeklyReply)
	rv := &fakeReviews{
		frozenByDay: map[string]*Frozen{},
		sentiByDay:  map[string]*SentimentRow{},
		saved:       map[string]savedReview{},
	}
	uc := NewUsecase(NewSettings(nil), e.llm, e.market, e.decisions, e.briefings, rv, stubLlmCalls{}, e.progress, nil, log.DefaultLogger)
	uc.now = func() time.Time { return time.Date(2026, 10, 16, 15, 55, 0, 0, locShanghai()) }
	if err := uc.GenerateWeeklyReview(context.Background(), weeklyDay(t, "2026-10-16")); err != nil {
		t.Fatal(err)
	}
	s, ok := rv.saved["2026-10-16|ai_week"]
	if !ok {
		t.Fatalf("整周没数据也应留降级行：%v", rv.saved)
	}
	if !strings.Contains(s.content, "没取到") {
		t.Fatalf("content = %q", s.content)
	}
	if !s.stats["degraded"].(bool) || !s.stats["partial"].(bool) || s.stats["usable_days"].(int) != 0 {
		t.Fatalf("stats = %v", s.stats)
	}
	if e.llm.tasks()["review.weekly"] != 0 {
		t.Fatalf("没数据不应调模型：%v", e.llm.tasks())
	}
	if len(rv.events) != 0 {
		t.Fatalf("没数据不应发通知事件：%v", rv.events)
	}
	if len(s.stats["missing"].([]string)) == 0 {
		t.Fatalf("missing 应列出缺数据的交易日：%v", s.stats)
	}
}

func TestGenerateWeeklyReviewDegradedModelStillNotifies(t *testing.T) {
	uc, _, rv := newWeeklyEnv(t, func(req llm.Request) (string, error) {
		return "", fmt.Errorf("boom")
	})
	if err := uc.GenerateWeeklyReview(context.Background(), weeklyDay(t, "2026-10-16")); err != nil {
		t.Fatal(err)
	}
	s := rv.saved["2026-10-16|ai_week"]
	if !strings.Contains(s.content, "模型未参与") {
		t.Fatalf("降级正文 = %q", s.content)
	}
	if !s.stats["degraded"].(bool) || !s.stats["partial"].(bool) {
		t.Fatalf("stats = %v", s.stats)
	}
	if len(s.lessons) == 0 {
		t.Fatalf("降级也应有兜底教训：%v", s.lessons)
	}
	if len(rv.events) != 1 || rv.events[0].Body != s.content {
		t.Fatalf("降级仍应发通知：%v", rv.events)
	}
}

func TestGenerateWeeklyReviewScrubCountsFabricatedNumbers(t *testing.T) {
	uc, _, rv := newWeeklyEnv(t, func(req llm.Request) (string, error) {
		if req.Task == "review.weekly" {
			return `{"summary":"本周情绪回暖，预计下周有999只涨停。","lessons":["晋级率从28%回升到42.5%","算力5天上榜累计46家"]}`, nil
		}
		return "", fmt.Errorf("unexpected task %s", req.Task)
	})
	if err := uc.GenerateWeeklyReview(context.Background(), weeklyDay(t, "2026-10-16")); err != nil {
		t.Fatal(err)
	}
	s := rv.saved["2026-10-16|ai_week"]
	if !strings.Contains(s.content, "（略※）") {
		t.Fatalf("无出处数字应被替换：%q", s.content)
	}
	if s.stats["omitted_count"].(int) < 1 {
		t.Fatalf("omitted_count 应 ≥1：%v", s.stats)
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

func TestWeeklyCalendarHelpers(t *testing.T) {
	uc, _, _ := newWeeklyEnv(t, weeklyReply)
	// 周五是本周最后交易日；周三不是。
	last, err := uc.IsWeekLastOpenDay(weeklyDay(t, "2026-10-16"))
	if err != nil || !last {
		t.Fatalf("2026-10-16 应为本周最后交易日：%v %v", last, err)
	}
	last, err = uc.IsWeekLastOpenDay(weeklyDay(t, "2026-10-15"))
	if err != nil || last {
		t.Fatalf("2026-10-15 不应为本周最后交易日：%v %v", last, err)
	}
	// 周末选日期也落回本周五。
	end, err := uc.weekLastOpenDay(weeklyDay(t, "2026-10-18"))
	if err != nil || end.Format("2006-01-02") != "2026-10-16" {
		t.Fatalf("周日应对齐到本周五：%s %v", end.Format("2006-01-02"), err)
	}
	// 下周一属于下一 ISO 周。
	end, err = uc.weekLastOpenDay(weeklyDay(t, "2026-10-19"))
	if err != nil || end.Format("2006-01-02") != "2026-10-23" {
		t.Fatalf("下周一应对齐到下周五：%s %v", end.Format("2006-01-02"), err)
	}
	// 非法 kind 读出口拒收。
	if _, _, _, _, _, err := uc.Review(context.Background(), weeklyDay(t, "2026-10-16"), "week"); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("非法 kind 应报 bad request：%v", err)
	}
}
