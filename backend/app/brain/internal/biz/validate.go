package biz

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"server/pkg/llm"
)

var numberToken = regexp.MustCompile(`\d+(?:,\d{3})*(?:\.\d+)?(?:%|万亿|亿|万)?`)

const relTolerance = 0.005

// unitScaleOf 中文记账单位 → 真实量纲换算（万*→1e4、亿*→1e8、万亿→1e12）；不带单位返 1。
// 按前缀归并，覆盖在用的复合单位（亿元、万股、亿股等）：正文 token 的单位后缀只管
// 万/亿/万亿 三档（后面的「股/元/手」等量词不进 token），事实 Unit 必须与之同口径，
// 否则「2亿股」写成事实 2/亿股 时对不上真实值 2e8（W3 复核根修回归实证）。
func unitScaleOf(u string) float64 {
	switch {
	case strings.HasPrefix(u, "万亿"):
		return 1e12
	case strings.HasPrefix(u, "亿"):
		return 1e8
	case strings.HasPrefix(u, "万"):
		return 1e4
	default:
		return 1
	}
}

// numberSet 是本次输入里出现过的数字及其出处。输出正文里的数字按四档判定（P1-5，
// vibe-astock verifier 口径 + 量级档）：①事实包内有出处（sourced）②算式重算通过
// （recomputed）③无出处（omitted，正文替换）④量级畸变（magnitude，按 omitted 计数）。
// 本地判定只在事实包以外纯文本数字时发生。带 万/亿/万亿 单位的正文 token 按真实量纲
// 核对（单位是对量纲的断言，不许被 ÷1万/÷1亿 换算位兜住——该场景走量级档）。
type numberSet struct {
	vals    []float64
	srcs    []string       // 与 vals 同长：事实标签或含该数字的文本片段
	closure []closureEntry // 由事实原始值两两四则运算预生成（算式重算档）
	// orig/origSrc 是「真实量纲」的证据锚（Fact.Unit 折进 Value、文本单位后缀折进数值），
	// 只供量级档（scrub.magnitudeMatch）对照 writtenScale vs evidenceScale、以及带单位
	// token 的直查（matchOfTrue）用；无单位 token 的旧口径匹配仍走 vals 的换算位。
	orig    []float64
	origSrc []string
}

// newNumberSet 收集输入文本里的数字并记下出处，给数值因子补上常见换算（×100、÷100、÷1万、÷1亿），
// 再用事实原始值两两运算生成算式重算档的校验池。文本数字按真实量纲入 orig（单位后缀折进）；
// 事实的 orig 锚同样取真实量纲（Value×Unit 换算）。
func newNumberSet(facts []Fact, texts ...string) numberSet {
	var s numberSet
	for _, t := range texts {
		for _, loc := range numberToken.FindAllStringIndex(t, -1) {
			if v, sc, _, _, ok := parseNumber(t[loc[0]:loc[1]]); ok {
				val := v * sc
				s.vals = append(s.vals, val)
				s.srcs = append(s.srcs, excerpt(t, loc))
				s.orig = append(s.orig, val)
				s.origSrc = append(s.origSrc, excerpt(t, loc))
			}
		}
	}
	var base []float64
	for _, f := range facts {
		if f.Text != "" {
			continue
		}
		v := math.Abs(f.Value)
		base = append(base, v)
		s.vals = append(s.vals, v, v*100, v/100, v/1e4, v/1e8)
		for range 5 {
			s.srcs = append(s.srcs, f.Label)
		}
		// 真实量纲锚：Fact.Unit 显式带单位的折进数值（成交额=7400/亿 → 7.4e11）。
		// 系数空间照旧留在 vals 供无单位 token 匹配，两边各归各。
		s.orig = append(s.orig, v*unitScaleOf(f.Unit))
		s.origSrc = append(s.origSrc, f.Label)
	}
	s.closure = buildClosure(base)
	return s
}

// excerpt 截取 token 前后的文本片段（前后各 excerptRunes 个字符），作为文本数字的出处。
const excerptRunes = 18

func excerpt(text string, loc []int) string {
	runes := []rune(text)
	start := len([]rune(text[:loc[0]]))
	end := start + len([]rune(text[loc[0]:loc[1]]))
	from := max(0, start-excerptRunes)
	to := min(len(runes), end+excerptRunes)
	snip := string(runes[from:to])
	if from > 0 {
		snip = "…" + snip
	}
	if to < len(runes) {
		snip += "…"
	}
	return strings.TrimSpace(snip)
}

