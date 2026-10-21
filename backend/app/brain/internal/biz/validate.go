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

var numberToken = regexp.MustCompile(`\d+(?:,\d{3})*(?:\.\d+)?%?`)

const relTolerance = 0.005

// numberSet 是本次输入里出现过的数字及其出处。输出正文里的数字按三档判定（P1-5，
// vibe-astock verifier 口径）：①事实包内有出处（sourced）②算式重算通过（recomputed）
// ③无出处（omitted，正文替换为「（略※）」）。本地判定只在事实包以外纯文本数字时发生。
type numberSet struct {
	vals    []float64
	srcs    []string       // 与 vals 同长：事实标签或含该数字的文本片段
	closure []closureEntry // 由事实原始值两两四则运算预生成（算式重算档）
}

// newNumberSet 收集输入文本里的数字并记下出处，给数值因子补上常见换算（×100、÷100、÷1万、÷1亿），
// 再用事实原始值两两运算生成算式重算档的校验池。
func newNumberSet(facts []Fact, texts ...string) numberSet {
	var s numberSet
	for _, t := range texts {
		for _, loc := range numberToken.FindAllStringIndex(t, -1) {
			if v, _, _, ok := parseNumber(t[loc[0]:loc[1]]); ok {
				s.vals = append(s.vals, v)
				s.srcs = append(s.srcs, excerpt(t, loc))
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

func parseNumber(tok string) (v float64, decimals int, pct bool, ok bool) {
	pct = strings.HasSuffix(tok, "%")
	tok = strings.ReplaceAll(strings.TrimSuffix(tok, "%"), ",", "")
	if i := strings.IndexByte(tok, '.'); i >= 0 {
		decimals = len(tok) - i - 1
	}
	v, err := strconv.ParseFloat(tok, 64)
	if err != nil {
		return 0, 0, false, false
	}
	return v, decimals, pct, true
}

// matchOf 按输出的小数位匹配数字，返回出处（事实标签或文本片段）。
// 百分比文本允许按 ×100 命中（事实常以系数存、正文以 % 写）。
func (s numberSet) matchOf(x float64, decimals int, pct bool) (string, bool) {
	for i, v := range s.vals {
		if matches(x, decimals, v) || (pct && matches(x, decimals, v*100)) {
			return s.srcs[i], true
		}
	}
	return "", false
}

func (s numberSet) has(x float64, decimals int, pct bool) bool {
	_, ok := s.matchOf(x, decimals, pct)
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

// check 返回正文里找不到出处的数字。
func (s numberSet) check(texts ...string) error {
	var bad []string
	for _, t := range texts {
		for _, m := range numberToken.FindAllString(t, -1) {
			v, dec, pct, ok := parseNumber(m)
			if !ok || s.has(v, dec, pct) {
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
