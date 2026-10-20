package data

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"server/app/datahub/internal/biz"
)

// W4 两融回退链 + W6 研报解析的包级离线钉死（评审 major 交付件）。
// 全部走 stub 传输回环（照 guba_test 的 routeTransport / httpc_test 的
// stubTransport 先例：驻留 *httpc 换 stub，backoff=1ms 不真睡），无真实网络。
//
// 键值读取点 marginReports 在 data 层直接调 appcfg.Get（strategy_config 下发）；
// 离线单测里 appcfg 无注入缝（Bind 需要真库，data 包既有测试零绑定先例），
// 故「双名单」钉在键值解析器 parseReportChain 与默认名单的链行为上，
// 「首名 9501→次名成功」跨名断言需在实现侧补链注入缝后再钉（交 finalizer）。

// seqTransport 记序 stub 传输：serve 依 URL 按需回包，并把请求 URL 依序记录，
// 供回退链「请求顺序」断言。
type seqTransport struct {
	mu    sync.Mutex
	urls  []string
	serve func(url string) (status int, body string)
}

func (t *seqTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	u := req.URL.String()
	t.mu.Lock()
	t.urls = append(t.urls, u)
	t.mu.Unlock()
	status, body := http.StatusOK, `{}`
	if t.serve != nil {
		status, body = t.serve(u)
	}
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
}

// reportNames 抽取已发请求的 reportName 参数序列（报表名单词是 ASCII，无需解码）。
func (t *seqTransport) reportNames() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]string, 0, len(t.urls))
	for _, u := range t.urls {
		if i := strings.Index(u, "reportName="); i >= 0 {
			name := u[i+len("reportName="):]
			if j := strings.IndexByte(name, '&'); j >= 0 {
				name = name[:j]
			}
			out = append(out, name)
		}
	}
	return out
}

func newSeqEastmoney(serve func(string) (int, string)) (*Eastmoney, *seqTransport) {
	tt := &seqTransport{serve: serve}
	// HTTPOptions 不带 P3-9 的 Retries（其批未落 HEAD）：零值即不重试，429 一次即错，
	// 仍足以钉「限流不换名」；退避计数/标签断言随 P3-9 落地后回补（本批挂账第 8 条）。
	hc := newHTTPC(biz.SourceEastmoney, HTTPOptions{QPS: 10000, Concurrency: 1}, nil)
	hc.hc = &http.Client{Transport: tt}
	return &Eastmoney{hc: hc}, tt
}

// dc 业务码语义：9501（实测「报表配置不存在」）→ ReportGoneError 且错误文案行首含
// 【报表轮换】标签；9201 与无码 success → 空成功；其他业务码 → 普通 error 不换名。
func TestDCReportGone(t *testing.T) {
	ctx := context.Background()

	t.Run("9501 折成 ReportGoneError", func(t *testing.T) {
		em, _ := newSeqEastmoney(func(string) (int, string) {
			return 200, `{"success":false,"code":9501,"message":"报表配置不存在","result":null}`
		})
		_, err := em.dc(ctx, "RPT_X", "", "")
		if err == nil {
			t.Fatal("want error")
		}
		var gone *ReportGoneError
		if !errors.As(err, &gone) {
			t.Fatalf("want *ReportGoneError, got %T %v", err, err)
		}
		if gone.Code != 9501 || gone.Report != "RPT_X" {
			t.Fatalf("gone=%+v，想 Code=9501 Report=RPT_X", gone)
		}
		if !strings.HasPrefix(err.Error(), "【报表轮换】") {
			t.Fatalf("错误文案行首须含【报表轮换】标签（健康面板可辨）: %v", err)
		}
	})

	t.Run("9201 与无码成功均空成功", func(t *testing.T) {
		for _, body := range []string{
			`{"success":false,"code":9201,"message":"查询成功","result":null}`,
			`{"success":true,"result":null}`,
		} {
			em, _ := newSeqEastmoney(func(string) (int, string) { return 200, body })
			rows, err := em.dc(ctx, "RPT_X", "", "")
			if err != nil || len(rows) != 0 {
				t.Fatalf("body=%s → rows=%v err=%v，想空成功", body, rows, err)
			}
		}
	})

	t.Run("其他非 9501 业务码是普通 error 不换名", func(t *testing.T) {
		em, _ := newSeqEastmoney(func(string) (int, string) {
			return 200, `{"success":false,"code":9502,"message":"查询参数错误","result":null}`
		})
		_, err := em.dc(ctx, "RPT_X", "", "")
		if err == nil {
			t.Fatal("want error")
		}
		var gone *ReportGoneError
		if errors.As(err, &gone) {
			t.Fatalf("非 9501 不得标轮换（错误码白名单先窄后宽）: %v", err)
		}
		if strings.HasPrefix(err.Error(), "【报表轮换】") {
			t.Fatalf("行首不得带【报表轮换】: %v", err)
		}
		if !strings.Contains(err.Error(), "RPT_X: 9502") {
			t.Fatalf("普通 error 须带报表名与业务码: %v", err)
		}
	})
}

