package kb

import (
	"reflect"
	"strings"
	"testing"

	"server/app/intel/internal/biz/kb/extract"
)

func TestTokenize(t *testing.T) {
	got := Tokenize("贵州茅台(600519.SH)弱转强，涨！ＡＴＲ")
	want := []string{"贵州", "州茅", "茅台", "600519", "sh", "弱转", "转强", "涨", "atr"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Tokenize = %q", got)
	}
}

func TestLexemesKeepPositions(t *testing.T) {
	lx := Lexemes("弱转强 弱转")
	if len(lx) != 2 || lx[0].Word != "弱转" || !reflect.DeepEqual(lx[0].Positions, []int{1, 3}) {
		t.Fatalf("Lexemes = %+v", lx)
	}
}

func TestQueryTermsDedup(t *testing.T) {
	if got := QueryTerms("回踩 回踩 ma5"); !reflect.DeepEqual(got, []string{"回踩", "ma5"}) {
		t.Fatalf("QueryTerms = %q", got)
	}
}

func TestEstimateTokens(t *testing.T) {
	cases := map[string]int{
		"":                  0,
		"弱转强":               3,
		"hello world":       4,
		"ATR，止损":            4,
		"600519.SH":         4,
		"  \n\t":            0,
		"abcdabcdabcdabcd1": 5,
	}
	for in, want := range cases {
		if got := EstimateTokens(in); got != want {
			t.Errorf("EstimateTokens(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestSplitChunksRespectsHardCap(t *testing.T) {
	long := strings.Repeat("首板次日低开后快速翻红视为弱转强。", 120)
	sections := []extract.Section{
		{Heading: "打法 > 弱转强", Paragraphs: []string{long}},
		{Heading: "打法 > 强势回踩", Paragraphs: []string{"缩量回踩五日线，次日放量反包。"}},
	}
	chunks := SplitChunks(sections, DefaultChunkOptions)
	if len(chunks) < 4 {
		t.Fatalf("got %d chunks", len(chunks))
	}
	for i, c := range chunks {
		if c.Seq != i {
			t.Fatalf("seq %d at %d", c.Seq, i)
		}
		if n := EstimateTokens(c.Heading) + 1 + EstimateTokens(c.Content); n > DefaultChunkOptions.Max {
			t.Fatalf("chunk %d has %d tokens", i, n)
		}
	}
	last := chunks[len(chunks)-1]
	if last.Heading != "打法 > 强势回踩" || !strings.Contains(last.EmbedText(), "打法 > 强势回踩\n") {
		t.Fatalf("sections must not merge: %+v", last)
	}
	// 同一章节内相邻块重叠一句。
	if !strings.HasPrefix(chunks[1].Content, "首板次日低开后快速翻红视为弱转强。") {
		t.Fatalf("missing overlap: %q", chunks[1].Content[:30])
	}
}

func TestSplitChunksMergesShortTail(t *testing.T) {
	// 476 + 5 超过目标 480 会先切开，再因尾块太短且合并后不超 500 而并回。
	para := strings.Repeat("情绪发酵", 119)
	tail := "尾巴很短。"
	chunks := SplitChunks([]extract.Section{{Paragraphs: []string{para, tail}}}, DefaultChunkOptions)
	if len(chunks) != 1 || !strings.HasSuffix(chunks[0].Content, tail) {
		t.Fatalf("short tail should merge into previous chunk: %d chunks", len(chunks))
	}
}

func TestSplitChunksHardSplitsHugeSentence(t *testing.T) {
	huge := strings.Repeat("字", 1300)
	chunks := SplitChunks([]extract.Section{{Paragraphs: []string{huge}}}, DefaultChunkOptions)
	total := 0
	for _, c := range chunks {
		if c.Tokens > DefaultChunkOptions.Max {
			t.Fatalf("chunk tokens %d", c.Tokens)
		}
		total += len([]rune(c.Content))
	}
	if total != 1300 {
		t.Fatalf("hard split lost text: %d runes", total)
	}
}

func TestFuseRRF(t *testing.T) {
	got := FuseRRF(60, []int{1, 2, 3}, []int{3, 4, 1})
	order := []int{got[0].ID, got[1].ID, got[2].ID, got[3].ID}
	if !reflect.DeepEqual(order, []int{1, 3, 2, 4}) {
		t.Fatalf("order = %v", order)
	}
	if !reflect.DeepEqual(got[0].Ranks, []int{1, 3}) || !reflect.DeepEqual(got[3].Ranks, []int{0, 2}) {
		t.Fatalf("ranks = %+v", got)
	}
	if got := FuseRRF(60, nil, []int{7, 7, 8}); len(got) != 2 || got[0].ID != 7 {
		t.Fatalf("single list = %+v", got)
	}
}

func TestOutcomeAndMarkdown(t *testing.T) {
	up, down := 1.5, -0.3
	for _, c := range []struct {
		status string
		pnl    *float64
		want   string
	}{
		{"closed", &up, "win"}, {"closed", &down, "loss"}, {"closed", nil, "flat"},
		{"expired", &up, "unfilled"}, {"cancelled", nil, "cancelled"},
	} {
		if got := outcomeOf(c.status, c.pnl); got != c.want {
			t.Errorf("outcomeOf(%s) = %s, want %s", c.status, got, c.want)
		}
	}
	md := caseMarkdown(SignalCase{Book: "live", StockCode: "000001.SZ", Pattern: "强势回踩", Attribution: "追高"},
		CaseRecord{Outcome: "loss"})
	for _, want := range []string{"账本：实盘", "形态：强势回踩", "结果：亏损", "归因：追高"} {
		if !strings.Contains(md, want) {
			t.Fatalf("markdown lacks %q:\n%s", want, md)
		}
	}
}
