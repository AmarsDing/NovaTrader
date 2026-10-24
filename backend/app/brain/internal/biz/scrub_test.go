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

// TestScrubMagnitudeTier 量级档：数字对但差 ≥3 个数量级（1.2 万写成 1.2 亿）单列 magnitude 留痕。
func TestScrubMagnitudeTier(t *testing.T) {
	facts := []Fact{{ID: "F:flow.main", Label: "主力净流入", Value: 12000}}
	nums := newNumberSet(facts)
	text, items, omitted := nums.scrubText("主力净流入 12000000 元")
	if omitted != 1 || !strings.Contains(text, scrubPlaceholder) {
		t.Fatalf("量级畸变应替换并计数: %q omitted=%d", text, omitted)
	}
	if len(items) != 1 || items[0].Verdict != "magnitude" || !strings.Contains(items[0].Match, "主力净流入") {
		t.Fatalf("magnitude 留痕不对: %+v", items)
	}
	// 同值不触发量级档；差一个数量级（10^1）也不触发（12000×10 属内置换算位，直接断言判据）。
	okText, okItems, okOmitted := nums.scrubText("主力净流入 12000 元")
	if okOmitted != 0 || okText != "主力净流入 12000 元" || okItems[0].Verdict != "sourced" {
		t.Fatalf("同值应命中 sourced: %q %+v", okText, okItems)
	}
	if _, found := nums.magnitudeMatch(120000, 0, false); found {
		t.Fatal("10^1 差不该触发量级档")
	}
}

// TestScrubChineseUnitFlagshipReachesMagnitude 旗舰场景（W3 复核第③条根修）：
// 事实口径 1.2 万（Value 12000、无单位），正文写成「1.2亿」——带单位 token 断言了量纲，
// 必须按真实量纲核对：1.2e8 在 orig 锚（12000）上差 10^4 → 量级档；若被 ÷1万 系数位
// 兜住盖 sourced 章，就是在替 10^4 级错数背书。（units 需紧贴数字才入 token，正则口径如此。）
func TestScrubChineseUnitFlagshipReachesMagnitude(t *testing.T) {
	facts := []Fact{{ID: "F:flow.main", Label: "主力净流入", Value: 12000}}
	nums := newNumberSet(facts)
	text, items, omitted := nums.scrubText("今日主力净流入约1.2亿")
	if omitted != 1 || !strings.Contains(text, scrubPlaceholder) {
		t.Fatalf("1.2 万写成 1.2亿 应替换并计数: %q omitted=%d", text, omitted)
	}
	if len(items) != 1 || items[0].Verdict != "magnitude" || !strings.Contains(items[0].Match, "10^4") {
		t.Fatalf("量级留痕应为 magnitude 且带 10^4 位移: %+v", items)
	}
	if !strings.Contains(items[0].Match, "主力净流入") {
		t.Fatalf("量级留痕应指出处: %+v", items[0])
	}
}

// TestScrubUnitTokenMatchesUnitFact 带单位 token 与带 Unit 事实的同尺命中：
// 事实口径 7400/亿（7.4e11），正文「7400亿」必须仍是 sourced——根修若把系数空间
// 错当唯一判据，会把这条正道扳成量级档。
func TestScrubUnitTokenMatchesUnitFact(t *testing.T) {
	nums := newNumberSet(scrubFacts())
	text, items, omitted := nums.scrubText("两市成交额7400亿")
	if omitted != 0 || text != "两市成交额7400亿" {
		t.Fatalf("同口径带单位数字不应被略: %q omitted=%d", text, omitted)
	}
	if len(items) != 1 || items[0].Verdict != "sourced" || items[0].Match != "成交额" {
		t.Fatalf("7400亿应命中成交额事实: %+v", items)
	}
	// 同事实但正文误写为「7400万」：真实量纲差 10^4（向下），进量级档而非侥幸 sourced。
	text2, items2, omitted2 := nums.scrubText("两市成交额7400万")
	if omitted2 != 1 || !strings.Contains(text2, scrubPlaceholder) {
		t.Fatalf("7400亿口径写成 7400万 应替换并计数: %q omitted=%d", text2, omitted2)
	}
	if len(items2) != 1 || items2[0].Verdict != "magnitude" || !strings.Contains(items2[0].Match, "10^-4") {
		t.Fatalf("7400万应为 magnitude 10^-4 留痕: %+v", items2)
	}
}
