package extract

import (
	"bytes"
	"fmt"
	"strings"
	"unicode"

	"github.com/ledongthuc/pdf"
)

// minUsefulRunes 低于这个数说明没有文字层，按扫描件拒收。
const minUsefulRunes = 20

// pdfText 每页一个章节，每行一段。解析库遇到损坏文件可能 panic，这里转成错误。
func pdfText(data []byte) (sections []Section, err error) {
	defer func() {
		if r := recover(); r != nil {
			sections, err = nil, unsupported("PDF 无法解析")
		}
	}()
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, unsupported("PDF 无法解析")
	}
	var useful, garbled int
	for i := 1; i <= r.NumPage(); i++ {
		p := r.Page(i)
		if p.V.IsNull() {
			continue
		}
		text, err := p.GetPlainText(nil)
		if err != nil {
			continue
		}
		var paras []string
		for _, line := range strings.Split(text, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				paras = append(paras, line)
			}
		}
		for _, ch := range text {
			switch {
			case ch == unicode.ReplacementChar || unicode.Is(unicode.Co, ch):
				garbled++
			case unicode.IsLetter(ch) || unicode.IsDigit(ch):
				useful++
			}
		}
		sections = append(sections, Section{Heading: fmt.Sprintf("第 %d 页", i), Paragraphs: paras})
	}
	if useful < minUsefulRunes {
		return nil, fmt.Errorf("%w: PDF 没有文字层（可能是扫描件），不做 OCR", ErrNoText)
	}
	if garbled*3 > useful {
		return nil, fmt.Errorf("%w: PDF 文字编码无法还原", ErrNoText)
	}
	return sections, nil
}
