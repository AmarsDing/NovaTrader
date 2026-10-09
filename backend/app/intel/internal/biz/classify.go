package biz

import (
	"fmt"
	"math"
	"strings"
)

// 事件类型，见设计文档第 6 节。
const (
	EventEarnings        = "earnings"
	EventHoldingIncrease = "holding_increase"
	EventHoldingDecrease = "holding_decrease"
	EventBuyback         = "buyback"
	EventPenalty         = "penalty"
	EventDelistingRisk   = "delisting_risk"
	EventRestructuring   = "restructuring"
	EventContract        = "contract"
	EventUnlock          = "unlock"
	EventSuspension      = "suspension"
	EventPolicy          = "policy"
	EventOther           = "other"
)

const (
	minute = 1
	hour   = 60 * minute
	day    = 24 * hour
)

type eventRule struct {
	name       string
	words      []string
	sentiment  float64
	importance int
	halfLife   int // 分钟；0 表示按 kind
}

// eventRules 的顺序就是同分时的优先级。
var eventRules = []eventRule{
	{EventDelistingRisk, []string{"退市风险警示", "终止上市", "退市整理", "强制退市", "暂停上市"}, -0.9, 5, 10 * day},
	{EventPenalty, []string{"立案调查", "立案告知", "行政处罚", "处罚决定", "警示函", "监管函", "纪律处分", "公开谴责", "立案"}, -0.7, 4, 10 * day},
	{EventRestructuring, []string{"重大资产重组", "资产重组", "重组", "收购", "并购", "吸收合并", "借壳"}, 0.4, 4, 5 * day},
	{EventEarnings, []string{"业绩预告", "业绩快报", "预增", "预减", "扭亏", "首亏", "续亏", "略增", "略减", "净利润", "年度报告", "半年度报告", "季度报告", "营业收入"}, 0, 3, 5 * day},
	{EventBuyback, []string{"回购"}, 0.4, 3, 3 * day},
	{EventHoldingIncrease, []string{"增持"}, 0.5, 3, 3 * day},
	{EventHoldingDecrease, []string{"减持"}, -0.5, 3, 3 * day},
	{EventUnlock, []string{"解禁", "限售股上市流通", "限售股份上市流通"}, -0.3, 2, 2 * day},
	{EventContract, []string{"中标", "签订合同", "签署合同", "重大合同", "战略合作协议", "框架协议"}, 0.3, 2, 2 * day},
	{EventSuspension, []string{"停牌", "复牌"}, 0, 3, 1 * day},
	{EventPolicy, []string{"国务院", "证监会", "央行", "人民银行", "发改委", "工信部", "财政部", "印发", "政策"}, 0, 3, 2 * day},
}

var eventNames = func() map[string]bool {
	m := map[string]bool{EventOther: true}
	for _, r := range eventRules {
		m[r.name] = true
	}
	return m
}()

var kindHalfLife = map[string]int{
	KindFlash:        4 * hour,
	KindNews:         12 * hour,
	KindReport:       3 * day,
	KindSocial:       2 * hour,
	KindAnnouncement: 1 * day,
}

var (
	positiveWords = []string{"增长", "预增", "扭亏", "超预期", "中标", "获批", "上调", "突破", "创新高", "大增", "盈利", "利好"}
	negativeWords = []string{"下滑", "下降", "亏损", "预减", "首亏", "违规", "终止", "风险", "下调", "暴跌", "诉讼", "冻结", "利空", "失败"}
)

// 只看正文开头，后面多是模板条款，会把极性词刷满。
const contentHead = 500

// RuleScore 用关键词给事件分类、打情感和重要度。总是可用，是模型失败时的兜底。
// 常规公告（回购、增减持、业绩）停在 3，重要度 4 以上只留给处罚、重组、退市风险，避免盘后公告潮刷屏告警。
func RuleScore(it *Item) Score {
	title := it.Title
	head := firstRunes(it.Content, contentHead)
	best, bestHits := -1, 0
	for i, r := range eventRules {
		hits := 3*countAny(title, r.words) + countAny(head, r.words)
		if hits > bestHits {
			best, bestHits = i, hits
		}
	}
	s := Score{EventType: EventOther, Importance: 1, Scorer: ScorerRule}
	base := 0.0
	if best >= 0 {
		r := eventRules[best]
		s.EventType, s.Importance, s.HalfLifeMinutes, base = r.name, r.importance, r.halfLife, r.sentiment
	}
	if s.HalfLifeMinutes == 0 {
		s.HalfLifeMinutes = HalfLife(it.Kind, s.EventType)
	}
	pos := 2*countAny(title, positiveWords) + countAny(head, positiveWords)
	neg := 2*countAny(title, negativeWords) + countAny(head, negativeWords)
	polarity := clamp(0.15*float64(pos-neg), -0.6, 0.6)
	s.Sentiment = round4(clamp(base+polarity, -1, 1))
	if it.Kind == KindSocial {
		s.Importance--
	}
	s.Importance = clampInt(s.Importance, 0, 5)
	s.Reason = fmt.Sprintf("规则：事件=%s 正向词=%d 负向词=%d", s.EventType, pos, neg)
	return s
}

// HalfLife 返回事件类型的半衰期；类型没有固定值时按情报种类。
func HalfLife(kind, eventType string) int {
	for _, r := range eventRules {
		if r.name == eventType && r.halfLife > 0 {
			return r.halfLife
		}
	}
	if v, ok := kindHalfLife[kind]; ok {
		return v
	}
	return kindHalfLife[KindNews]
}

func countAny(s string, words []string) int {
	n := 0
	for _, w := range words {
		n += strings.Count(s, w)
	}
	return n
}

func firstRunes(s string, n int) string {
	i := 0
	for pos := range s {
		if i == n {
			return s[:pos]
		}
		i++
	}
	return s
}

func clamp(v, lo, hi float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	return math.Max(lo, math.Min(hi, v))
}

func clampInt(v, lo, hi int) int {
	return max(lo, min(hi, v))
}

func round4(v float64) float64 {
	return math.Round(v*10000) / 10000
}
