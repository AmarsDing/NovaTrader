package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type readinessReply struct {
	AsOf     string          `json:"as_of"`
	Ready    bool            `json:"ready"`
	Gates    []readinessGate `json:"gates"`
	Blockers []readinessGate `json:"blockers"`
}

func readinessUpstream(t *testing.T, body string) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.Close)
	return s
}

func readinessGateForTest(t *testing.T, secret string, upstreams map[string]string) (int, readinessReply) {
	t.Helper()
	g := &Gate{Settings: Settings{Secret: secret, Upstreams: upstreams}}
	req := httptest.NewRequest(http.MethodGet, "/api/ops/readiness", nil)
	if secret != "" {
		token, err := Issue(secret, User{Name: "viewer", Role: "viewer"}, time.Now())
		if err != nil {
			t.Fatalf("发 token 失败：%v", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)
	var out readinessReply
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("回执不是 JSON：%v，body=%s", err, w.Body.String())
		}
	}
	return w.Code, out
}

func blockersByName(resp readinessReply) map[string]readinessGate {
	out := make(map[string]readinessGate, len(resp.Blockers))
	for _, b := range resp.Blockers {
		out[b.Gate] = b
	}
	return out
}

func TestReadinessRequiresToken(t *testing.T) {
	code, _ := readinessGateForTest(t, "", nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("无 token 应 401，实际 %d", code)
	}
}

func TestReadinessAllGreen(t *testing.T) {
	upstreams := map[string]string{
		"risk":    readinessUpstream(t, `{"mode":"NORMAL","configReady":true,"ready":true,"killSwitch":{"active":false}}`).URL,
		"trade":   readinessUpstream(t, `{"reportJson":"{}","consecutiveDays":20,"requiredDays":20,"runEvidenceReady":true,"liveEnabled":false,"brokerConnected":true}`).URL,
		"datahub": readinessUpstream(t, `{"sources":[{"source":"tdx","state":"closed"},{"source":"akshare","state":"closed"}]}`).URL,
		"brain":   readinessUpstream(t, `{"tiers":[{"tier":"small","model":"qwen","configured":true,"circuitOpen":false}]}`).URL,
	}
	code, resp := readinessGateForTest(t, "readiness-secret", upstreams)
	if code != http.StatusOK {
		t.Fatalf("应 200，实际 %d", code)
	}
	if !resp.Ready {
		t.Fatalf("全绿应 ready=true，blockers=%+v", resp.Blockers)
	}
	if len(resp.Blockers) != 0 {
		t.Fatalf("全绿不应有 blocker：%+v", resp.Blockers)
	}
	// 全绿时 risk_config 不出现，固定 6 道；序固定（首闸 risk_engine）。
	if len(resp.Gates) != 6 {
		t.Fatalf("闸门应为 6 道，实际 %d：%+v", len(resp.Gates), resp.Gates)
	}
	if resp.Gates[0].Gate != "risk_engine" || resp.Gates[len(resp.Gates)-1].Gate != "llm_config" {
		t.Fatalf("闸门序漂移：%+v", resp.Gates)
	}
	for _, gt := range resp.Gates {
		if gt.State != "ok" {
			t.Fatalf("全绿时 %s 应 ok，实际 %+v", gt.Gate, gt)
		}
		if gt.Severity != "" {
			t.Fatalf("ok 行不应带 severity：%+v", gt)
		}
	}
}

