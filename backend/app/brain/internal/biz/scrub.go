package biz

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// scrub.go —— P1-5 AI 数字三档核对（对标 vibe-astock verifier 口径）：
// ①sourced：能在事实包里找到出处（事实标签或文本片段）；
// ②recomputed：正文写了算式能由事实包数字两两四则运算重算通过（容差同 matches）；
// ③omitted：无出处，正文数字替换为「（略※）」并计数。
// 核对留痕 EvidenceItem 随晨报落库，前端可逐条回查依据。

const scrubPlaceholder = "（略※）"

// closureMaxFacts 算式重算档的运算池上限：对数是 O(n²)，超过就整档跳过。
const closureMaxFacts = 200

// EvidenceItem 是一条数字核对留痕。
type EvidenceItem struct {
	Field   string `json:"field"`           // 字段归属：headline / market_view / risks[0] / watchlist.600519 / positions.000002
	Span    string `json:"span"`            // 正文中的数字 token
	Verdict string `json:"verdict"`         // sourced / recomputed / omitted
	Match   string `json:"match,omitempty"` // sourced=出处；recomputed=算式；omitted 为空
}

// closureEntry 是算式重算档的一个候选值：两个事实数字经一次四则运算得到。
type closureEntry struct {
	v    float64
	expr string
}

// buildClosure 用事实原始值的两两四则运算（+、|−|、×、÷ 双向）预生成重算档校验池。
func buildClosure(base []float64) []closureEntry {
	if len(base) > closureMaxFacts {
		return nil
	}
	var out []closureEntry
	for i, a := range base {
		for j, b := range base {
			if i == j {
				continue
			}
			if c := a + b; sane(c) {
				out = append(out, closureEntry{c, opStr(a) + " + " + opStr(b)})
			}
			if c := math.Abs(a - b); sane(c) {
				out = append(out, closureEntry{c, "|" + opStr(a) + " − " + opStr(b) + "|"})
			}
			if c := a * b; sane(c) {
				out = append(out, closureEntry{c, opStr(a) + " × " + opStr(b)})
			}
			if b != 0 {
				if c := a / b; sane(c) {
					out = append(out, closureEntry{c, opStr(a) + " ÷ " + opStr(b)})
				}
			}
		}
	}
	return out
}

func sane(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && math.Abs(v) < 1e15
}

// opStr 写算式的数字段，保留 4 位小数避免浮点尾巴污染算式展示。
func opStr(v float64) string {
	return strconv.FormatFloat(round(v, 4), 'f', -1, 64)
}

// closureMatch 判断数字能否由算式重算得到，返回算式文本。
func (s numberSet) closureMatch(x float64, decimals int, pct bool) (string, bool) {
	for _, e := range s.closure {
		if matches(x, decimals, e.v) || (pct && matches(x, decimals, e.v*100)) {
			return e.expr, true
		}
	}
	return "", false
}

// scrubText 对一段文本执行三档核对并返回修复后的文本、留痕和略掉的数字个数。
func (s numberSet) scrubText(src string) (string, []EvidenceItem, int) {
	locs := numberToken.FindAllStringIndex(src, -1)
	if len(locs) == 0 {
		return src, nil, 0
	}
	var fixed strings.Builder
	var items []EvidenceItem
	omitted := 0
	last := 0
	for _, loc := range locs {
		tok := src[loc[0]:loc[1]]
		v, dec, pct, ok := parseNumber(tok)
		if !ok {
			continue
		}
		if srcLabel, found := s.matchOf(v, dec, pct); found {
			items = append(items, EvidenceItem{Span: tok, Verdict: "sourced", Match: srcLabel})
			continue
		}
		if expr, found := s.closureMatch(v, dec, pct); found {
			items = append(items, EvidenceItem{Span: tok, Verdict: "recomputed", Match: expr})
			continue
		}
		fixed.WriteString(src[last:loc[0]])
		fixed.WriteString(scrubPlaceholder)
		last = loc[1]
		items = append(items, EvidenceItem{Span: tok, Verdict: "omitted"})
		omitted++
	}
	fixed.WriteString(src[last:])
	return fixed.String(), items, omitted
}

type scrubField struct {
	name string
	p    *string
}

// scrubBriefing 对晨报正文各字段做三档核对并原地修复 ③，返回去重后的留痕与略掉总数。
func scrubBriefing(nums numberSet, b *Briefing) (items []EvidenceItem, omitted int) {
	fields := []scrubField{{"headline", &b.Headline}, {"market_view", &b.MarketView}}
	for i := range b.Risks {
		fields = append(fields, scrubField{fmt.Sprintf("risks[%d]", i), &b.Risks[i]})
	}
	for i := range b.Watchlist {
		fields = append(fields, scrubField{"watchlist." + b.Watchlist[i].Symbol, &b.Watchlist[i].Reason})
	}
	for i := range b.Positions {
		fields = append(fields, scrubField{"positions." + b.Positions[i].Symbol, &b.Positions[i].View})
	}
	return scrubFields(nums, fields)
}

// scrubFields 对一组带名字的文本字段做三档核对并原地修复，返回去重后的留痕与略掉总数。
// 分段复盘（P1-7）复用同一条核对流水线。
func scrubFields(nums numberSet, fields []scrubField) (items []EvidenceItem, omitted int) {
	seen := map[string]bool{}
	for _, f := range fields {
		text, found, n := nums.scrubText(*f.p)
		*f.p = text
		omitted += n
		for _, it := range found {
			key := it.Span + "|" + it.Verdict + "|" + it.Match
			if it.Verdict == "omitted" {
				key += "|" + f.name // 每处「（略※）」都要留痕，不跨字段去重
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			it.Field = f.name
			items = append(items, it)
		}
	}
	return items, omitted
}