// parseNumber 解析数字 token。万/亿/万亿 后缀按中文记账单位折算返回 scale（>1 表示带单位；
// 百分号按系数↔百分比口径返回 pct，不与单位后缀同现）。调用方按 v×scale 落真实量纲。
func parseNumber(tok string) (v float64, scale float64, decimals int, pct bool, ok bool) {
	scale = 1
	switch {
	case strings.HasSuffix(tok, "%"):
		pct, tok = true, strings.TrimSuffix(tok, "%")
	default:
		for _, u := range []string{"万亿", "亿", "万"} {
			if strings.HasSuffix(tok, u) {
				scale, tok = unitScaleOf(u), strings.TrimSuffix(tok, u)
				break
			}
		}
	}
	tok = strings.ReplaceAll(tok, ",", "")
	if i := strings.IndexByte(tok, '.'); i >= 0 {
		decimals = len(tok) - i - 1
	}
	v, err := strconv.ParseFloat(tok, 64)
	if err != nil {
		return 0, 0, 0, false, false
	}
	return v, scale, decimals, pct, true
}

// matchOf 按输出的小数位匹配数字，返回出处（事实标签或文本片段）。
// 百分比文本允许按 ×100 命中（事实常以系数存、正文以 % 写）。
// 只供无单位 token：带单位 token 必须走 matchOfTrue——「1.2 亿」若被 ÷1万 换算位兜住
// 盖 sourced 章，等于替 10^4 级错数背书（W3 复核根因）。
func (s numberSet) matchOf(x float64, decimals int, pct bool) (string, bool) {
	for i, v := range s.vals {
		if matches(x, decimals, v) || (pct && matches(x, decimals, v*100)) {
			return s.srcs[i], true
		}
	}
	return "", false
}

// matchOfTrue 带单位 token 的直查：只在真实量纲锚（orig，Fact.Unit 与文本单位后缀
// 均已折进）上匹配，不碰系数空间的换算位。
func (s numberSet) matchOfTrue(x float64, decimals int) (string, bool) {
	for i, v := range s.orig {
		if matches(x, decimals, v) {
			return s.origSrc[i], true
		}
	}
	return "", false
}

func (s numberSet) has(x float64, decimals int, pct bool) bool {
	_, ok := s.matchOf(x, decimals, pct)
	return ok
}

func (s numberSet) hasTrue(x float64, decimals int) bool {
	_, ok := s.matchOfTrue(x, decimals)
	return ok
}

// matches：按输出的小数位四舍五入后相等，或相对误差不超过 0.5%。
func matches(x float64, decimals int, v float64) bool {
	if math.Abs(round(v, decimals)-x) < 1e-9 {
		return true
	}
	if v == 0 {
		return false
	}
	return math.Abs(x-v) <= relTolerance*math.Abs(v)
}

// check 返回正文里找不到出处的数字。带 万/亿/万亿 单位的 token 按真实量纲核对：
// 单位是对量纲的断言，失配必须算「找不到出处」，不许被换算位兜住（致死因同上）。
func (s numberSet) check(texts ...string) error {
	var bad []string
	for _, t := range texts {
		for _, m := range numberToken.FindAllString(t, -1) {
			v, scale, dec, pct, ok := parseNumber(m)
			if !ok {
				continue
			}
			if scale > 1 {
				if s.hasTrue(v*scale, dec) {
					continue
				}
			} else if s.has(v, dec, pct) {
				continue
			}
			bad = append(bad, m)
		}
	}
	if len(bad) > 0 {
		return fmt.Errorf("正文中的数字 %s 在事实包里找不到", strings.Join(bad, "、"))
	}
	return nil
}

func decodeJSON(content string, out any) error {
	if err := json.Unmarshal([]byte(llm.ExtractJSON(content)), out); err != nil {
		return fmt.Errorf("不是合法 JSON：%v", err)
	}
	return nil
}

func checkEvidence(ids map[string]bool, ev []string, min int, where string) error {
	if len(ev) < min {
		return fmt.Errorf("%s 至少引用 %d 个证据编号", where, min)
	}
	for _, id := range ev {
		if !ids[id] {
			return fmt.Errorf("%s 引用的编号 %q 不在事实包里", where, id)
		}
	}
	return nil
}

func checkLen(s string, max int, where string) error {
	if strings.TrimSpace(s) == "" {
		return fmt.Errorf("%s 为空", where)
	}
	if utf8.RuneCountInString(s) > max {
		return fmt.Errorf("%s 超过 %d 字", where, max)
	}
	return nil
}

type dimOut struct {
	Score   *float64 `json:"score"`
	Reasons []Reason `json:"reasons"`
	Risks   []string `json:"risks"`
}

const (
	maxReasons     = 5
	maxRisks       = 5
	maxReasonRunes = 200
	maxRiskRunes   = 100
)

