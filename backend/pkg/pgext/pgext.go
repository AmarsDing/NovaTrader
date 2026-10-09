// Package pgext 是 ent 用的 PostgreSQL 扩展列类型：pgvector 的 vector 和内置的 tsvector。
// 两者都按文本格式与数据库交换，不依赖驱动的二进制编码。
package pgext

import (
	"database/sql/driver"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Vector 对应 pgvector 的 vector 列，文本格式为 [1,2,3]。
type Vector []float32

func (v Vector) Value() (driver.Value, error) {
	if v == nil {
		return nil, nil
	}
	var b strings.Builder
	b.Grow(len(v) * 10)
	b.WriteByte('[')
	for i, x := range v {
		if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
			return nil, fmt.Errorf("pgext: vector element %d is not finite", i)
		}
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(x), 'g', -1, 32))
	}
	b.WriteByte(']')
	return b.String(), nil
}

func (v *Vector) Scan(src any) error {
	var s string
	switch x := src.(type) {
	case nil:
		*v = nil
		return nil
	case string:
		s = x
	case []byte:
		s = string(x)
	default:
		return fmt.Errorf("pgext: cannot scan %T into Vector", src)
	}
	s = strings.TrimSpace(s)
	if len(s) < 2 || s[0] != '[' || s[len(s)-1] != ']' {
		return fmt.Errorf("pgext: bad vector literal")
	}
	body := s[1 : len(s)-1]
	if body == "" {
		*v = Vector{}
		return nil
	}
	parts := strings.Split(body, ",")
	out := make(Vector, len(parts))
	for i, p := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 32)
		if err != nil {
			return fmt.Errorf("pgext: vector element %d: %w", i, err)
		}
		out[i] = float32(f)
	}
	*v = out
	return nil
}

// TSVector 对应 tsvector 列。写入时内容必须已是合法的 tsvector 文本，见 FormatTSVector。
type TSVector string

func (t TSVector) Value() (driver.Value, error) {
	return string(t), nil
}

func (t *TSVector) Scan(src any) error {
	switch x := src.(type) {
	case nil:
		*t = ""
	case string:
		*t = TSVector(x)
	case []byte:
		*t = TSVector(x)
	default:
		return fmt.Errorf("pgext: cannot scan %T into TSVector", src)
	}
	return nil
}

// Lexeme 是一个词和它出现的位置（从 1 开始）。
type Lexeme struct {
	Word      string
	Positions []int
}

// maxPosition 是 tsvector 允许的最大位置，超出的位置会被数据库截断。
const maxPosition = 16383

// FormatTSVector 把词表写成 tsvector 文本，例如 'a':1,3 'b':2。词按原样存，不再经过分词配置。
func FormatTSVector(lexemes []Lexeme) TSVector {
	var b strings.Builder
	for i, lx := range lexemes {
		if lx.Word == "" {
			continue
		}
		if i > 0 && b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(quoteLexeme(lx.Word))
		if len(lx.Positions) > 0 {
			b.WriteByte(':')
			for j, p := range lx.Positions {
				if p < 1 {
					p = 1
				}
				if p > maxPosition {
					p = maxPosition
				}
				if j > 0 {
					b.WriteByte(',')
				}
				b.WriteString(strconv.Itoa(p))
			}
		}
	}
	return TSVector(b.String())
}

// FormatTSQueryOr 把词用 | 连成 tsquery 文本。words 为空时返回空串。
func FormatTSQueryOr(words []string) string {
	parts := make([]string, 0, len(words))
	for _, w := range words {
		if w != "" {
			parts = append(parts, quoteLexeme(w))
		}
	}
	return strings.Join(parts, " | ")
}

func quoteLexeme(w string) string {
	w = strings.ReplaceAll(w, `\`, `\\`)
	w = strings.ReplaceAll(w, `'`, `''`)
	return "'" + w + "'"
}