func TestReadinessBlocksWithProgress(t *testing.T) {
	upstreams := map[string]string{
		"risk":    readinessUpstream(t, `{"ready":false,"configReady":false,"killSwitch":{"active":true,"reason":"人工按下","operator":"owner"}}`).URL,
		"trade":   readinessUpstream(t, `{"consecutiveDays":12,"requiredDays":20,"runEvidenceReady":false,"brokerConnected":false}`).URL,
		"datahub": readinessUpstream(t, `{"sources":[{"source":"tdx","state":"open"},{"source":"akshare","state":"half_open"},{"source":"cls","state":"closed"}]}`).URL,
		"brain":   readinessUpstream(t, `{"tiers":[{"tier":"small","model":"qwen","configured":false},{"tier":"mid","model":"deepseek","configured":true,"circuitOpen":true}]}`).URL,
	}
	code, resp := readinessGateForTest(t, "readiness-secret", upstreams)
	if code != http.StatusOK {
		t.Fatalf("应 200，实际 %d", code)
	}
	if resp.Ready {
		t.Fatalf("多闸不绿应 ready=false；gates=%+v", resp.Gates)
	}
	if len(resp.Gates) != 7 {
		t.Fatalf("risk_config 应随 configReady=false 出现，共 7 道，实际 %d：%+v", len(resp.Gates), resp.Gates)
	}
	b := blockersByName(resp)

	if gt := b["risk_engine"]; gt.State != "down" || gt.Severity != "danger" {
		t.Errorf("risk_engine 应 down/danger：%+v", gt)
	}
	if gt := b["risk_config"]; gt.State != "warn" || gt.Severity != "warn" {
		t.Errorf("risk_config 应 warn：%+v", gt)
	}
	if gt := b["risk_killswitch"]; gt.State != "down" || gt.Severity != "danger" || !strings.Contains(gt.Why, "人工按下") {
		t.Errorf("risk_killswitch 应 down/danger 且带原因：%+v", gt)
	}
	adm := b["sim_admission"]
	if adm.State != "warn" || adm.Severity != "warn" {
		t.Errorf("sim_admission 未达标是倒计时，应 warn：%+v", adm)
	}
	if adm.Progress == nil || adm.Progress.Done != 12 || adm.Progress.Total != 20 {
		t.Errorf("sim_admission 应带 12/20 进度：%+v", adm)
	}
	if adm.Deeplink != "/trade#admission" {
		t.Errorf("sim_admission deeplink 漂移：%s", adm.Deeplink)
	}
	if gt := b["broker_gw"]; gt.State != "warn" || gt.Severity != "warn" {
		t.Errorf("broker_gw 应 warn：%+v", gt)
	}
	src := b["datahub_sources"]
	if src.State != "down" || src.Severity != "danger" || !strings.Contains(src.Why, "tdx") {
		t.Errorf("datahub_sources 应 down/danger 且点名：%+v", src)
	}
	// 已配置档全部熔断 → danger。
	if gt := b["llm_config"]; gt.State != "down" || gt.Severity != "danger" || !strings.Contains(gt.Why, "deepseek") {
		t.Errorf("llm_config 全熔断应 down/danger 且点名：%+v", gt)
	}
}

func TestReadinessLLMUnconfigured(t *testing.T) {
	upstreams := map[string]string{
		"risk":    readinessUpstream(t, `{"ready":true,"configReady":true,"killSwitch":{"active":false}}`).URL,
		"trade":   readinessUpstream(t, `{"consecutiveDays":20,"requiredDays":20,"runEvidenceReady":true,"brokerConnected":true}`).URL,
		"datahub": readinessUpstream(t, `{"sources":[{"source":"tdx","state":"closed"}]}`).URL,
		"brain":   readinessUpstream(t, `{"tiers":[{"tier":"small","model":"","configured":false},{"tier":"mid","model":"","configured":false}]}`).URL,
	}
	code, resp := readinessGateForTest(t, "readiness-secret", upstreams)
	if code != http.StatusOK || resp.Ready {
		t.Fatalf("无已配置模型档应 ready=false：code=%d ready=%v", code, resp.Ready)
	}
	b := blockersByName(resp)
	if len(b) != 1 {
		t.Fatalf("应只有 llm_config 一闸：%+v", resp.Blockers)
	}
	if gt := b["llm_config"]; gt.State != "down" || gt.Severity != "danger" || gt.Deeplink != "/config#models" {
		t.Errorf("llm_config 无配置应 down/danger 指配置页：%+v", gt)
	}
}

func TestReadinessUpstreamDownFailClosed(t *testing.T) {
	// 上游全未配置（base 空 → 请求必败）：「没取到≠0」，不可达不等于通过。
	code, resp := readinessGateForTest(t, "readiness-secret", map[string]string{})
	if code != http.StatusOK {
		t.Fatalf("聚合本身应 200，实际 %d", code)
	}
	if resp.Ready {
		t.Fatalf("上游全挂应 ready=false")
	}
	b := blockersByName(resp)
	checks := []struct {
		gate, state, severity string
	}{
		{"risk_engine", "down", "danger"},
		{"risk_killswitch", "unknown", "warn"},
		{"sim_admission", "down", "danger"},
		{"broker_gw", "unknown", "warn"},
		{"datahub_sources", "down", "danger"},
		{"llm_config", "unknown", "warn"},
	}
	for _, want := range checks {
		gt, ok := b[want.gate]
		if !ok {
			t.Errorf("缺 blocker %s", want.gate)
			continue
		}
		if gt.State != want.state || gt.Severity != want.severity {
			t.Errorf("%s 应 %s/%s，实际 %+v", want.gate, want.state, want.severity, gt)
		}
	}
}