// Margins 回退链：仅 ReportGoneError（9501）换名，限流/网络错误立即失败不换名；
// 全链灭返回首个轮换错误；运行时键空值回退默认名单。
func TestMarginReportChain(t *testing.T) {
	ctx := context.Background()
	day := time.Date(2026, 10, 19, 0, 0, 0, 0, time.Local)

	t.Run("默认名单成功解析且请求顺序可断言", func(t *testing.T) {
		em, tt := newSeqEastmoney(func(u string) (int, string) {
			if strings.Contains(u, "reportName="+emMarginReportDefault) {
				return 200, `{"success":true,"result":{"pages":1,"data":[
					{"SCODE":"600519","RZYE":100.5,"RZMRE":20,"RZCHE":10,"RQYE":5,"RQMCL":3,"RZRQYE":105.5}
				]}}`
			}
			return 200, `{"success":false,"code":9501,"message":"报表配置不存在","result":null}`
		})
		items, err := em.Margins(ctx, day)
		if err != nil {
			t.Fatalf("Margins: %v", err)
		}
		if got := tt.reportNames(); len(got) != 1 || got[0] != emMarginReportDefault {
			t.Fatalf("链请求顺序=%v，想单名默认 [%s]", got, emMarginReportDefault)
		}
		if len(items) != 1 {
			t.Fatalf("items=%d want 1", len(items))
		}
		m := items[0]
		if m.Symbol != "600519.SH" || m.Rzye != 100.5 || m.Rzmre != 20 || m.Rzche != 10 ||
			m.Rqye != 5 || m.Rqmcl != 3 || m.Rzrqye != 105.5 {
			t.Fatalf("两融字段解析不对: %+v", m)
		}
	})

	t.Run("默认名单 9501 全链灭返回首个轮换错误", func(t *testing.T) {
		em, tt := newSeqEastmoney(func(string) (int, string) {
			return 200, `{"success":false,"code":9501,"message":"报表配置不存在","result":null}`
		})
		_, err := em.Margins(ctx, day)
		var gone *ReportGoneError
		if !errors.As(err, &gone) {
			t.Fatalf("want *ReportGoneError（全链灭=首个轮换错误），got %T %v", err, err)
		}
		if gone.Report != emMarginReportDefault {
			t.Fatalf("gone.Report=%q want %s", gone.Report, emMarginReportDefault)
		}
		if !strings.HasPrefix(err.Error(), "【报表轮换】") {
			t.Fatalf("错误文案行首须含【报表轮换】: %v", err)
		}
		if got := tt.reportNames(); len(got) != 1 {
			t.Fatalf("默认名单是单名链，链请求=%v", got)
		}
	})

	t.Run("首名 429 限流不换名立即错", func(t *testing.T) {
		em, tt := newSeqEastmoney(func(string) (int, string) { return 429, `{}` })
		_, err := em.Margins(ctx, day)
		if err == nil {
			t.Fatal("want throttled error")
		}
		var gone *ReportGoneError
		if errors.As(err, &gone) {
			t.Fatalf("限流不得误判轮换（防雪崩式探名）: %v", err)
		}
		// 【疑似限流/封禁】标签断言随 P3-9 落地回补（其批给 403/429 打标，HEAD 尚无）。
		for _, name := range tt.reportNames() {
			if name != emMarginReportDefault {
				t.Fatalf("限流时链不得换名，请求了 %s", name)
			}
		}
		if n := len(tt.reportNames()); n != 1 {
			t.Fatalf("限流应停在首名即错（零重试配置下 1 次），calls=%d", n)
		}
	})

	t.Run("appcfg 键双名单解析 trim/去重/保序", func(t *testing.T) {
		got := parseReportChain("RPT_B, RPT_A,,RPT_B")
		if len(got) != 2 || got[0] != "RPT_B" || got[1] != "RPT_A" {
			t.Fatalf("双名单解析=%v，想 [RPT_B RPT_A]", got)
		}
	})

	t.Run("appcfg 键空值回退默认名单", func(t *testing.T) {
		// 前提：data 测试进程未 Bind appcfg（全包既有测试零绑定先例），Get 返回空串。
		for _, key := range []string{"", " , ,"} {
			if got := parseReportChain(key); got != nil {
				t.Fatalf("parseReportChain(%q)=%v，想 nil（调用方回退默认）", key, got)
			}
		}
		em, _ := newSeqEastmoney(nil)
		got := em.marginReports(ctx)
		if len(got) != 1 || got[0] != emMarginReportDefault {
			t.Fatalf("未绑定时 marginReports=%v，想 [%s]", got, emMarginReportDefault)
		}
	})
}

