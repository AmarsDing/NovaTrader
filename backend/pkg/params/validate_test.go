package params

import "testing"

func TestStrictCatalogueAndRanges(t *testing.T) {
	if _, err := ValidatePatch(nil, map[string]string{"market.sentiment.rules": "{} {}"}); err == nil {
		t.Fatal("trailing JSON accepted")
	}
	bad := []map[string]string{{"m06.unknown": "1"}, {"m06.rule_top_n": "2.5"}, {"m06.select_score": "NaN"}, {"m06.select_score": "101"}, {"app.brain.large.url": "http://user:secret@localhost"}, {"market.sentiment.rules": `{"injected":1}`}, {"market.sector.weights": `{"min_members":0}`}, {"risk.max_single_pct": "2"}, {"risk.live_admitted": "true"}}
	for _, v := range bad {
		if _, e := ValidatePatch(nil, v); e == nil {
			t.Errorf("accepted invalid patch keys %v", v)
		}
	}
	valid := map[string]string{"app.brain.large.key": "secret", "m06.rule_top_n": "20", "risk.max_single_pct": "0.05", "market.sentiment.rules": `{"hot_min_up":80}`}
	if _, err := ValidatePatch(nil, valid); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidatePatch(valid, map[string]string{"app.brain.large.key": ""}); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidatePatch(map[string]string{"m06.fb.gap_min": "0.01"}, map[string]string{"m06.fb.gap_max": "0"}); err == nil {
		t.Fatal("inverted range accepted")
	}
}

// TestLLMGovernanceKeys 锁 M13-T06 白名单：brain/intel 的 disable_json_mode 开关，
// 以及密钥类键接受 env:/enc: 前缀值（裸 env: 不完整需 422）。
func TestLLMGovernanceKeys(t *testing.T) {
	good := map[string]string{
		"app.brain.large.disable_json_mode": "true",
		"app.brain.small.disable_json_mode": "false",
		"app.intel.disable_json_mode":       "true",
		"app.brain.large.key":               "env:DEEPSEEK_API_KEY",
		"app.brain.small.key":               "enc:Y2lwaGVy",
		"app.intel.key":                     "env:INTEL_LLM_KEY",
		"app.kb.key":                        "env:KB_EMBED_KEY",
	}
	if _, err := ValidatePatch(nil, good); err != nil {
		t.Fatal(err)
	}
	bads := []map[string]string{
		{"app.brain.large.disable_json_mode": "yes"},
		{"app.intel.disable_json_mode": "1"},
		{"app.brain.large.key": "env:"},
		// 嵌入无 JSON 模式概念，未登记的键必须仍被拦下。
		{"app.kb.disable_json_mode": "true"},
		{"app.brain.large.json_mode": "false"},
	}
	for _, v := range bads {
		if _, e := ValidatePatch(nil, v); e == nil {
			t.Errorf("accepted invalid patch %v", v)
		}
	}
	if svc, _ := Target("app.brain.large.disable_json_mode"); svc != "brain" {
		t.Fatal("brain disable_json_mode key not routed to brain")
	}
	if svc, _ := Target("app.intel.disable_json_mode"); svc != "intel" {
		t.Fatal("intel disable_json_mode key not routed to intel")
	}
}

func TestDatahubKeyFamily(t *testing.T) {
	good := map[string]string{
		"app.ds.eastmoney.qps":            "6",
		"app.ds.tushare.quota":            "500",
		"app.ds.sina.quota":               "0",
		"app.ds.tdx_file.timeout_sec":     "45",
		"app.ds.openbb_fred.on":           "true",
		"app.ds.openbb_fred.key":          "fred-secret",
		"app.ds.tdx_http_archive.on":      "false",
		"app.ds.eastmoney.margin_reports": "RPTA_WEB_RZRQ_GGMX,RPTA_WEB_RZRQ_DETAIL",
		"app.domain.money_flow.sources":   `["eastmoney","tushare"]`,
		"app.domain.snapshot.sources":     `["tdx_local","tdx_tcp","eastmoney","sina","tencent"]`,
		"app.domain.block_trade.sources":  `["eastmoney"]`,
		"app.domain.guba.sources":         `["eastmoney"]`,
		"app.domain.flash.sources":        `["eastmoney","sina7x24","tavily","rss"]`,
	}
	if _, err := ValidatePatch(nil, good); err != nil {
		t.Fatal(err)
	}
	bads := []map[string]string{
		{"app.ds.eastmoney.qps": "0"}, {"app.ds.eastmoney.qps": "51"}, {"app.ds.eastmoney.qps": "1.5"},
		{"app.ds.tushare.quota": "-1"}, {"app.ds.tushare.quota": "100001"},
		{"app.ds.tdx_file.timeout_sec": "0"}, {"app.ds.tdx_file.timeout_sec": "121"}, {"app.ds.tdx_file.timeout_sec": "abc"},
		{"app.domain.money_flow.sources": `["sina"]`},
		{"app.domain.money_flow.sources": `[]`},
		{"app.domain.money_flow.sources": `["tushare","tushare"]`},
		{"app.domain.money_flow.sources": `{"a":1}`},
		{"app.domain.money_flow.sources": `["eastmoney"] trailing`},
		{"app.domain.money_flow.sources": `["eastmoney","ghost"]`},
		{"app.domain.ghost.sources": `["eastmoney"]`},
		{"app.ds.ghost.on": "true"},
		{"app.domain.money_flow.sources": `["sina7x24","tushare"]`},
	}
	for _, v := range bads {
		if _, e := ValidatePatch(nil, v); e == nil {
			t.Errorf("accepted invalid patch keys %v", v)
		}
	}
	// 空串=清覆盖回落 yaml，qps、降级链与 text 键都可回收。
	clears := map[string]string{
		"app.ds.eastmoney.qps":            "",
		"app.domain.money_flow.sources":   "",
		"app.ds.eastmoney.margin_reports": "",
	}
	if _, err := ValidatePatch(good, clears); err != nil {
		t.Fatal(err)
	}
	if svc, _ := Target("app.domain.snapshot.sources"); svc != "datahub" {
		t.Fatal("domain chain key not routed to datahub")
	}
	if svc, _ := Target("app.ds.eastmoney.qps"); svc != "datahub" {
		t.Fatal("source qps key not routed to datahub")
	}
	if !Secret("app.ds.openbb_fred.key") {
		t.Fatal("fred key must be masked by .key suffix")
	}
	if !SupportsDataSource("snapshot", "sina") {
		t.Fatal("sina must support snapshot")
	}
	if SupportsDataSource("money_flow", "sina") {
		t.Fatal("sina must not support money_flow")
	}
	// 本批落地能力：block_trade/guba 由 eastmoney 提供，sina7x24 接 intel 域（flash 等），
	// baostock 接 daily_bar/finance（W5/W9 收口；评审 blocker：能力不补则降级链登记被 422）。
	if !SupportsDataSource("block_trade", "eastmoney") {
		t.Fatal("eastmoney must support block_trade")
	}
	if !SupportsDataSource("guba", "eastmoney") {
		t.Fatal("eastmoney must support guba")
	}
	if !SupportsDataSource("flash", "sina7x24") {
		t.Fatal("sina7x24 must support flash")
	}
	if !SupportsDataSource("daily_bar", "baostock") || !SupportsDataSource("finance", "baostock") {
		t.Fatal("baostock must support daily_bar and finance")
	}
}
