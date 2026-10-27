package gateway

import "testing"

// W2-c 拆态钉板：源状态归级——熔断/禁用/不可用=down；半开、覆盖率未证实=warn（不绿不红）；
// 空串/unknown=unknown 暗点（没测过≠健康）；closed 等常态=ok。前端 statusDot 四色同口径。
func TestSourceNodeStatus(t *testing.T) {
	cases := map[string]string{
		"closed":           "ok",
		"ready":            "ok",
		"open":             "down",
		"unavailable":      "down",
		"disabled":         "down",
		"half_open":        "warn",
		"coverage_unknown": "warn",
		"":                 "unknown",
		"unknown":          "unknown",
	}
	for state, want := range cases {
		if got := sourceNodeStatus(state); got != want {
			t.Fatalf("state %q: got %q want %q", state, got, want)
		}
	}
}
