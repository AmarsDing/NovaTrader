package service

import (
	"testing"

	"server/app/notify/internal/biz"
)

// 通知 symbol 契约：只透传来源事件 Fields["symbol"]，空值不出现（nil），不从标题/正文推断。
func TestToReplySymbol(t *testing.T) {
	cases := []struct {
		name   string
		fields map[string]string
		want   *string
	}{
		{name: "有 symbol 原样输出", fields: map[string]string{"symbol": "600519.SH"}, want: strPtr("600519.SH")},
		{name: "symbol 前后空白 trim", fields: map[string]string{"symbol": "  600519.SH "}, want: strPtr("600519.SH")},
		{name: "空 symbol 不出现", fields: map[string]string{"symbol": "   "}, want: nil},
		{name: "无 fields 不出现", fields: nil, want: nil},
		{name: "有 fields 但无 symbol 不出现", fields: map[string]string{"level": "warn"}, want: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := toReply(biz.Message{ID: 1, Title: "标题", Fields: tc.fields}).Symbol
			if tc.want == nil {
				if got != nil {
					t.Fatalf("want nil, got %q", *got)
				}
				return
			}
			if got == nil || *got != *tc.want {
				t.Fatalf("want %q, got %v", *tc.want, got)
			}
		})
	}
}

func strPtr(s string) *string { return &s }
