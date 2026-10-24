package decl

import (
	"fmt"
	"strings"
	"testing"
)

// okTrue 是恒真 filter 的最小合法表达式。
const okTrue = `{"op":"bool","bool":true}`

func validJSON(filter string) string {
	return fmt.Sprintf(`{"version":1,"name":"dsl_ok","hold_days":3,"filter":%s,"score":{"base":40}}`, filter)
}

// TestParseValid 合法定义：最小式与全件式都过校验，且内部索引就位。
func TestParseValid(t *testing.T) {
	d, err := Parse([]byte(validJSON(okTrue)))
	if err != nil {
		t.Fatalf("minimal: %v", err)
	}
	if d.HoldDays != 3 || d.Name != "dsl_ok" {
		t.Fatalf("minimal fields %+v", d)
	}
	full := `{
	  "version": 1, "name": "dsl_pull", "title": "回踩示例", "hold_days": 5,
	  "params": [{"name":"dd_min","default":0.08,"min":0.02,"max":0.2}],
	  "filter": {"op":"and","args":[
	    {"op":"gte","args":[{"op":"field","name":"dev_ma10"},{"op":"sub","args":[{"op":"num","num":0},{"op":"param","name":"dd_min"}]}]},
	    {"op":"gt","args":[{"op":"ma_close","args":[{"op":"num","num":10}]},{"op":"ma_close","args":[{"op":"num","num":20}]}]},
	    {"op":"or","args":[
	      {"op":"lt","args":[{"op":"field","name":"volume"},{"op":"mul","args":[{"op":"num","num":0.7},{"op":"num","num":1000000}]}]},
	      {"op":"bool","bool":false}
	    ]},
	    {"op":"not","args":[{"op":"eq","args":[{"op":"field","name":"last"},{"op":"high_n","args":[{"op":"num","num":10}]}]}]}
	  ]},
	  "score": {"base": 40, "adds": [
	    {"when": {"op":"gt","args":[{"op":"field","name":"amount"},{"op":"num","num":300000000}]}, "num": {"op":"num","num":5}},
	    {"num": {"op":"clamp","args":[{"op":"abs","args":[{"op":"field","name":"dev_ma10"}]},{"op":"num","num":0},{"op":"num","num":0.03}]}, "cap": 0.015}
	  ]},
	  "entry": {"price": {"op":"mul","args":[{"op":"field","name":"last"},{"op":"num","num":0.99}]}}
	}`
	d, err = Parse([]byte(full))
	if err != nil {
		t.Fatalf("full: %v", err)
	}
	if d.params["dd_min"].Default != 0.08 {
		t.Fatalf("param index %+v", d.params)
	}
	spec := d.Spec()
	if spec.Name != "dsl_pull" || len(spec.Params) != 1 || spec.Params[0].Min != 0.02 {
		t.Fatalf("spec %+v", spec)
	}
}

