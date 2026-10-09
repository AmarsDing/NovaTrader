package extract

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// maxPartBytes 限制 zip 内单个部件解压后的大小，防止压缩炸弹。
const maxPartBytes = 64 << 20

func openZip(data []byte, kind string) (*zip.Reader, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, unsupported("不是有效的 %s 文件", kind)
	}
	return zr, nil
}

func readPart(zr *zip.Reader, name string) ([]byte, bool, error) {
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, true, err
		}
		defer rc.Close()
		b, err := io.ReadAll(io.LimitReader(rc, maxPartBytes+1))
		if err != nil {
			return nil, true, err
		}
		if len(b) > maxPartBytes {
			return nil, true, unsupported("文件内容过大")
		}
		return b, true, nil
	}
	return nil, false, nil
}

func attr(se xml.StartElement, local string) string {
	for _, a := range se.Attr {
		if a.Name.Local == local {
			return a.Value
		}
	}
	return ""
}

var headingStyle = regexp.MustCompile(`(?i)^(?:heading|标题)\s*([1-6])$`)

// docxStyles 读 styles.xml，得到样式编号到标题级别（1–6）的映射。
func docxStyles(zr *zip.Reader) map[string]int {
	levels := map[string]int{}
	b, ok, err := readPart(zr, "word/styles.xml")
	if !ok || err != nil {
		return levels
	}
	dec := xml.NewDecoder(bytes.NewReader(b))
	var id string
	for {
		tok, err := dec.Token()
		if err != nil {
			return levels
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch se.Name.Local {
		case "style":
			id = attr(se, "styleId")
		case "name":
			if m := headingStyle.FindStringSubmatch(attr(se, "val")); m != nil && id != "" {
				levels[id], _ = strconv.Atoi(m[1])
			}
		case "outlineLvl":
			if n, err := strconv.Atoi(attr(se, "val")); err == nil && n < 6 && id != "" {
				if _, set := levels[id]; !set {
					levels[id] = n + 1
				}
			}
		}
	}
}

var headingStyleID = regexp.MustCompile(`(?i)^heading([1-6])$`)

// docx 读 word/document.xml。标题样式开新章节；表格每行合成一段，单元格用 | 分隔。
func docx(data []byte) ([]Section, error) {
	zr, err := openZip(data, "docx")
	if err != nil {
		return nil, err
	}
	body, ok, err := readPart(zr, "word/document.xml")
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, unsupported("docx 缺少 word/document.xml")
	}
	styles := docxStyles(zr)

	var (
		out      []Section
		path     [6]string
		current  string
		paras    []string
		text     strings.Builder
		level    int
		inText   bool
		rowDepth int
		cells    []string
		cell     strings.Builder
	)
	flush := func() {
		if len(paras) > 0 {
			out = append(out, Section{Heading: current, Paragraphs: paras})
			paras = nil
		}
	}
	dec := xml.NewDecoder(bytes.NewReader(body))
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, unsupported("docx 内容损坏")
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "p":
				text.Reset()
				level = 0
			case "pStyle":
				id := attr(t, "val")
				if n, ok := styles[id]; ok {
					level = n
				} else if m := headingStyleID.FindStringSubmatch(id); m != nil {
					level, _ = strconv.Atoi(m[1])
				}
			case "outlineLvl":
				if n, err := strconv.Atoi(attr(t, "val")); err == nil && n < 6 {
					level = n + 1
				}
			case "t":
				inText = true
			case "tab":
				text.WriteByte('\t')
			case "br", "cr":
				text.WriteByte('\n')
			case "tr":
				rowDepth++
				cells = nil
			case "tc":
				cell.Reset()
			}
		case xml.CharData:
			if inText {
				text.Write(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inText = false
			case "p":
				s := strings.TrimSpace(text.String())
				switch {
				case s == "":
				case rowDepth > 0:
					if cell.Len() > 0 {
						cell.WriteByte(' ')
					}
					cell.WriteString(s)
				case level > 0:
					flush()
					path[level-1] = collapseSpace(s)
					for i := level; i < len(path); i++ {
						path[i] = ""
					}
					current = joinPath(path[:])
				default:
					paras = append(paras, s)
				}
			case "tc":
				if rowDepth > 0 {
					cells = append(cells, strings.TrimSpace(cell.String()))
				}
			case "tr":
				rowDepth--
				if row := joinNonEmpty(cells, " | "); row != "" {
					paras = append(paras, row)
				}
				cells = nil
			}
		}
	}
	flush()
	return out, nil
}

func joinPath(parts []string) string {
	return joinNonEmpty(parts, " > ")
}

func joinNonEmpty(parts []string, sep string) string {
	var keep []string
	for _, p := range parts {
		if p != "" {
			keep = append(keep, p)
		}
	}
	return strings.Join(keep, sep)
}

// xlsx 每个工作表一个章节。首个非空行当表头，其余每行写成「列名：值；列名：值」。
func xlsx(data []byte) ([]Section, error) {
	zr, err := openZip(data, "xlsx")
	if err != nil {
		return nil, err
	}
	shared, err := xlsxSharedStrings(zr)
	if err != nil {
		return nil, err
	}
	sheets, err := xlsxSheets(zr)
	if err != nil {
		return nil, err
	}
	var out []Section
	for _, sh := range sheets {
		b, ok, err := readPart(zr, sh.part)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		rows, err := xlsxRows(b, shared)
		if err != nil {
			return nil, err
		}
		out = append(out, Section{Heading: sh.name, Paragraphs: rowsToParagraphs(rows)})
	}
	return out, nil
}

