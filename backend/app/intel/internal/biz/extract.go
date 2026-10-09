package biz

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	amountPattern = regexp.MustCompile(`(\d{1,3}(?:,\d{3})+(?:\.\d+)?|\d+(?:\.\d+)?)\s*(亿元|万元|元)`)
	ratioPattern  = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*%`)
	datePatterns  = []*regexp.Regexp{
		regexp.MustCompile(`(\d{4})\s*年\s*(\d{1,2})\s*月\s*(\d{1,2})\s*日`),
		regexp.MustCompile(`(\d{4})-(\d{1,2})-(\d{1,2})`),
	}
)

const maxFacts = 50

var amountScale = map[string]float64{"元": 1, "万元": 1e4, "亿元": 1e8}

// ExtractFacts 抽出金额（换算成元）、比例（百分数）和日期。Start、End 是 content 的字符下标。
func ExtractFacts(content string) []Fact {
	var out []Fact
	runeAt := runeIndexer(content)
	for _, m := range amountPattern.FindAllStringSubmatchIndex(content, -1) {
		num := strings.ReplaceAll(content[m[2]:m[3]], ",", "")
		v, err := strconv.ParseFloat(num, 64)
		if err != nil {
			continue
		}
		v *= amountScale[content[m[4]:m[5]]]
		out = append(out, Fact{Type: "amount", Value: &v, Unit: "元", Text: content[m[0]:m[1]], Start: runeAt(m[0]), End: runeAt(m[1])})
	}
	for _, m := range ratioPattern.FindAllStringSubmatchIndex(content, -1) {
		v, err := strconv.ParseFloat(content[m[2]:m[3]], 64)
		if err != nil {
			continue
		}
		out = append(out, Fact{Type: "ratio", Value: &v, Unit: "%", Text: content[m[0]:m[1]], Start: runeAt(m[0]), End: runeAt(m[1])})
	}
	for _, p := range datePatterns {
		for _, m := range p.FindAllStringSubmatchIndex(content, -1) {
			y, _ := strconv.Atoi(content[m[2]:m[3]])
			mo, _ := strconv.Atoi(content[m[4]:m[5]])
			d, _ := strconv.Atoi(content[m[6]:m[7]])
			t := time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC)
			if y < 1990 || y > 2100 || t.Month() != time.Month(mo) || t.Day() != d {
				continue
			}
			out = append(out, Fact{Type: "date", Text: fmt.Sprintf("%04d-%02d-%02d", y, mo, d), Start: runeAt(m[0]), End: runeAt(m[1])})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	if len(out) > maxFacts {
		out = out[:maxFacts]
	}
	return out
}

// runeIndexer 把字节下标换成字符下标。下标单调递增时按增量计算。
func runeIndexer(s string) func(byteIdx int) int {
	lastByte, lastRune := 0, 0
	return func(b int) int {
		if b < lastByte {
			lastByte, lastRune = 0, 0
		}
		lastRune += utf8.RuneCountInString(s[lastByte:b])
		lastByte = b
		return lastRune
	}
}
