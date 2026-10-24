// Package decl 是 B4 声明式策略：用户策略以 JSON 表达式树描述，存
// strategy_library.code（status=active 的行被策略扫描装载）。解释器是
// 白名单求值器——内存执行、禁 SQL/反射/任意代码，算子集合封闭；解析期
// 拒绝未知字段、未知算子、非数字周期、深度/规模炸弹并给出路径定位。
// 数据纪律：取值缺失沿表达式一路显式传播（ok=false），绝不以 0 冒充；
// Filter 缺失视为不通过，Score 满足条件但取值缺失的加分项按 0 计
// （分数是评优序而非观测值），Entry 参考价取不到则不出计划。
package decl

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// FormatVersion 是 code 字段 JSON 的格式版本（「并版本化」合同）：
// 只接受 1，未知版本显式拒绝，不猜测语义。
const FormatVersion = 1

const (
	// MaxCodeSize 限制单个 code JSON 的体积，挡住体积炸弹与误粘贴的二进制。
	MaxCodeSize = 256 * 1024
	// maxDepth / maxNodes 限制表达式树形（深度/节点数），求值是递归的。
	maxDepth  = 24
	maxNodes  = 512
	maxParams = 32
	maxAdds   = 16
	maxHold   = 15
)

var nameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{1,31}$`)

// Definition 是一份声明式策略定义 v1。
//
//	{
//	  "version": 1,
//	  "name": "dsl_momo",
//	  "title": "动量示例",
//	  "hold_days": 3,
//	  "params": [{"name":"dd_min","default":0.05,"min":0,"max":0.2}],
//	  "filter": <bool-expr>,
//	  "score": {"base": 40, "adds": [{"when": <bool-expr>, "num": <num-expr>, "cap": 15}]},
//	  "entry": {"price": <num-expr>}   // 缺省 = 现价
//	}
type Definition struct {
	Version  int         `json:"version"`
	Name     string      `json:"name"`
	Title    string      `json:"title,omitempty"`
	HoldDays int         `json:"hold_days"`
	Params   []DeclParam `json:"params,omitempty"`
	Filter   *Expr       `json:"filter"`
	Score    ScoreBlock  `json:"score"`
	Entry    EntryBlock  `json:"entry,omitempty"`

	params map[string]DeclParam
}

type DeclParam struct {
	Name    string  `json:"name"`
	Default float64 `json:"default"`
	Min     float64 `json:"min"`
	Max     float64 `json:"max"`
}

type ScoreBlock struct {
	Base float64    `json:"base"`
	Adds []ScoreAdd `json:"adds,omitempty"`
}

type ScoreAdd struct {
	// When 缺失（nil）= 无条件加分。求值 missing 视为不满足（不惩罚不冒充）。
	When *Expr `json:"when,omitempty"`
	// Num 是加分值表达式；必填。求值 missing → 该加分项按 0 计（见包注释口径）。
	Num *Expr `json:"num"`
	// Cap 上限，0 = 不封顶。负值解析期拒绝。
	Cap float64 `json:"cap,omitempty"`
}

type EntryBlock struct {
	// Price 是参考价表达式；缺省 = 现价（field last）。
	Price *Expr `json:"price,omitempty"`
}

// Expr 是类型化表达式节点。Op 决定类型与操作数：
//
//	数值：num（字面量，Num 字段）/ field / param（Name 字段）
//	      add sub mul div min max clamp(x,lo,hi) abs
//	      ma_close ma_amount high_n low_n（args[0] 必须是 num 字面量周期 1..250）
//	布尔：bool（字面量，Bool 字段）/ and or not / gt gte lt lte eq neq
type Expr struct {
	Op   string   `json:"op"`
	Args []*Expr  `json:"args,omitempty"`
	Num  *float64 `json:"num,omitempty"`
	Bool *bool    `json:"bool,omitempty"`
	Name string   `json:"name,omitempty"`
}

// numFields 是 field 算子可取的白名单字段（今日/昨日截面）。
var numFields = map[string]bool{
	"last": true, "prev_close": true,
	"open": true, "high": true, "low": true, "close": true,
	"amount": true, "volume": true, "dev_ma10": true,
}

var errSyntax = func(path, format string, args ...any) error {
	return fmt.Errorf("decl: %s: %s", path, fmt.Sprintf(format, args...))
}

// Parse 解析并校验一份定义。所有失败都带路径（如 filter.args[1]），
// 供 strategy_library 行级错误提示与「绝不半激活」的装载裁决。
func Parse(data []byte) (*Definition, error) {
	if len(data) == 0 {
		return nil, errSyntax("$", "empty definition")
	}
	if len(data) > MaxCodeSize {
		return nil, errSyntax("$", "definition too large (%d > %d bytes)", len(data), MaxCodeSize)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var d Definition
	if err := dec.Decode(&d); err != nil {
		return nil, errSyntax("$", "invalid json: %v", err)
	}
	if d.Version != FormatVersion {
		return nil, errSyntax("version", "unsupported version %d (want %d)", d.Version, FormatVersion)
	}
	if !nameRe.MatchString(d.Name) {
		return nil, errSyntax("name", "must match %s (got %q)", nameRe.String(), d.Name)
	}
	if d.HoldDays < 1 || d.HoldDays > maxHold {
		return nil, errSyntax("hold_days", "must be in [1, %d] (got %d)", maxHold, d.HoldDays)
	}
	if len(d.Params) > maxParams {
		return nil, errSyntax("params", "too many (%d > %d)", len(d.Params), maxParams)
	}
	d.params = make(map[string]DeclParam, len(d.Params))
	for i, p := range d.Params {
		path := fmt.Sprintf("params[%d]", i)
		if !nameRe.MatchString(p.Name) {
			return nil, errSyntax(path+".name", "must match %s (got %q)", nameRe.String(), p.Name)
		}
		if _, dup := d.params[p.Name]; dup {
			return nil, errSyntax(path+".name", "duplicate param %q", p.Name)
		}
		if !(p.Min <= p.Max) {
			return nil, errSyntax(path, "min %v > max %v", p.Min, p.Max)
		}
		if p.Default < p.Min || p.Default > p.Max {
			return nil, errSyntax(path+".default", "%v outside [%v, %v]", p.Default, p.Min, p.Max)
		}
		d.params[p.Name] = p
	}
	if d.Filter == nil {
		return nil, errSyntax("filter", "required")
	}
	budget := &budget{nodes: maxNodes}
	if err := d.checkExpr(d.Filter, "filter", wantBool, 0, budget); err != nil {
		return nil, err
	}
	if len(d.Score.Adds) > maxAdds {
		return nil, errSyntax("score.adds", "too many (%d > %d)", len(d.Score.Adds), maxAdds)
	}
	for i, a := range d.Score.Adds {
		path := fmt.Sprintf("score.adds[%d]", i)
		if a.When != nil {
			if err := d.checkExpr(a.When, path+".when", wantBool, 0, budget); err != nil {
				return nil, err
			}
		}
		if a.Num == nil {
			return nil, errSyntax(path+".num", "required")
		}
		if err := d.checkExpr(a.Num, path+".num", wantNum, 0, budget); err != nil {
			return nil, err
		}
		if a.Cap < 0 {
			return nil, errSyntax(path+".cap", "must be >= 0")
		}
	}
	if d.Entry.Price != nil {
		if err := d.checkExpr(d.Entry.Price, "entry.price", wantNum, 0, budget); err != nil {
			return nil, err
		}
	}
	return &d, nil
}

type budget struct{ nodes int }

type exprType int

const (
	wantNum exprType = iota
	wantBool
)

// checkExpr 递归校验：未知算子/字段、操作数数量与类型、字面量完整性、
// 周期常量与界、深度与节点预算。命名引用只许已声明 params / 白名单字段。
func (d *Definition) checkExpr(e *Expr, path string, want exprType, depth int, b *budget) error {
	if e == nil {
		return errSyntax(path, "null expression")
	}
	b.nodes--
	if b.nodes < 0 {
		return errSyntax(path, "expression too large (> %d nodes)", maxNodes)
	}
	if depth > maxDepth {
		return errSyntax(path, "expression too deep (> %d)", maxDepth)
	}
	switch e.Op {
	case "num":
		if e.Num == nil {
			return errSyntax(path, "num literal missing value")
		}
		return d.wantType(path, want, wantNum, e.Op)
	case "bool":
		if e.Bool == nil {
			return errSyntax(path, "bool literal missing value")
		}
		return d.wantType(path, want, wantBool, e.Op)
	case "field":
		if !numFields[e.Name] {
			return nilUnknown(path, "field", e.Name, numFields)
		}
		return d.wantType(path, want, wantNum, e.Op)
	case "param":
		if _, ok := d.params[e.Name]; !ok {
			return errSyntax(path, "unknown param %q", e.Name)
		}
		return d.wantType(path, want, wantNum, e.Op)
	case "add", "sub", "mul", "div", "min", "max":
		if err := d.wantType(path, want, wantNum, e.Op); err != nil {
			return err
		}
		return d.checkChildren(e, path, 2, 2, wantNum, depth, b)
	case "clamp":
		if err := d.wantType(path, want, wantNum, e.Op); err != nil {
			return err
		}
		return d.checkChildren(e, path, 3, 3, wantNum, depth, b)
	case "abs":
		if err := d.wantType(path, want, wantNum, e.Op); err != nil {
			return err
		}
		return d.checkChildren(e, path, 1, 1, wantNum, depth, b)
	case "ma_close", "ma_amount", "high_n", "low_n":
		if err := d.wantType(path, want, wantNum, e.Op); err != nil {
			return err
		}
		if len(e.Args) != 1 {
			return errSyntax(path, "%s takes exactly 1 arg (period), got %d", e.Op, len(e.Args))
		}
		p := e.Args[0]
		if p.Op != "num" || p.Num == nil {
			return errSyntax(path+".args[0]", "period must be a num literal")
		}
		n := *p.Num
		if n != float64(int(n)) || n < 1 || n > 250 {
			return errSyntax(path+".args[0]", "period must be an integer in [1, 250] (got %v)", n)
		}
		return nil
	case "gt", "gte", "lt", "lte", "eq", "neq":
		if err := d.wantType(path, want, wantBool, e.Op); err != nil {
			return err
		}
		return d.checkChildren(e, path, 2, 2, wantNum, depth, b)
	case "and", "or":
		if err := d.wantType(path, want, wantBool, e.Op); err != nil {
			return err
		}
		return d.checkChildren(e, path, 2, -1, wantBool, depth, b)
	case "not":
		if err := d.wantType(path, want, wantBool, e.Op); err != nil {
			return err
		}
		return d.checkChildren(e, path, 1, 1, wantBool, depth, b)
	default:
		return errSyntax(path, "unknown op %q", e.Op)
	}
}

func (d *Definition) wantType(path string, want, got exprType, op string) error {
	if want != got {
		return errSyntax(path, "op %s yields %s, want %s", op, typeName(got), typeName(want))
	}
	return nil
}

func (d *Definition) checkChildren(e *Expr, path string, minArgs, maxArgs int, child exprType, depth int, b *budget) error {
	if len(e.Args) < minArgs || (maxArgs >= 0 && len(e.Args) > maxArgs) {
		return errSyntax(path, "op %s takes %s args, got %d", e.Op, arity(minArgs, maxArgs), len(e.Args))
	}
	for i, a := range e.Args {
		if err := d.checkExpr(a, fmt.Sprintf("%s.args[%d]", path, i), child, depth+1, b); err != nil {
			return err
		}
	}
	return nil
}

func arity(min, max int) string {
	if max < 0 {
		return fmt.Sprintf("at least %d", min)
	}
	if min == max {
		return fmt.Sprintf("%d", min)
	}
	return fmt.Sprintf("%d..%d", min, max)
}

func typeName(t exprType) string {
	if t == wantBool {
		return "bool"
	}
	return "num"
}

func nilUnknown(path, kind, name string, allowed map[string]bool) error {
	keys := make([]string, 0, len(allowed))
	for k := range allowed {
		keys = append(keys, k)
	}
	return errSyntax(path, "unknown %s %q (allowed: %s)", kind, name, strings.Join(keys, " "))
}