func (o *dimOut) check(ids map[string]bool, nums numberSet) error {
	if o.Score == nil {
		return fmt.Errorf("缺少 score")
	}
	if *o.Score < 0 || *o.Score > 100 || math.IsNaN(*o.Score) {
		return fmt.Errorf("score 应在 0 到 100 之间")
	}
	if len(o.Reasons) == 0 || len(o.Reasons) > maxReasons {
		return fmt.Errorf("reasons 应为 1 到 %d 条", maxReasons)
	}
	if len(o.Risks) > maxRisks {
		return fmt.Errorf("risks 不超过 %d 条", maxRisks)
	}
	var texts []string
	for i, r := range o.Reasons {
		where := fmt.Sprintf("第 %d 条理由", i+1)
		if err := checkLen(r.Text, maxReasonRunes, where); err != nil {
			return err
		}
		if err := checkEvidence(ids, r.Evidence, 1, where); err != nil {
			return err
		}
		texts = append(texts, r.Text)
	}
	for i, r := range o.Risks {
		if err := checkLen(r, maxRiskRunes, fmt.Sprintf("第 %d 条风险", i+1)); err != nil {
			return err
		}
		texts = append(texts, r)
	}
	return nums.check(texts...)
}

func (o *dimOut) score() int {
	return int(math.Round(*o.Score))
}

type summaryOut struct {
	Summary  string   `json:"summary"`
	Evidence []string `json:"evidence"`
}

func (o *summaryOut) check(ids map[string]bool, nums numberSet) error {
	if err := checkLen(o.Summary, 400, "summary"); err != nil {
		return err
	}
	if err := checkEvidence(ids, o.Evidence, 1, "summary"); err != nil {
		return err
	}
	return nums.check(o.Summary)
}

type explainOut struct {
	Summary  string   `json:"summary"`
	Impact   string   `json:"impact"`
	Risks    []string `json:"risks"`
	Evidence []string `json:"evidence"`
}

func (o *explainOut) check(ids map[string]bool, nums numberSet) error {
	if err := checkLen(o.Summary, 300, "summary"); err != nil {
		return err
	}
	switch o.Impact {
	case "positive", "negative", "neutral":
	default:
		return fmt.Errorf("impact 只能是 positive、negative、neutral")
	}
	if len(o.Risks) > maxRisks {
		return fmt.Errorf("risks 不超过 %d 条", maxRisks)
	}
	for i, r := range o.Risks {
		if err := checkLen(r, maxRiskRunes, fmt.Sprintf("第 %d 条风险", i+1)); err != nil {
			return err
		}
	}
	if err := checkEvidence(ids, o.Evidence, 1, "evidence"); err != nil {
		return err
	}
	return nums.check(append([]string{o.Summary}, o.Risks...)...)
}

type askOut struct {
	Answer   string   `json:"answer"`
	Evidence []string `json:"evidence"`
}

func (o *askOut) check(ids map[string]bool, nums numberSet) error {
	if err := checkLen(o.Answer, 600, "answer"); err != nil {
		return err
	}
	if err := checkEvidence(ids, o.Evidence, 1, "evidence"); err != nil {
		return err
	}
	return nums.check(o.Answer)
}

type briefOut struct {
	Headline   string         `json:"headline"`
	MarketView string         `json:"market_view"`
	Risks      []string       `json:"risks"`
	Watchlist  []WatchItem    `json:"watchlist"`
	Positions  []PositionView `json:"positions"`
}

// check 做结构与证据编号校验（长度、编号、关注股在事实包、持仓看法只针对持仓）。
// 数字三档核对不在本步拦截——结构合格后由 scrubBriefing 原地修复并留痕（P1-5）。
func (o *briefOut) check(ids map[string]bool, known, held map[string]bool) error {
	if err := checkLen(o.Headline, 80, "headline"); err != nil {
		return err
	}
	if err := checkLen(o.MarketView, 600, "market_view"); err != nil {
		return err
	}
	if len(o.Risks) > maxRisks {
		return fmt.Errorf("risks 不超过 %d 条", maxRisks)
	}
	if len(o.Watchlist) > 10 {
		return fmt.Errorf("watchlist 不超过 10 只")
	}
	for i, r := range o.Risks {
		if err := checkLen(r, 120, fmt.Sprintf("第 %d 条风险", i+1)); err != nil {
			return err
		}
	}
	for _, w := range o.Watchlist {
		if !known[w.Symbol] {
			return fmt.Errorf("watchlist 里的 %q 不在事实包里", w.Symbol)
		}
		if err := checkLen(w.Reason, 160, "watchlist.reason"); err != nil {
			return err
		}
		if err := checkEvidence(ids, w.Evidence, 1, "watchlist "+w.Symbol); err != nil {
			return err
		}
	}
	for _, p := range o.Positions {
		if !held[p.Symbol] {
			return fmt.Errorf("positions 里的 %q 不是持仓", p.Symbol)
		}
		if err := checkLen(p.View, 160, "positions.view"); err != nil {
			return err
		}
		if err := checkEvidence(ids, p.Evidence, 1, "positions "+p.Symbol); err != nil {
			return err
		}
	}
	return nil
}
