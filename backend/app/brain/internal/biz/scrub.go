package biz

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// scrub.go —— P1-5 AI 数字三档核对（对标 vibe-astock verifier 口径）+ 量级档：
// ①sourced：能在事实包里找到出处（事实标签或文本片段）；
// ②recomputed：正文写了算式能由事实包数字两两四则运算重算通过（容差同 matches）；
// ③omitted：无出处，正文数字替换为「（略※）」并计数；
// ④magnitude：有效数字基本一致但差 ≥3 个数量级——vibe number_fidelity 的
//   writtenScale/evidenceScale 核对；比无出处更危险，正文同样替换并计数，但留痕单列
//   verdict，可从普通无出处里筛出来。万/亿/万亿 单位后缀按真实量纲折算进判定
//   （Fact.Unit 同尺），所以旗舰场景真可达：1.2 万写成 1.2 亿 → 真实量纲失配 →
//   原值/算式双双落空 → 量级档命中（若被 ÷1万 系数位兜住盖章，就是在替错数背书）。
// 核对留痕 EvidenceItem 随晨报落库，前端可逐条回查依据。

const scrubPlaceholder = "（略※）"

// closureMaxFacts 算式重算档的运算池上限：对数是 O(n²)，超过就整档跳过。
const closureMaxFacts = 200

// EvidenceItem 是一条数字核对留痕。
type EvidenceItem struct {
	Field   string `json:"field"`           // 字段归属：headline / market_view / risks[0] / watchlist.600519 / positions.000002
	Span    string `json:"span"`            // 正文中的数字 token
	Verdict string `json:"verdict"`         // sourced / recomputed / omitted / magnitude（量级畸变，按 omitted 计数）
	Match   string `json:"match,omitempty"` // sourced=出处；recomputed=算式；magnitude=出处+量级位移；omitted 为空
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

// magnitudeMatch 量级档：有效数字与某事实基本一致、但差 ≥3 个数量级时返回出处。
// 只在 sourced/recomputed 都失败后才判定；命中按不可信处理（正文替换），
// 留痕 Match 带出处与量级位移，便于和纯编造区分。
func (s numberSet) magnitudeMatch(x float64, decimals int, pct bool) (string, bool) {
	if x == 0 {
		return "", false
	}
	ax := math.Abs(x)
	for i, v := range s.orig {
		// 个位数锚（规则编号、板数、家数）不做量级锚：999≈1×10³ 这类假阳性有害无益。
		if v == 0 || math.Abs(v) < 10 || math.Signbit(v) != math.Signbit(x) {
			continue
		}
		shift := math.Round(math.Log10(ax / math.Abs(v)))
		if math.Abs(shift) < 3 {
			continue
		}
		// 量级档只归责「记账单位级失配」：在册单位错位的最大跨度是 元↔亿=10^8
		// （万↔亿=10^4、元↔万=10^4），≥10^9 的位移不可能由单位错写造成（实测回归：
		// 编造的 999 对 10^12 成交额锚是 10^-9、解禁案 2亿 误配日期锚 20 是 10^7）——
		// 那更可能是纯粹编造，按无出处 omitted 走，不做量级畸变归责。
		if math.Abs(shift) > 8 {
			continue
		}
		scaled := x / math.Pow(10, shift)
		if matches(scaled, decimals, v) || (pct && matches(scaled, decimals, v*100)) {
			return fmt.Sprintf("%s（量级差 10^%d）", s.origSrc[i], int(shift)), true
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
		v, scale, dec, pct, ok := parseNumber(tok)
		if !ok {
			continue
		}
		// 带单位 token 断言了量纲：先按真实量纲直查；无单位走旧口径（含系数换算位）。
		if scale > 1 {
			v *= scale
			if srcLabel, found := s.matchOfTrue(v, dec); found {
				items = append(items, EvidenceItem{Span: tok, Verdict: "sourced", Match: srcLabel})
				continue
			}
		} else if srcLabel, found := s.matchOf(v, dec, pct); found {
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
		if srcLabel, found := s.magnitudeMatch(v, dec, pct); found {
			items = append(items, EvidenceItem{Span: tok, Verdict: "magnitude", Match: srcLabel})
		} else {
			items = append(items, EvidenceItem{Span: tok, Verdict: "omitted"})
		}
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
			if it.Verdict == "omitted" || it.Verdict == "magnitude" {
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
