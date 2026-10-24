package gateway

import (
	"net/http"
	"testing"
)

// TestLiveGateAuditMapping 钉死 M13-T05 的先审计后转发口径：LIVE 放行/收权路径
// 必须映射到与 trade biz 同源的敏感动作（audit 关闸时代理直接 503）；相似但错位
// 的路径不得命中；GET 从不审计。
func TestLiveGateAuditMapping(t *testing.T) {
	cases := []struct {
		method, path, want string
	}{
		{http.MethodPost, "/api/trade/admission/live/grant", "trade.grant_live"},
		{http.MethodPost, "/api/trade/admission/live/revoke", "trade.revoke_live"},
		{http.MethodPost, "/api/trade/live/cash-baseline", "trade.cash_baseline"},
		{http.MethodGet, "/api/trade/admission/live/grant", ""},
		{http.MethodGet, "/api/trade/live/cash-baseline", ""},
		{http.MethodPost, "/api/trade/admission/live/grantx", ""},
		{http.MethodPost, "/api/trade/admission", ""},
		{http.MethodPost, "/api/trade/live", ""},
	}
	for _, tc := range cases {
		if got := auditAction(tc.method, tc.path); got != tc.want {
			t.Errorf("%s %s: got %q want %q", tc.method, tc.path, got, tc.want)
		}
	}
	if !sensitive("trade.grant_live") || !sensitive("trade.revoke_live") || !sensitive("trade.cash_baseline") {
		t.Fatal("live gate actions must stay sensitive so audit-off aborts the proxy")
	}
}