func xlsxSharedStrings(zr *zip.Reader) ([]string, error) {
	b, ok, err := readPart(zr, "xl/sharedStrings.xml")
	if err != nil || !ok {
		return nil, err
	}
	var (
		out    []string
		cur    strings.Builder
		inT    bool
		inPhon bool
	)
	dec := xml.NewDecoder(bytes.NewReader(b))
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, unsupported("xlsx 共享字符串损坏")
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "si":
				cur.Reset()
			case "t":
				inT = true
			case "rPh":
				inPhon = true
			}
		case xml.CharData:
			if inT && !inPhon {
				cur.Write(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inT = false
			case "rPh":
				inPhon = false
			case "si":
				out = append(out, cur.String())
			}
		}
	}
}

type sheetRef struct {
	name string
	part string
}

func xlsxSheets(zr *zip.Reader) ([]sheetRef, error) {
	wb, ok, err := readPart(zr, "xl/workbook.xml")
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, unsupported("xlsx 缺少 xl/workbook.xml")
	}
	rels := map[string]string{}
	if rb, ok, err := readPart(zr, "xl/_rels/workbook.xml.rels"); err == nil && ok {
		dec := xml.NewDecoder(bytes.NewReader(rb))
		for {
			tok, err := dec.Token()
			if err != nil {
				break
			}
			if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "Relationship" {
				rels[attr(se, "Id")] = attr(se, "Target")
			}
		}
	}
	var out []sheetRef
	dec := xml.NewDecoder(bytes.NewReader(wb))
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "sheet" {
			continue
		}
		var rid string
		for _, a := range se.Attr {
			if a.Name.Local == "id" && a.Name.Space != "" {
				rid = a.Value
			}
		}
		target := rels[rid]
		if target == "" {
			target = fmt.Sprintf("worksheets/sheet%d.xml", len(out)+1)
		}
		part := strings.TrimPrefix(target, "/")
		if !strings.HasPrefix(target, "/") {
			part = path.Join("xl", target)
		}
		out = append(out, sheetRef{name: attr(se, "name"), part: part})
	}
	return out, nil
}

// xlsxRows 返回每行按列号排好的单元格文字。
func xlsxRows(b []byte, shared []string) ([][]string, error) {
	var (
		rows   [][]string
		row    []string
		col    int
		typ    string
		val    strings.Builder
		inVal  bool
		inCell bool
	)
	dec := xml.NewDecoder(bytes.NewReader(b))
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return rows, nil
		}
		if err != nil {
			return nil, unsupported("xlsx 工作表损坏")
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "row":
				row = nil
				col = 0
			case "c":
				inCell = true
				typ = attr(t, "t")
				val.Reset()
				if n := columnIndex(attr(t, "r")); n >= 0 {
					col = n
				}
			case "v", "t":
				if inCell {
					inVal = true
				}
			}
		case xml.CharData:
			if inVal {
				val.Write(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "v", "t":
				inVal = false
			case "c":
				inCell = false
				s := cellText(typ, val.String(), shared)
				for len(row) <= col {
					row = append(row, "")
				}
				row[col] = s
				col++
			case "row":
				rows = append(rows, row)
			}
		}
	}
}

func cellText(typ, raw string, shared []string) string {
	raw = strings.TrimSpace(raw)
	switch typ {
	case "s":
		i, err := strconv.Atoi(raw)
		if err != nil || i < 0 || i >= len(shared) {
			return ""
		}
		return strings.TrimSpace(shared[i])
	case "b":
		if raw == "1" {
			return "TRUE"
		}
		return "FALSE"
	case "inlineStr", "str", "e":
		return raw
	}
	if f, err := strconv.ParseFloat(raw, 64); err == nil {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return raw
}

// columnIndex 把 "B12" 转成 1。没有列字母时返回 -1。
func columnIndex(ref string) int {
	n := 0
	seen := false
	for _, r := range ref {
		if r >= 'A' && r <= 'Z' {
			n = n*26 + int(r-'A'+1)
			seen = true
			continue
		}
		break
	}
	if !seen {
		return -1
	}
	return n - 1
}

func rowsToParagraphs(rows [][]string) []string {
	var header []string
	var out []string
	for _, r := range rows {
		if joinNonEmpty(r, "") == "" {
			continue
		}
		if header == nil {
			header = r
			continue
		}
		var parts []string
		for i, v := range r {
			if v == "" {
				continue
			}
			name := ""
			if i < len(header) {
				name = header[i]
			}
			if name == "" {
				name = columnName(i)
			}
			parts = append(parts, name+"："+v)
		}
		if len(parts) > 0 {
			out = append(out, strings.Join(parts, "；"))
		}
	}
	if len(out) == 0 && header != nil {
		out = append(out, joinNonEmpty(header, " | "))
	}
	return out
}

func columnName(i int) string {
	s := ""
	for i++; i > 0; i = (i - 1) / 26 {
		s = string(rune('A'+(i-1)%26)) + s
	}
	return s
}
