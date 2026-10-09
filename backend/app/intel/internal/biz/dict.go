package biz

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"server/pkg/symbol"
)

// 关联方式与基础置信度，见设计文档第 5 节。
const (
	MethodSource  = "source"
	MethodCode    = "code"
	MethodName    = "name"
	MethodAlias   = "alias"
	MethodConcept = "concept"

	confSource    = 1.00
	confCode      = 0.90
	confName      = 0.90
	confShortName = 0.50
	confConcept   = 0.80
	bonusTitle    = 0.05
	bonusRepeat   = 0.03
)

// StockEntry 来自 stock_basic。
type StockEntry struct {
	Code     string // 600519.SH
	Name     string
	Concepts []string
}

// AliasEntry 来自 stock_alias。
type AliasEntry struct {
	Alias      string
	Code       string
	Confidence float64
}

type term struct {
	targetType string
	target     string
	method     string
	confidence float64
}

// Dictionary 是关联用的只读词典。重建后整体替换，不在原地修改。
type Dictionary struct {
	terms    map[string][]term
	first    map[rune]bool
	maxLen   int
	codes    map[string][]string // 6 位代码 → 内部代码
	Stocks   int
	Aliases  int
	Concepts int
}

// BuildDictionary 合并 stock_basic 与 stock_alias。同一个词对应多只股票时，置信度按股票数均分。
func BuildDictionary(stocks []StockEntry, aliases []AliasEntry) *Dictionary {
	d := &Dictionary{
		terms: map[string][]term{},
		first: map[rune]bool{},
		codes: map[string][]string{},
	}
	add := func(text string, t term) {
		text = strings.TrimSpace(foldWidth(text))
		n := utf8.RuneCountInString(text)
		if n < 2 {
			return
		}
		for _, old := range d.terms[text] {
			if old.targetType == t.targetType && old.target == t.target {
				return
			}
		}
		d.terms[text] = append(d.terms[text], t)
		r, _ := utf8.DecodeRuneInString(text)
		d.first[r] = true
		if n > d.maxLen {
			d.maxLen = n
		}
	}
	concepts := map[string]bool{}
	for _, s := range stocks {
		sym, err := symbol.Parse(s.Code)
		if err != nil {
			continue
		}
		code := sym.Tongdaxin()
		d.Stocks++
		d.codes[sym.Code] = appendUnique(d.codes[sym.Code], code)
		for _, name := range stockNames(s.Name) {
			conf := confName
			if utf8.RuneCountInString(name) < 3 {
				conf = confShortName
			}
			add(name, term{TargetStock, code, MethodName, conf})
		}
		for _, c := range s.Concepts {
			c = strings.TrimSpace(c)
			if c == "" {
				continue
			}
			concepts[c] = true
			add(c, term{TargetConcept, c, MethodConcept, confConcept})
		}
	}
	for _, a := range aliases {
		sym, err := symbol.Parse(a.Code)
		if err != nil {
			continue
		}
		conf := a.Confidence
		if conf <= 0 || conf > 1 {
			conf = 0.8
		}
		d.Aliases++
		add(a.Alias, term{TargetStock, sym.Tongdaxin(), MethodAlias, conf})
	}
	d.Concepts = len(concepts)
	for text, ts := range d.terms {
		stocksForText := 0
		for _, t := range ts {
			if t.targetType == TargetStock {
				stocksForText++
			}
		}
		if stocksForText > 1 {
			for i := range ts {
				if ts[i].targetType == TargetStock {
					ts[i].confidence /= float64(stocksForText)
				}
			}
			d.terms[text] = ts
		}
	}
	return d
}

// stockNames 返回简称本身和去掉 ST 前缀、空格后的写法。
func stockNames(name string) []string {
	name = strings.TrimSpace(foldWidth(name))
	if name == "" {
		return nil
	}
	out := []string{name}
	bare := strings.ReplaceAll(name, " ", "")
	for _, p := range []string{"*ST", "S*ST", "SST", "ST"} {
		if strings.HasPrefix(strings.ToUpper(bare), p) {
			bare = bare[len(p):]
			break
		}
	}
	if bare != "" && bare != name {
		out = append(out, bare)
	}
	return out
}