// TestParseReject 拒绝矩阵：每个非法输入都必须带路径报错（绝不半激活）。
func TestParseReject(t *testing.T) {
	cases := []struct {
		name string
		data string
		want string // 报错应包含的路径/关键字
	}{
		{"空定义", ``, "$"},
		{"未知顶层字段", `{"version":1,"name":"dsl_ok","hold_days":3,"filter":` + okTrue + `,"score":{"base":40},"evil":"drop table"}`, "invalid json"},
		{"未知嵌套字段", `{"version":1,"name":"dsl_ok","hold_days":3,"filter":{"op":"bool","bool":true,"x":1},"score":{"base":40}}`, "invalid json"},
		{"版本 0", strings.Replace(validJSON(okTrue), `"version":1`, `"version":0`, 1), "version"},
		{"版本 2", strings.Replace(validJSON(okTrue), `"version":1`, `"version":2`, 1), "version"},
		{"缺版本", `{"name":"dsl_ok","hold_days":3,"filter":` + okTrue + `,"score":{"base":40}}`, "version"},
		{"名大写", strings.Replace(validJSON(okTrue), `"dsl_ok"`, `"DslOk"`, 1), "name"},
		{"名单字符", strings.Replace(validJSON(okTrue), `"dsl_ok"`, `"a"`, 1), "name"},
		{"名注入串", strings.Replace(validJSON(okTrue), `"dsl_ok"`, `"abc; DROP TABLE x;--"`, 1), "name"},
		{"hold_days 0", strings.Replace(validJSON(okTrue), `"hold_days":3`, `"hold_days":0`, 1), "hold_days"},
		{"hold_days 16", strings.Replace(validJSON(okTrue), `"hold_days":3`, `"hold_days":16`, 1), "hold_days"},
		{"缺 filter", `{"version":1,"name":"dsl_ok","hold_days":3,"score":{"base":40}}`, "filter"},
		{"filter 类型错", validJSON(`{"op":"add","args":[{"op":"num","num":1},{"op":"num","num":2}]}`), "bool"},
		{"参数重名", `{"version":1,"name":"dsl_ok","hold_days":3,"params":[{"name":"a_b","default":1,"min":0,"max":2},{"name":"a_b","default":1,"min":0,"max":2}],"filter":` + okTrue + `,"score":{"base":40}}`, "duplicate"},
		{"参数 min>max", `{"version":1,"name":"dsl_ok","hold_days":3,"params":[{"name":"a_b","default":1,"min":3,"max":2}],"filter":` + okTrue + `,"score":{"base":40}}`, "min"},
		{"参数默认越界", `{"version":1,"name":"dsl_ok","hold_days":3,"params":[{"name":"a_b","default":9,"min":0,"max":2}],"filter":` + okTrue + `,"score":{"base":40}}`, "default"},
		{"未知字段引用", validJSON(`{"op":"eq","args":[{"op":"field","name":"rsi"},{"op":"num","num":70}]}`), "field"},
		{"未知参数引用", validJSON(`{"op":"eq","args":[{"op":"param","name":"nope"},{"op":"num","num":1}]}`), "filter.args[0]"},
		{"未知算子", validJSON(`{"op":"eval","args":[]}`), "unknown op"},
		{"比较缺操作数", validJSON(`{"op":"gt","args":[{"op":"num","num":1}]}`), "args"},
		{"num 缺字面量", validJSON(`{"op":"num"}`), "literal"},
		{"bool 缺字面量", validJSON(`{"op":"bool"}`), "literal"},
		{"指标周期非字面量", validJSON(`{"op":"eq","args":[{"op":"ma_close","args":[{"op":"field","name":"last"}]},{"op":"num","num":1}]}`), "num literal"},
		{"指标周期 0", validJSON(`{"op":"eq","args":[{"op":"ma_close","args":[{"op":"num","num":0}]},{"op":"num","num":1}]}`), "period"},
		{"指标周期 251", validJSON(`{"op":"eq","args":[{"op":"ma_close","args":[{"op":"num","num":251}]},{"op":"num","num":1}]}`), "period"},
		{"指标周期非整数", validJSON(`{"op":"eq","args":[{"op":"ma_close","args":[{"op":"num","num":10.5}]},{"op":"num","num":1}]}`), "period"},
		{"加分缺 num", `{"version":1,"name":"dsl_ok","hold_days":3,"filter":` + okTrue + `,"score":{"base":40,"adds":[{"when":{"op":"bool","bool":true}}]}}`, "score.adds[0].num"},
		{"加分 cap 负", `{"version":1,"name":"dsl_ok","hold_days":3,"filter":` + okTrue + `,"score":{"base":40,"adds":[{"num":{"op":"num","num":1},"cap":-1}]}}`, "cap"},
		{"entry 类型错", `{"version":1,"name":"dsl_ok","hold_days":3,"filter":` + okTrue + `,"score":{"base":40},"entry":{"price":{"op":"bool","bool":true}}}`, "entry.price"},
		{"深度炸弹", validJSON(nestedAdds(30)), "too deep"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, err := Parse([]byte(tc.data))
			if err == nil {
				t.Fatalf("expected rejection, got %+v", d)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err.Error(), tc.want)
			}
		})
	}
}

// TestParseNodeBomb 超节点预算（depth 不超限但节点数超限）。
func TestParseNodeBomb(t *testing.T) {
	// and 树下挂 300 个恒真叶子（2+300+300*0 ≈ 302 节点，anty 深度仅 2）。
	leaves := make([]string, 0, 300)
	for range 300 {
		leaves = append(leaves, okTrue)
	}
	wide := `{"op":"and","args":[` + strings.Join(leaves, ",") + `]}`
	d, err := Parse([]byte(validJSON(wide)))
	if err != nil {
		t.Fatalf("302 nodes should pass: %v", err)
	}
	_ = d
	// 直接超 512：两棵 300 叶树再套一层 and。
	wideTwo := `{"op":"and","args":[` + wide + `,` + wide + `]}`
	if _, err = Parse([]byte(validJSON(wideTwo))); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("expected node budget rejection, got %v", err)
	}
}

// TestParseSizeCap 体积帽：> MaxCodeSize 一律拒。
func TestParseSizeCap(t *testing.T) {
	big := `{"version":1,"name":"dsl_ok","hold_days":3,"filter":` + okTrue + `,"score":{"base":40},"` + strings.Repeat(" ", MaxCodeSize) + `":1}`
	if _, err := Parse([]byte(big)); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("expected size rejection, got %v", err)
	}
}

// nestedAdds 生成 depth 层 add 嵌套的 JSON 片段。
func nestedAdds(depth int) string {
	s := `{"op":"num","num":1}`
	for range depth {
		s = `{"op":"add","args":[` + s + `,{"op":"num","num":1}]}`
	}
	return `{"op":"eq","args":[` + s + `,{"op":"num","num":0}]}`
}
