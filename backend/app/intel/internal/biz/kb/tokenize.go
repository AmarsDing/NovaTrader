package kb

import (
	"strings"
	"unicode"

	"server/pkg/pgext"
)

// 数据库没有中文分词，关键词在这里切：连续汉字取相邻两字，单字保留；英文数字整词小写。
// 文档和查询必须用同一套规则，否则 tsvector 与 tsquery 对不上。

func normalizeRune(r rune) rune {
	if r >= 0xFF01 && r <= 0xFF5E {
		r -= 0xFEE0
	}
	return unicode.ToLower(r)
}

func isHan(r rune) bool { return unicode.Is(unicode.Han, r) }

func isWord(r rune) bool {
	return !isHan(r) && (unicode.IsLetter(r) || unicode.IsDigit(r))
}

// Tokenize 按出现顺序返回词，允许重复。
func Tokenize(s string) []string {
	var (
		out  []string
		han  []rune
		word strings.Builder
	)
	flushHan := func() {
		switch len(han) {
		case 0:
		case 1:
			out = append(out, string(han))
		default:
			for i := 0; i+1 < len(han); i++ {
				out = append(out, string(han[i:i+2]))
			}
		}
		han = han[:0]
	}
	flushWord := func() {
		if word.Len() > 0 {
			out = append(out, word.String())
			word.Reset()
		}
	}
	for _, r := range s {
		r = normalizeRune(r)
		switch {
		case isHan(r):
			flushWord()
			han = append(han, r)
		case isWord(r):
			flushHan()
			word.WriteRune(r)
		default:
			flushHan()
			flushWord()
		}
	}
	flushHan()
	flushWord()
	return out
}

// Lexemes 把文本切词并记下位置，用于写 tsvector。
func Lexemes(s string) []pgext.Lexeme {
	index := map[string]int{}
	var out []pgext.Lexeme
	for i, w := range Tokenize(s) {
		j, ok := index[w]
		if !ok {
			j = len(out)
			index[w] = j
			out = append(out, pgext.Lexeme{Word: w})
		}
		out[j].Positions = append(out[j].Positions, i+1)
	}
	return out
}

// QueryTerms 切查询词并去重，保持首次出现顺序。
func QueryTerms(s string) []string {
	seen := map[string]bool{}
	var out []string
	for _, w := range Tokenize(s) {
		if !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	return out
}

// EstimateTokens 估算 bge 分词后的长度：汉字和标点各算 1，连续英文数字每 4 个字符算 1。
func EstimateTokens(s string) int {
	n, run := 0, 0
	flush := func() {
		if run > 0 {
			n += (run + 3) / 4
			run = 0
		}
	}
	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			flush()
		case r < 0x80 && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			run++
		default:
			flush()
			n++
		}
	}
	flush()
	return n
}
