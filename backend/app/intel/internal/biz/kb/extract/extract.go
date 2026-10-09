// Package extract 从上传文件里抽出带标题的段落。只读文字层，不做 OCR。
package extract

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// Section 是一个章节：标题路径和其下的段落。
type Section struct {
	Heading    string
	Paragraphs []string
}

// ErrUnsupported 表示格式不收。错误信息里带给用户看的原因。
var ErrUnsupported = errors.New("extract: unsupported")

// ErrNoText 表示文件里没有可用文字，例如扫描版 PDF。
var ErrNoText = errors.New("extract: no text")

func unsupported(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrUnsupported, fmt.Sprintf(format, args...))
}

// Format 由文件名后缀得到格式名。不认识的返回空串。
func Format(fileName string) string {
	switch strings.ToLower(filepath.Ext(fileName)) {
	case ".md", ".markdown":
		return "md"
	case ".txt", ".text":
		return "txt"
	case ".docx":
		return "docx"
	case ".xlsx":
		return "xlsx"
	case ".pdf":
		return "pdf"
	case ".doc":
		return "doc"
	case ".xls":
		return "xls"
	}
	return ""
}

// Extract 按格式抽文字。返回的章节已去掉空段落，至少有一段。
func Extract(format string, data []byte) ([]Section, error) {
	var (
		sections []Section
		err      error
	)
	switch format {
	case "md":
		sections, err = markdown(data)
	case "txt":
		sections, err = plain(data)
	case "docx":
		sections, err = docx(data)
	case "xlsx":
		sections, err = xlsx(data)
	case "pdf":
		sections, err = pdfText(data)
	case "doc", "xls":
		return nil, unsupported("旧版 Office 格式，请另存为 docx / xlsx")
	default:
		return nil, unsupported("不支持的格式 %q，只收 md、txt、docx、xlsx、pdf", format)
	}
	if err != nil {
		return nil, err
	}
	sections = clean(sections)
	if len(sections) == 0 {
		return nil, fmt.Errorf("%w: 文件里没有可用文字", ErrNoText)
	}
	return sections, nil
}

// decodeText 把文本文件转成 UTF-8。非 UTF-8 时按 GB18030 解码（Windows 下中文 txt 常见）。
func decodeText(data []byte) (string, error) {
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	if utf8.Valid(data) {
		return string(data), nil
	}
	out, err := simplifiedchinese.GB18030.NewDecoder().Bytes(data)
	if err != nil || !utf8.Valid(out) {
		return "", unsupported("文本编码无法识别，请转成 UTF-8")
	}
	return string(out), nil
}

const maxHeadingRunes = 120

func clean(in []Section) []Section {
	out := make([]Section, 0, len(in))
	for _, s := range in {
		heading := truncateRunes(collapseSpace(s.Heading), maxHeadingRunes)
		var paras []string
		for _, p := range s.Paragraphs {
			p = strings.TrimSpace(strings.ReplaceAll(p, "\r\n", "\n"))
			p = strings.ReplaceAll(p, "\x00", "")
			if hasContent(p) {
				paras = append(paras, p)
			}
		}
		if len(paras) == 0 {
			continue
		}
		out = append(out, Section{Heading: heading, Paragraphs: paras})
	}
	return out
}

func hasContent(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

func collapseSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}

// splitParagraphs 按空行切段。
func splitParagraphs(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	var (
		out []string
		cur []string
	)
	flush := func() {
		if len(cur) > 0 {
			out = append(out, strings.Join(cur, "\n"))
			cur = cur[:0]
		}
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		cur = append(cur, strings.TrimRight(line, " \t"))
	}
	flush()
	return out
}
