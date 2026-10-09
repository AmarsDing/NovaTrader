package kb

import (
	"strings"
	"unicode"

	"server/app/intel/internal/biz/kb/extract"
)

// ChunkOptions 是分块长度，单位为估算 token。bge-large-zh-v1.5 最长 512，硬上限要留余量。
type ChunkOptions struct {
	Target  int
	Max     int
	Min     int
	Overlap int
}

// DefaultChunkOptions 对齐 M04 设计第 3 节。
var DefaultChunkOptions = ChunkOptions{Target: 480, Max: 500, Min: 200, Overlap: 60}

// Chunk 是一个分块。Content 不含标题，嵌入和关键词都用 EmbedText。
type Chunk struct {
	Seq     int
	Heading string
	Content string
	Tokens  int
}

// EmbedText 是送去嵌入和分词的文本：标题一行加正文。
func (c Chunk) EmbedText() string {
	if c.Heading == "" {
		return c.Content
	}
	return c.Heading + "\n" + c.Content
}

type piece struct {
	text   string
	tokens int
	// cont 为真表示与上一片属于同一段，拼接时不加换行。
	cont bool
}

type draft struct {
	pieces []piece
	// overlap 是开头从上一块借来的片数，合并回上一块时要去掉。
	overlap int
}

func (d draft) tokens() int {
	n := 0
	for _, p := range d.pieces {
		n += p.tokens
	}
	return n
}

func (d draft) text() string {
	var b strings.Builder
	for i, p := range d.pieces {
		if i > 0 && !p.cont {
			b.WriteByte('\n')
		}
		b.WriteString(p.text)
	}
	return b.String()
}

// SplitChunks 把章节切成块。同一章节内按段落累积，不跨章节合并。
func SplitChunks(sections []extract.Section, opt ChunkOptions) []Chunk {
	if opt.Max <= 0 {
		opt = DefaultChunkOptions
	}
	var out []Chunk
	for _, sec := range sections {
		headCost := 0
		if sec.Heading != "" {
			headCost = EstimateTokens(sec.Heading) + 1
		}
		budget := max(opt.Target-headCost, 100)
		hard := max(opt.Max-headCost, budget)
		for _, d := range splitSection(sec.Paragraphs, budget, hard, opt) {
			text := strings.TrimSpace(d.text())
			if text == "" {
				continue
			}
			out = append(out, Chunk{
				Seq:     len(out),
				Heading: sec.Heading,
				Content: text,
				Tokens:  headCost + d.tokens(),
			})
		}
	}
	return out
}

func splitSection(paragraphs []string, budget, hard int, opt ChunkOptions) []draft {
	var pieces []piece
	for _, p := range paragraphs {
		pieces = append(pieces, paragraphPieces(p, budget)...)
	}
	var (
		drafts []draft
		cur    draft
	)
	for _, p := range pieces {
		if len(cur.pieces) > cur.overlap && cur.tokens()+p.tokens > budget {
			drafts = append(drafts, cur)
			cur = draft{}
			if last := drafts[len(drafts)-1].pieces; len(last) > 1 {
				tail := last[len(last)-1]
				if tail.tokens <= opt.Overlap && tail.tokens+p.tokens <= hard {
					tail.cont = false
					cur.pieces = append(cur.pieces, tail)
					cur.overlap = 1
				}
			}
		}
		if len(cur.pieces) == 0 {
			p.cont = false
		}
		cur.pieces = append(cur.pieces, p)
	}
	if len(cur.pieces) > cur.overlap {
		drafts = append(drafts, cur)
	}
	if n := len(drafts); n >= 2 {
		last, prev := drafts[n-1], drafts[n-2]
		rest := last.pieces[last.overlap:]
		merged := draft{pieces: append(append([]piece{}, prev.pieces...), rest...), overlap: prev.overlap}
		if last.tokens() < opt.Min && merged.tokens() <= hard {
			drafts = append(drafts[:n-2], merged)
		}
	}
	return drafts
}

// paragraphPieces 段落不超预算时整段一片；否则按句切，单句仍超长就按长度硬切。
func paragraphPieces(p string, budget int) []piece {
	if t := EstimateTokens(p); t <= budget {
		return []piece{{text: p, tokens: t}}
	}
	var out []piece
	for _, s := range sentences(p) {
		if t := EstimateTokens(s); t <= budget {
			out = append(out, piece{text: s, tokens: t, cont: true})
			continue
		}
		for _, h := range hardSplit(s, budget) {
			out = append(out, piece{text: h, tokens: EstimateTokens(h), cont: true})
		}
	}
	if len(out) > 0 {
		out[0].cont = false
	}
	return out
}

func isSentenceEnd(r rune) bool {
	switch r {
	case '。', '！', '？', '；', '!', '?', ';', '\n':
		return true
	}
	return false
}

func sentences(p string) []string {
	var (
		out []string
		cur []rune
	)
	rs := []rune(p)
	for i, r := range rs {
		cur = append(cur, r)
		end := isSentenceEnd(r) || (r == '.' && i+1 < len(rs) && unicode.IsSpace(rs[i+1]))
		if end {
			if s := strings.TrimSpace(string(cur)); s != "" {
				out = append(out, s)
			}
			cur = cur[:0]
		}
	}
	if s := strings.TrimSpace(string(cur)); s != "" {
		out = append(out, s)
	}
	return out
}

// hardSplit 按估算长度切，汉字和标点各算 1，英文数字算 1/4。
func hardSplit(s string, budget int) []string {
	var (
		out  []string
		cur  []rune
		cost float64
	)
	for _, r := range s {
		c := 1.0
		switch {
		case unicode.IsSpace(r):
			c = 0
		case r < 0x80 && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			c = 0.25
		}
		if cost+c > float64(budget) && len(cur) > 0 {
			out = append(out, string(cur))
			cur, cost = cur[:0], 0
		}
		cur = append(cur, r)
		cost += c
	}
	if len(cur) > 0 {
		out = append(out, string(cur))
	}
	return out
}
