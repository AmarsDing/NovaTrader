package biz

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/go-kratos/kratos/v2/log"
)

// 研报预测镜像的合同测试。fixture 按 datahub biz.Intel 的 JSON 形态手写
// （跨 app 不互相 import，两侧结构体 tag 逐字对齐——见 ReportForecast 注释）。

const reportForecastJSON = `{
	"source":"eastmoney","source_id":"AP202610190001","kind":"report",
	"title":"贵州茅台：三季报点评","content":"东吴证券 增持（前次 增持）｜2026E EPS 0.64 / PE 20.18｜目标价 12.50–14.00 元",
	"url":"https://data.eastmoney.com/report/info/AP202610190001.html",
	"publish_time":"2026-10-19T08:30:00+08:00","codes":["600519.SH"],
	"forecast":{
		"eps":[{"year":2026,"eps":0.64,"pe":20.18},{"year":2027,"eps":0.71},{"year":2028,"pe":18.01}],
		"aim_price_low":12.5,"aim_price_high":14.0,
		"rating":"增持","last_rating":"增持","rating_change":3,"report_type":2,
		"researcher":"张三","industry":"白酒","org_code":"org-dw","org_name":"东吴证券"
	}
}`

func f64(v float64) *float64 { return &v }

// 镜像字段 unmarshal 完整保留：同名字段、指针、零占位纪律逐项核对。
func TestRawForecastMirrorRoundTrip(t *testing.T) {
	var raw Raw
	if err := json.Unmarshal([]byte(reportForecastJSON), &raw); err != nil {
		t.Fatal(err)
	}
	fc := raw.Forecast
	if fc == nil {
		t.Fatal("forecast silently dropped by unmarshal")
	}
	if len(fc.EPS) != 3 {
		t.Fatalf("eps = %+v", fc.EPS)
	}
	if fc.EPS[0].Year != 2026 || fc.EPS[0].EPS == nil || *fc.EPS[0].EPS != 0.64 || fc.EPS[0].PE == nil || *fc.EPS[0].PE != 20.18 {
		t.Fatalf("eps[0] = %+v", fc.EPS[0])
	}
	// 缺字段保持 nil，绝不落 0——没取到≠0。
	if fc.EPS[1].EPS == nil || *fc.EPS[1].EPS != 0.71 || fc.EPS[1].PE != nil {
		t.Fatalf("eps[1] = %+v", fc.EPS[1])
	}
	if fc.EPS[2].EPS != nil || fc.EPS[2].PE == nil || *fc.EPS[2].PE != 18.01 {
		t.Fatalf("eps[2] = %+v", fc.EPS[2])
	}
	if fc.AimPriceLow == nil || *fc.AimPriceLow != 12.5 || fc.AimPriceHigh == nil || *fc.AimPriceHigh != 14.0 {
		t.Fatalf("aim = %+v %+v", fc.AimPriceLow, fc.AimPriceHigh)
	}
	if fc.Rating != "增持" || fc.LastRating != "增持" || fc.Researcher != "张三" || fc.Industry != "白酒" || fc.OrgCode != "org-dw" || fc.OrgName != "东吴证券" {
		t.Fatalf("strings passthrough = %+v", fc)
	}
	if fc.RatingChange == nil || *fc.RatingChange != 3 || fc.ReportType == nil || *fc.ReportType != 2 {
		t.Fatalf("enum passthrough = %+v", fc)
	}

	// marshal 回 JSON：nil 字段键缺席（omitempty），不补占位。
	out, err := json.Marshal(&raw)
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatal(err)
	}
	fm, ok := back["forecast"].(map[string]any)
	if !ok {
		t.Fatalf("re-marshaled forecast = %s", out)
	}
	eps, ok := fm["eps"].([]any)
	if !ok || len(eps) != 3 {
		t.Fatalf("re-marshaled eps = %v", fm["eps"])
	}
	if _, has := eps[1].(map[string]any)["pe"]; has {
		t.Fatalf("nil pe leaked into json: %v", eps[1])
	}
	if _, has := eps[2].(map[string]any)["eps"]; has {
		t.Fatalf("nil eps leaked into json: %v", eps[2])
	}
}

// 旧版无 forecast 键的事件在新 Raw 上照常解码（Forecast==nil，前进兼容）；
// 新版含 forecast 的事件在旧结构上只是多余字段，不报错（后退兼容）。
func TestRawForecastCompatibility(t *testing.T) {
	legacy := `{"source":"eastmoney","source_id":"a1","kind":"report","title":"t","content":"c","publish_time":"2026-10-19"}`
	var raw Raw
	if err := json.Unmarshal([]byte(legacy), &raw); err != nil {
		t.Fatal(err)
	}
	if raw.Forecast != nil {
		t.Fatalf("legacy json must leave Forecast nil: %+v", raw.Forecast)
	}
	type oldRaw struct {
		Source   string `json:"source"`
		SourceID string `json:"source_id"`
		Title    string `json:"title"`
	}
	var old oldRaw
	if err := json.Unmarshal([]byte(reportForecastJSON), &old); err != nil {
		t.Fatal(err)
	}
	if old.SourceID != "AP202610190001" {
		t.Fatalf("old consumer lost identity: %+v", old)
	}
}

// prepare 原样透传 Forecast：不加工、不补缺。
func TestPreparePassesForecastThrough(t *testing.T) {
	u := NewUsecase(newMemRepo(), nil, nil, log.DefaultLogger)
	fc := &ReportForecast{Rating: "增持", EPS: []EPSPoint{{Year: 2026, EPS: f64(0.64)}}}
	it, err := u.prepare(Raw{Source: "eastmoney", SourceID: "AP1", Kind: "report", Title: "研报", Content: "内容", Forecast: fc})
	if err != nil {
		t.Fatal(err)
	}
	if it.Forecast != fc {
		t.Fatalf("prepare must pass the pointer through untouched: %+v", it.Forecast)
	}
	it, err = u.prepare(Raw{Source: "eastmoney", SourceID: "AP2", Kind: "report", Title: "研报", Content: "内容"})
	if err != nil {
		t.Fatal(err)
	}
	if it.Forecast != nil {
		t.Fatalf("missing forecast must stay nil: %+v", it.Forecast)
	}
}

// Process(kind=report, Raw 带 Forecast) → CreateItem 收到的 Item.Forecast 非空。
func TestProcessDeliversForecastToCreateItem(t *testing.T) {
	repo := newMemRepo()
	u := newTestUsecase(t, repo, nil, nil)
	fc := &ReportForecast{Rating: "买入", AimPriceHigh: f64(14.0)}
	res, err := u.Process(context.Background(), Raw{Source: "eastmoney", SourceID: "AP9", Kind: "report",
		Title: "宁德时代深度报告", Content: "东吴证券 买入｜目标价 14.00 元", Codes: []string{"300750.SZ"}, Forecast: fc})
	if err != nil {
		t.Fatal(err)
	}
	if res.NewsID == 0 {
		t.Fatalf("res = %+v", res)
	}
	got := repo.items[res.NewsID].it.Forecast
	if got == nil || got.Rating != "买入" || got.AimPriceHigh == nil {
		t.Fatalf("CreateItem received forecast = %+v", got)
	}
}
