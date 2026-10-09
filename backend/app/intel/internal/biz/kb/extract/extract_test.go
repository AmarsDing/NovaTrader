package extract

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func TestFormat(t *testing.T) {
	cases := map[string]string{
		"a.MD": "md", "b.txt": "txt", "c.docx": "docx", "d.xlsx": "xlsx",
		"e.pdf": "pdf", "f.doc": "doc", "g.xls": "xls", "h.exe": "", "noext": "",
	}
	for name, want := range cases {
		if got := Format(name); got != want {
			t.Errorf("Format(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestMarkdownHeadings(t *testing.T) {
	src := "前言段落\n\n# 第一章\n\n正文一\n续行\n\n## 1.1 入场\n\n```\n# 不是标题\n```\n\n# 第二章\n\n正文二\n"
	got, err := Extract("md", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	want := []Section{
		{Heading: "", Paragraphs: []string{"前言段落"}},
		{Heading: "第一章", Paragraphs: []string{"正文一\n续行"}},
		{Heading: "第一章 > 1.1 入场", Paragraphs: []string{"```\n# 不是标题\n```"}},
		{Heading: "第二章", Paragraphs: []string{"正文二"}},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %q", got)
	}
}

func TestPlainTextGBK(t *testing.T) {
	gbk, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte("止损设在开盘价下方。\n\n第二段"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := Extract("txt", gbk)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0].Paragraphs) != 2 || got[0].Paragraphs[0] != "止损设在开盘价下方。" {
		t.Fatalf("got %q", got)
	}
}

func TestRejectsLegacyAndEmpty(t *testing.T) {
	if _, err := Extract("doc", []byte("x")); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("doc: %v", err)
	}
	if _, err := Extract("md", []byte("  \n\n ---- \n")); !errors.Is(err, ErrNoText) {
		t.Fatalf("empty md: %v", err)
	}
	if _, err := Extract("docx", []byte("not a zip")); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("bad docx: %v", err)
	}
}

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const wns = `xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"`

func TestDocx(t *testing.T) {
	styles := `<w:styles ` + wns + `><w:style w:styleId="1"><w:name w:val="heading 1"/></w:style></w:styles>`
	doc := `<w:document ` + wns + `><w:body>
<w:p><w:r><w:t>开头说明</w:t></w:r></w:p>
<w:p><w:pPr><w:pStyle w:val="1"/></w:pPr><w:r><w:t>风控规则</w:t></w:r></w:p>
<w:p><w:r><w:t xml:space="preserve">单票仓位</w:t></w:r><w:r><w:t>不超过三成</w:t></w:r></w:p>
<w:tbl><w:tr><w:tc><w:p><w:r><w:t>指标</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>阈值</w:t></w:r></w:p></w:tc></w:tr>
<w:tr><w:tc><w:p><w:r><w:t>回撤</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>8%</w:t></w:r></w:p></w:tc></w:tr></w:tbl>
<w:p><w:pPr><w:pStyle w:val="Heading2"/></w:pPr><w:r><w:t>熔断</w:t></w:r></w:p>
<w:p><w:r><w:t>连亏三笔停止开仓</w:t></w:r></w:p>
</w:body></w:document>`
	got, err := Extract("docx", zipOf(t, map[string]string{"word/document.xml": doc, "word/styles.xml": styles}))
	if err != nil {
		t.Fatal(err)
	}
	want := []Section{
		{Heading: "", Paragraphs: []string{"开头说明"}},
		{Heading: "风控规则", Paragraphs: []string{"单票仓位不超过三成", "指标 | 阈值", "回撤 | 8%"}},
		{Heading: "风控规则 > 熔断", Paragraphs: []string{"连亏三笔停止开仓"}},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %q", got)
	}
}

func TestXlsx(t *testing.T) {
	files := map[string]string{
		"xl/workbook.xml": `<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="复盘" sheetId="1" r:id="rId1"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Target="worksheets/sheet1.xml"/></Relationships>`,
		"xl/sharedStrings.xml":       `<sst><si><t>股票</t></si><si><t>收益</t></si><si><r><t>贵州</t></r><r><t>茅台</t></r></si></sst>`,
		"xl/worksheets/sheet1.xml": `<worksheet><sheetData>
<row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c></row>
<row r="2"><c r="A2" t="s"><v>2</v></c><c r="B2"><v>3.2000000000000002</v></c><c r="D2" t="inlineStr"><is><t>备注</t></is></c></row>
<row r="3"></row>
</sheetData></worksheet>`,
	}
	got, err := Extract("xlsx", zipOf(t, files))
	if err != nil {
		t.Fatal(err)
	}
	want := []Section{{Heading: "复盘", Paragraphs: []string{"股票：贵州茅台；收益：3.2；D：备注"}}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %q", got)
	}
}

// minimalPDF 拼一个带文字层的单页 PDF，交叉引用表按实际偏移生成。
func minimalPDF(text string) []byte {
	content := fmt.Sprintf("BT /F1 12 Tf 72 720 Td (%s) Tj ET", text)
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	var b strings.Builder
	b.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	return []byte(b.String())
}

func TestPDF(t *testing.T) {
	got, err := Extract("pdf", minimalPDF("Stop loss below the opening price by one ATR"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Heading != "第 1 页" || !strings.Contains(strings.Join(got[0].Paragraphs, " "), "opening price") {
		t.Fatalf("got %q", got)
	}
	if _, err := Extract("pdf", minimalPDF("")); !errors.Is(err, ErrNoText) {
		t.Fatalf("textless pdf: %v", err)
	}
	if _, err := Extract("pdf", []byte("%PDF-1.4 garbage")); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("broken pdf: %v", err)
	}
}
