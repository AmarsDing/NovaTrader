package biz

import (
	"crypto/sha256"
	"encoding/hex"
	"html"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	htmlTag    = regexp.MustCompile(`(?s)<[^>]*>`)
	spaceRun   = regexp.MustCompile(`[ \t\f\v\x{00A0}\x{3000}]+`)
	blankLines = regexp.MustCompile(`\s*\n\s*`)
	// 结尾的来源、编辑署名，不同转载方各不相同，留着会让精确去重失效。
	tailNote = regexp.MustCompile(`(?:[（(]\s*(?:来源|责任编辑|编辑|原标题|文章来源)\s*[:：][^）)]*[）)]\s*|(?:来源|责任编辑|文章来源)\s*[:：][^\n]{0,40})$`)
)

// Normalize 去 HTML、全角转半角、合并空白、去结尾署名。
func Normalize(s string) string {
	s = htmlTag.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = foldWidth(s)
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = spaceRun.ReplaceAllString(s, " ")
	s = blankLines.ReplaceAllString(s, "\n")
	s = strings.TrimSpace(s)
	for i := 0; i < 3; i++ {
		trimmed := strings.TrimSpace(tailNote.ReplaceAllString(s, ""))
		if trimmed == s {
			break
		}
		s = trimmed
	}
	return s
}

// foldWidth 把全角 ASCII 和全角空格换成半角。中文标点保持原样。
func foldWidth(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == 0x3000:
			return ' '
		case r >= 0xFF01 && r <= 0xFF5E:
			if isCJKPunct(r) {
				return r
			}
			return r - 0xFEE0
		}
		return r
	}, s)
}

// 全角括号、冒号、逗号在中文里是正常标点，保留，便于按中文标点切分。
func isCJKPunct(r rune) bool {
	switch r {
	case '（', '）', '：', '，', '；', '！', '？':
		return true
	}
	return false
}

// ContentHash 是精确去重用的指纹。
func ContentHash(title, content string) string {
	sum := sha256.Sum256([]byte(title + "\n" + content))
	return hex.EncodeToString(sum[:])
}

// textRunes 只留字母和数字，供 SimHash 和长度判断使用。
func textRunes(s string) []rune {
	out := make([]rune, 0, utf8.RuneCountInString(s))
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out = append(out, unicode.ToLower(r))
		}
	}
	return out
}