// 研报盈利预测离线解析：parseReportNum 哨兵、年度映射（item 级 publishDate 年份为锚、
// 缺失回退响应级 meta.currentYear——跨年水位回看 currentYear 漂移 +1 会致 est_year 错键，
// 对抗复核 #7 锚序反转）三年度展开、EPS/PE 双 nil 年度点整条丢弃、
// rating/report_type 数码字符串原样透传、非数码不赋值。
func TestReportParsing(t *testing.T) {
	ctx := context.Background()

	t.Run("parseReportNum 空串/非数→nil 数码→指针", func(t *testing.T) {
		for _, s := range []string{"", " ", "abc", "1.0x"} {
			if got := parseReportNum(s); got != nil {
				t.Fatalf("parseReportNum(%q)=%v want nil（空串=未披露，没取到≠0）", s, *got)
			}
		}
		if got := parseReportNum(" 2.5 "); got == nil || *got != 2.5 {
			t.Fatalf("parseReportNum(\" 2.5 \")=%v", got)
		}
		if got := parseReportNum("0"); got == nil || *got != 0 {
			t.Fatalf("真 0 是合法数值必须保留: %v", got)
		}
	})

	isReportList := func(u string) bool { return strings.Contains(u, "reportapi.eastmoney.com/report/list") }

	t.Run("meta 缺失回退 publishDate 年份、双 nil 年度点丢弃", func(t *testing.T) {
		em, _ := newSeqEastmoney(func(u string) (int, string) {
			if isReportList(u) {
				return 200, `{"TotalPage":1,"data":[
					{"title":"公司点评","stockCode":"600519","orgSName":"东吴证券","publishDate":"2026-10-19 10:00:00","infoCode":"10001",
					 "emRatingName":"买入","lastEmRatingName":"增持","researcher":"张三","indvInduName":"白酒","orgCode":"80001","orgName":"东吴证券研究所",
					 "predictThisYearEps":"0.64","predictThisYearPe":"20.18",
					 "predictNextYearEps":"","predictNextYearPe":"",
					 "predictNextTwoYearEps":"1.10","predictNextTwoYearPe":"15.20",
					 "indvAimPriceT":"14.00","indvAimPriceL":"12.50",
					 "ratingChange":"2","reportType":"1"},
					{"title":"调研简报","stockCode":"000001","orgSName":"海通证券","publishDate":"2026-10-20 09:30:00","infoCode":"10002",
					 "emRatingName":"","researcher":"李四",
					 "predictThisYearEps":"","predictThisYearPe":"",
					 "predictNextYearEps":"","predictNextYearPe":"",
					 "predictNextTwoYearEps":"","predictNextTwoYearPe":""}
				]}`
			}
			return 200, `{"data":[]}`
		})
		items, err := em.reports(ctx, biz.IntelQuery{Since: time.Date(2026, 10, 19, 0, 0, 0, 0, time.Local)})
		if err != nil {
			t.Fatalf("reports: %v", err)
		}
		if len(items) != 2 {
			t.Fatalf("items=%d want 2", len(items))
		}
		var full, bare *biz.Intel
		for i := range items {
			switch items[i].SourceID {
			case "10001":
				full = &items[i]
			case "10002":
				bare = &items[i]
			}
		}
		if full == nil || bare == nil {
			t.Fatalf("items 缺行: %+v", items)
		}
		if full.Forecast == nil {
			t.Fatal("有预测字段行 Forecast 不得为 nil")
		}
		fc := full.Forecast
		// 年度映射：meta 无 currentYear → 回退 publishDate 2026 年；第二年行双 nil 整条丢弃。
		if len(fc.EPS) != 2 {
			t.Fatalf("双 nil 年度点应整条丢弃，EPS=%+v", fc.EPS)
		}
		if fc.EPS[0].Year != 2026 || fc.EPS[0].EPS == nil || *fc.EPS[0].EPS != 0.64 || fc.EPS[0].PE == nil || *fc.EPS[0].PE != 20.18 {
			t.Fatalf("当前年点不对: %+v", fc.EPS[0])
		}
		if fc.EPS[1].Year != 2028 || fc.EPS[1].EPS == nil || *fc.EPS[1].EPS != 1.10 {
			t.Fatalf("后两年点不对（中间年双 nil 已跳过）: %+v", fc.EPS[1])
		}
		if fc.AimPriceLow == nil || *fc.AimPriceLow != 12.50 || fc.AimPriceHigh == nil || *fc.AimPriceHigh != 14.00 {
			t.Fatalf("目标价不对: %+v %+v", fc.AimPriceLow, fc.AimPriceHigh)
		}
		// rating 原样透传；ratingChange/reportType 数码字符串透传为 int。
		if fc.Rating != "买入" || fc.LastRating != "增持" {
			t.Fatalf("rating 须原样透传: %+v", fc)
		}
		if fc.RatingChange == nil || *fc.RatingChange != 2 || fc.ReportType == nil || *fc.ReportType != 1 {
			t.Fatalf("数码字符串应透传为 int: RatingChange=%v ReportType=%v", fc.RatingChange, fc.ReportType)
		}
		wantContent := "东吴证券 买入（前次 增持）｜2026E EPS 0.64 / PE 20.18｜2028E EPS 1.10 / PE 15.20｜目标价 12.50–14.00 元"
		if full.Content != wantContent {
			t.Fatalf("content 组句不对:\n got %q\nwant %q", full.Content, wantContent)
		}
		if full.PublishTime != "2026-10-19T10:00:00+08:00" || full.Kind != biz.KindReport {
			t.Fatalf("PublishTime/Kind 不对: %+v", full)
		}
		if len(full.Codes) != 1 || full.Codes[0] != "600519.SH" {
			t.Fatalf("Codes 不对: %+v", full.Codes)
		}
		// 全部预测缺失行：Forecast 留 nil，content 回落旧格式（机构+评级 trim）。
		if bare.Forecast != nil {
			t.Fatalf("全预测缺失行 Forecast 须为 nil: %+v", bare.Forecast)
		}
		if bare.Content != "海通证券" {
			t.Fatalf("content 回落=机构名: %q", bare.Content)
		}
	})

	t.Run("非数码枚举不赋值", func(t *testing.T) {
		em, _ := newSeqEastmoney(func(u string) (int, string) {
			if isReportList(u) {
				return 200, `{"TotalPage":1,"currentYear":2026,"data":[
					{"title":"首次覆盖","stockCode":"600519","orgSName":"招商证券","publishDate":"2026-10-19 11:00:00","infoCode":"10004",
					 "emRatingName":"买入","ratingChange":"abc","reportType":"x",
					 "predictThisYearEps":"","predictThisYearPe":"",
					 "predictNextYearEps":"","predictNextYearPe":"",
					 "predictNextTwoYearEps":"","predictNextTwoYearPe":""}
				]}`
			}
			return 200, `{"data":[]}`
		})
		items, err := em.reports(ctx, biz.IntelQuery{Since: time.Date(2026, 10, 19, 0, 0, 0, 0, time.Local)})
		if err != nil {
			t.Fatalf("reports: %v", err)
		}
		if len(items) != 1 || items[0].Forecast == nil {
			t.Fatalf("有评级行 Forecast 不得为 nil: %+v", items)
		}
		fc := items[0].Forecast
		if fc.RatingChange != nil || fc.ReportType != nil {
			t.Fatalf("非数码枚举应不赋值（不臆造口径）: RatingChange=%v ReportType=%v", fc.RatingChange, fc.ReportType)
		}
		if len(fc.EPS) != 0 {
			t.Fatalf("预测全空行 EPS 应为空: %+v", fc.EPS)
		}
		if items[0].Content != "招商证券 买入" {
			t.Fatalf("content 组句不对: %q", items[0].Content)
		}
	})

	// 跨年回看形态（#7）：响应采于 2027（currentYear=2027），研报发布于 2026——est_year
	// 必须锚定发布年 2026，不受采集时刻漂移污染（旧锚序会得到 2027 错键）。
	t.Run("publishDate 年份为锚（currentYear 跨年漂移不得污染 est_year）", func(t *testing.T) {
		em, _ := newSeqEastmoney(func(u string) (int, string) {
			if isReportList(u) {
				return 200, `{"TotalPage":1,"currentYear":2027,"data":[
					{"title":"深度报告","stockCode":"600519","orgSName":"国泰君安","publishDate":"2026-12-31 10:00:00","infoCode":"10003",
					 "predictThisYearEps":"1.00","predictThisYearPe":"",
					 "predictNextYearEps":"","predictNextYearPe":"",
					 "predictNextTwoYearEps":"","predictNextTwoYearPe":""}
				]}`
			}
			return 200, `{"data":[]}`
		})
		items, err := em.reports(ctx, biz.IntelQuery{Since: time.Date(2026, 10, 19, 0, 0, 0, 0, time.Local)})
		if err != nil {
			t.Fatalf("reports: %v", err)
		}
		if len(items) != 1 || items[0].Forecast == nil {
			t.Fatalf("items=%v", items)
		}
		eps := items[0].Forecast.EPS
		if len(eps) != 1 || eps[0].Year != 2026 || eps[0].EPS == nil || *eps[0].EPS != 1.00 || eps[0].PE != nil {
			t.Fatalf("est_year 应锚发布年 2026 且 PE 空串留 nil: %+v", eps)
		}
	})

	// publishDate 缺失：降级响应级 currentYear 兜底（年拿不到即丢整段 EPS 的另一分支）。
	t.Run("publishDate 缺失回退 currentYear", func(t *testing.T) {
		em, _ := newSeqEastmoney(func(u string) (int, string) {
			if isReportList(u) {
				return 200, `{"TotalPage":1,"currentYear":2027,"data":[
					{"title":"缺日期行","stockCode":"600519","orgSName":"国泰君安","publishDate":"","infoCode":"10005",
					 "predictThisYearEps":"0.50","predictThisYearPe":"",
					 "predictNextYearEps":"","predictNextYearPe":"",
					 "predictNextTwoYearEps":"","predictNextTwoYearPe":""}
				]}`
			}
			return 200, `{"data":[]}`
		})
		items, err := em.reports(ctx, biz.IntelQuery{Since: time.Date(2027, 1, 1, 0, 0, 0, 0, time.Local)})
		if err != nil {
			t.Fatalf("reports: %v", err)
		}
		if len(items) != 1 || items[0].Forecast == nil {
			t.Fatalf("items=%v", items)
		}
		eps := items[0].Forecast.EPS
		if len(eps) != 1 || eps[0].Year != 2027 || eps[0].EPS == nil || *eps[0].EPS != 0.50 {
			t.Fatalf("publishDate 缺失应回退 currentYear=2027: %+v", eps)
		}
	})
}
