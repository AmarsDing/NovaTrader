package biz

import (
	"strings"
	"testing"
)

func scrubFacts() []Fact {
	return []Fact{
		{ID: "F:q.limit_up", Label: "涨停家数", Value: 42, Unit: "家"},
		{ID: "F:q.amount", Label: "成交额", Value: 7400, Unit: "亿"},
		{ID: "F:t.text", Text: "纯文本事实不进运算池"},
	}
}

func TestScrubSourcedCarriesOrigin(t *testing.T) {
	nums := newNumberSet(scrubFacts(), "盘前阅读时长约 3 分钟")
	text, items, omitted := nums.scrubText("市场涨停家数 42 家，成交额 7400 亿，阅读约 3 分钟")
	if omitted != 0 || text != "市场涨停家数 42 家，成交额 7400 亿，阅读约 3 分钟" {
		t.Fatalf("有出处数字不应被略: %q omitted=%d", text, omitted)
	}
	if len(items) != 3 {
		t.Fatalf("items 应为 3 条，实际 %d", len(items))
	}
	if items[0].Verdict != "sourced" || items[0].Match != "涨停家数" {
		t.Fatalf("事实出处不对: %+v", items[0])
	}
	if items[2].Verdict != "sourced" || !strings.Contains(items[2].Match, "阅读时长约 3") {
		t.Fatalf("文本出处应为片段: %+v", items[2])
	}
}

func TestScrubRecomputedByClosure(t *testing.T) {
	nums := newNumberSet(scrubFacts())
	// 7400 + 42 = 7442；两个数相除 7400 ÷ 42 ≈ 176.19
	text, items, omitted := nums.scrubText("两项合计 7442，比值约 176.19")
	if omitted != 0 {
		t.Fatalf("可重算数字不应被略: %q omitted=%d", text, omitted)
	}
	if len(items) != 2 || items[0].Verdict != "recomputed" || !strings.Contains(items[0].Match, "+") {
		t.Fatalf("加法算式重算未命中: %+v", items)
	}
	if items[1].Verdict != "recomputed" || !strings.Contains(items[1].Match, "÷") {
		t.Fatalf("除法算式重算未命中: %+v", items[1])
	}
}

func TestScrubOmitReplacesToken(t *testing.T) {
	nums := newNumberSet(scrubFacts())
	text, items, omitted := nums.scrubText("昨日美股涨了 88 个点，成交价 17 元")
	if omitted != 2 {
		t.Fatalf("88/17 均无出处，omitted 应为 2，实际 %d", omitted)
	}
	want := "昨日美股涨了 " + scrubPlaceholder + " 个点，成交价 " + scrubPlaceholder + " 元"
	if text != want {
		t.Fatalf("文本替换不对: got %q want %q", text, want)
	}
	for _, it := range items {
		if it.Verdict != "omitted" || it.Match != "" {
			t.Fatalf("omitted 留痕不对: %+v", it)
		}
	}
}

func TestScrubPercentHint(t *testing.T) {
	facts := []Fact{{ID: "F:q.seal_rate", Label: "封板率", Value: 0.618}}
	nums := newNumberSet(facts)
	text, items, omitted := nums.scrubText("封板率约 61.8%")
	if omitted != 0 || text != "封板率约 61.8%" {
		t.Fatalf("百分比应命中 ×100 出处: %q omitted=%d", text, omitted)
	}
	if items[0].Verdict != "sourced" || items[0].Match != "封板率" {
		t.Fatalf("百分比出处不对: %+v", items[0])
	}
}

func TestClosureSkipWhenPoolTooLarge(t *testing.T) {
	var facts []Fact
	for i := 0; i < closureMaxFacts+1; i++ {
		facts = append(facts, Fact{ID: "F:x", Label: "x", Value: float64(i)})
	}
	nums := newNumberSet(facts)
	if len(nums.closure) != 0 {
		t.Fatalf("运算池超限应整档跳过，仍有 %d 条", len(nums.closure))
	}
}

func TestScrubBriefingDedupesAndCounts(t *testing.T) {
	nums := newNumberSet(scrubFacts())
	b := &Briefing{
		Headline:   "涨停 42 家",
		MarketView: "成交额 7400 亿，开盘前留意 88 点缺口；再次提到 42 家",
		Risks:      []string{"外围波动 88 点"},
		Watchlist:  []WatchItem{{Symbol: "600519", Reason: "成交额 7400 亿"}},
		Positions:  []PositionView{{Symbol: "000001", View: "成本 7400 元"}},
	}
	items, omitted := scrubBriefing(nums, b)
	if omitted != 2 {
		t.Fatalf("market_view 与 risks 各一个 88，omitted 应为 2，实际 %d", omitted)
	}
	if !strings.Contains(b.MarketView, scrubPlaceholder+" 点缺口") || !strings.Contains(b.Risks[0], scrubPlaceholder) {
		t.Fatalf("③ 档未原地替换: view=%q risks=%q", b.MarketView, b.Risks)
	}
	if b.Watchlist[0].Reason != "成交额 7400 亿" || b.Positions[0].View != "成本 7400 元" {
		t.Fatalf("① 档文本应保持不动: %+v %+v", b.Watchlist[0], b.Positions[0])
	}
	// 42 出现两次应去重成一条 sourced；88 两次各一条 omitted。
	var sourced42, omitted88 int
	for _, it := range items {
		if it.Span == "42" && it.Verdict == "sourced" {
			sourced42++
		}
		if it.Span == "88" && it.Verdict == "omitted" {
			omitted88++
		}
	}
	if sourced42 != 1 || omitted88 != 2 {
		t.Fatalf("留痕去重不对: sourced42=%d omitted88=%d items=%+v", sourced42, omitted88, items)
	}
	var haveRiskField bool
	for _, it := range items {
		if strings.HasPrefix(it.Field, "risks[") {
			haveRiskField = true
		}
	}
	if !haveRiskField {
		t.Fatalf("字段归属缺失 risks: %+v", items)
	}
}