func appendUnique(list []string, s string) []string {
	for _, x := range list {
		if x == s {
			return list
		}
	}
	return append(list, s)
}

// mention 是正文中一次命中。pos 为字符下标。
type mention struct {
	term
	text string
	pos  int
}

// match 用最左最长、不重叠的方式扫描。
func (d *Dictionary) match(s string) []mention {
	if d == nil || len(d.terms) == 0 {
		return nil
	}
	rs := []rune(s)
	var out []mention
	for i := 0; i < len(rs); {
		if !d.first[rs[i]] {
			i++
			continue
		}
		hit := 0
		for l := min(d.maxLen, len(rs)-i); l >= 2; l-- {
			key := string(rs[i : i+l])
			if ts, ok := d.terms[key]; ok {
				for _, t := range ts {
					out = append(out, mention{term: t, text: key, pos: i})
				}
				hit = l
				break
			}
		}
		if hit > 0 {
			i += hit
		} else {
			i++
		}
	}
	return out
}

var sixDigits = regexp.MustCompile(`\d{6}`)

// matchCodes 找独立出现的 6 位已知代码。前后是数字或小数点、后面跟「股」「元」「万」「亿」「%」的不算。
func (d *Dictionary) matchCodes(s string) []mention {
	if d == nil || len(d.codes) == 0 {
		return nil
	}
	var out []mention
	for _, loc := range sixDigits.FindAllStringIndex(s, -1) {
		if loc[0] > 0 {
			prev, _ := utf8.DecodeLastRuneInString(s[:loc[0]])
			if prev >= '0' && prev <= '9' || prev == '.' {
				continue
			}
		}
		if loc[1] < len(s) {
			next, _ := utf8.DecodeRuneInString(s[loc[1]:])
			if next >= '0' && next <= '9' || strings.ContainsRune("股元万亿%", next) {
				continue
			}
		}
		code := s[loc[0]:loc[1]]
		targets := d.codes[code]
		if len(targets) == 0 {
			continue
		}
		pos := utf8.RuneCountInString(s[:loc[0]])
		for _, t := range targets {
			out = append(out, mention{
				term: term{TargetStock, t, MethodCode, confCode / float64(len(targets))},
				text: code,
				pos:  pos,
			})
		}
	}
	return out
}

// LinkItem 关联股票和概念。title 中的命中加分，多次命中加分，低于 threshold 的丢弃。
func (d *Dictionary) LinkItem(it *Item, threshold float64) []Link {
	type agg struct {
		Link
		count   int
		inTitle bool
	}
	byKey := map[string]*agg{}
	var order []string
	put := func(targetType, target, method, matched string, conf float64, inTitle bool) {
		key := targetType + "|" + target
		a, ok := byKey[key]
		if !ok {
			a = &agg{Link: Link{TargetType: targetType, Target: target, Confidence: conf, Method: method, Matched: matched}}
			byKey[key] = a
			order = append(order, key)
		} else if conf > a.Confidence {
			a.Confidence, a.Method, a.Matched = conf, method, matched
		}
		a.count++
		a.inTitle = a.inTitle || inTitle
	}
	for _, c := range it.Codes {
		put(TargetStock, c, MethodSource, c, confSource, true)
	}
	text := it.Title + "\n" + it.Content
	titleLen := utf8.RuneCountInString(it.Title)
	for _, m := range append(d.match(text), d.matchCodes(text)...) {
		put(m.targetType, m.target, m.method, m.text, m.confidence, m.pos < titleLen)
	}
	out := make([]Link, 0, len(order))
	for _, key := range order {
		a := byKey[key]
		conf := a.Confidence
		if a.inTitle {
			conf += bonusTitle
		}
		conf += bonusRepeat * float64(a.count-1)
		if conf > 1 {
			conf = 1
		}
		if conf < threshold {
			continue
		}
		a.Link.Confidence = round4(conf)
		out = append(out, a.Link)
	}
	return out
}
