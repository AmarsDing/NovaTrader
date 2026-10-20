package data

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"golang.org/x/text/encoding/simplifiedchinese"
)

/* AH 报价链路测试（T12–T24）。全走 httptest 双服务（q.emURL / q.quoteURL 指向），
 * 零真网络。工程红线 a（r_hk 不转小写、不过 symbol.Parse/fromLower）钉在 T24。 */

func gbkEnc(s string) []byte {
	b, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(s))
	if err != nil {
		return []byte(s)
	}
	return b
}

// tqRow 造腾讯响应行：posMap 指定下标值，其余补 "1"，总长 n。
func tqRow(n int, posMap map[int]string) string {
	f := make([]string, n)
	for i := range f {
		f[i] = "1"
	}
	for k, v := range posMap {
		f[k] = v
	}
	return strings.Join(f, "~")
}

func newTestAHQuote(t *testing.T, f10 http.HandlerFunc, qt http.HandlerFunc) *AHQuote {
	t.Helper()
	f10Srv := httptest.NewServer(f10)
	qtSrv := httptest.NewServer(qt)
	t.Cleanup(f10Srv.Close)
	t.Cleanup(qtSrv.Close)
	q := NewAHQuote(log.DefaultLogger)
	q.emURL = f10Srv.URL
	q.quoteURL = qtSrv.URL + "/q="
	return q
}

const f10PairOK = `{"version":"x","result":{"pages":1,"data":[{"SECUCODE":"601939.SH","STR_CODEH":"00939.HK","STR_NAMEA":"建设银行","SECURITY_NAME_ABBR":"建设银行","STR_NAMEH":"建设银行"}],"count":1},"success":true,"message":"ok","code":0}`
const f10PureA = `{"result":{"pages":1,"data":[{"SECUCODE":"000001.SZ","STR_CODEH":null,"STR_NAMEA":"平安银行","SECURITY_NAME_ABBR":"平安银行","STR_NAMEH":null}],"count":1},"success":true,"message":"ok","code":0}`

// 腾讯三符号混合域批响应样例（照 2026-10-20 活源形态裁剪）。
func tripleBody(sym, hk string) string {
	a := `v_sh` + sym + `="` + tqRow(40, map[int]string{
		1: "建设银行", 3: "10.00", 6: "847887", 30: "20261019161436",
	}) + `";`
	h := `v_r_hk` + hk + `="` + tqRow(45, map[int]string{
		1: "建设银行", 3: "9.865", 30: "2026/10/16 16:08:22", 37: "183102483.0",
	}) + `";`
	fx := `v_whHKDCNY="` + tqRow(22, map[int]string{
		1: "港元人民币", 2: "HKDCNY", 3: "0.92", 5: "20261020065912",
	}) + `";`
	return a + h + fx
}

// T12：F10 映射（.HK 后缀剥除、STR_NAMEA 在场、SECURITY_NAME_ABBR 兜底）。
func TestAHPairMapping(t *testing.T) {
	q := newTestAHQuote(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawQuery, "RPT_F10_BASIC_ORGINFO") {
			t.Errorf("reportName 缺失：%s", r.URL.RawQuery)
		}
		w.Write([]byte(f10PairOK))
	}, nil)
	pair, err := q.AHPair(context.Background(), "601939.SH")
	if err != nil || !pair.Pair {
		t.Fatalf("pair=%+v err=%v", pair, err)
	}
	if pair.HCode != "00939" || pair.AName != "建设银行" || pair.HName != "建设银行" ||
		pair.ACode != "601939" || pair.AMarket != "SH" {
		t.Fatalf("映射失败:%+v", pair)
	}

	// STR_NAMEA 空时 SECURITY_NAME_ABBR 兜底。
	q = newTestAHQuote(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"result":{"data":[{"SECUCODE":"601398.SH","STR_CODEH":"01398.HK","STR_NAMEA":"","SECURITY_NAME_ABBR":"工商银行","STR_NAMEH":null}]},"success":true,"code":0}`))
	}, nil)
	pair, _ = q.AHPair(context.Background(), "601398.SH")
	if pair.AName != "工商银行" || !pair.Pair {
		t.Fatalf("兜底映射失败:%+v", pair)
	}
}

// T13+T16：9201 / 空 data / STR_CODEH null / 非 .HK 后缀——四态合一判 no_pair 不报错。
func TestAHPairNoPairSemantics(t *testing.T) {
	cases := []struct{ name, symbol, body string }{
		{"9201", "601939.SH", `{"success":false,"code":9201,"message":"查询成功","result":null}`},
		{"空data", "601939.SH", `{"result":{"pages":0,"data":[],"count":0},"success":true,"code":0}`},
		{"codeh null", "000001.SZ", f10PureA},
		{"非 .HK", "601939.SH", `{"result":{"data":[{"SECUCODE":"601939.SH","STR_CODEH":"00939.US","STR_NAMEA":"建设银行","STR_NAMEH":null}]},"success":true,"code":0}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := newTestAHQuote(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(c.body)) }, nil)
			pair, err := q.AHPair(context.Background(), c.symbol)
			if err != nil {
				t.Fatalf("no_pair 不得报错:%v", err)
			}
			if pair.Pair {
				t.Fatalf("%s 应判 no_pair:%+v", c.name, pair)
			}
		})
	}
	// 纯 A 行在场时 AName 照常回波（能查到什么给什么）。
	q := newTestAHQuote(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(f10PureA)) }, nil)
	pair, _ := q.AHPair(context.Background(), "000001.SZ")
	if pair.Pair || pair.AName != "平安银行" {
		t.Fatalf("纯 A 事实列回波失败:%+v", pair)
	}
}

// T14+T15：9501 报表轮换（行首标签）/ 成功码畸形 → error。
func TestAHPairCodeSplit(t *testing.T) {
	q := newTestAHQuote(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"success":false,"code":9501,"message":"报表配置不存在","result":null}`))
	}, nil)
	_, err := q.AHPair(context.Background(), "601939.SH")
	if err == nil || !strings.HasPrefix(err.Error(), "【报表轮换】") {
		t.Fatalf("T14：9501 须带【报表轮换】行首标签:%v", err)
	}
	if _, err := q.AHPair(context.Background(), "601939.SH"); err == nil {
		t.Fatal("T14：配对错误不缓存，重查必须重发请求")
	}

	q = newTestAHQuote(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"success":true,"code":0,"result":null}`))
	}, nil)
	if _, err := q.AHPair(context.Background(), "601939.SH"); err == nil || !strings.Contains(err.Error(), "畸形") {
		t.Fatalf("T15：成功码 result 缺=请求坏了:%v", err)
	}
}

// T17：TTL 缓存三钉——正 24h 命中 / 负 30min 命中短查 / 过期重查失败不复用陈旧。
func TestAHPairCacheTTL(t *testing.T) {
	now := time.Date(2026, 10, 20, 10, 0, 0, 0, time.Local)
	hits := 0
	q := newTestAHQuote(t, func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write([]byte(f10PairOK))
	}, nil)
	q.now = func() time.Time { return now }

	if _, err := q.AHPair(context.Background(), "601939.SH"); err != nil {
		t.Fatal(err)
	}
	if _, err := q.AHPair(context.Background(), "601939.SH"); err != nil {
		t.Fatal(err)
	}
	if hits != 1 {
		t.Fatalf("T17a：正命中 TTL 内重查不得发请求，hits=%d", hits)
	}
	q.now = func() time.Time { return now.Add(23 * time.Hour) }
	q.AHPair(context.Background(), "601939.SH")
	if hits != 1 {
		t.Fatalf("T17a：24h 未过仍应命中，hits=%d", hits)
	}
	q.now = func() time.Time { return now.Add(25 * time.Hour) }
	q.AHPair(context.Background(), "601939.SH")
	if hits != 2 {
		t.Fatalf("T17a：过期须重查，hits=%d", hits)
	}

	// 负缓存 30min：no_pair 命中短查。
	hitsNeg := 0
	q2 := newTestAHQuote(t, func(w http.ResponseWriter, r *http.Request) {
		hitsNeg++
		w.Write([]byte(f10PureA))
	}, nil)
	q2.now = func() time.Time { return now }
	q2.AHPair(context.Background(), "000001.SZ")
	q2.AHPair(context.Background(), "000001.SZ")
	if hitsNeg != 1 {
		t.Fatalf("T17b：负缓存 30min 内不得重查，hits=%d", hitsNeg)
	}
	q2.now = func() time.Time { return now.Add(31 * time.Minute) }
	q2.AHPair(context.Background(), "000001.SZ")
	if hitsNeg != 2 {
		t.Fatalf("T17b：负缓存过期须重查，hits=%d", hitsNeg)
	}

	// 过期且重查失败 → 上抛 error，绝不回带陈旧配对（对立档口径）。
	hits3 := 0
	q3 := newTestAHQuote(t, func(w http.ResponseWriter, r *http.Request) {
		hits3++
		if hits3 == 1 {
			w.Write([]byte(f10PairOK))
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}, nil)
	q3.now = func() time.Time { return now }
	if _, err := q3.AHPair(context.Background(), "601939.SH"); err != nil {
		t.Fatal(err)
	}
	q3.now = func() time.Time { return now.Add(25 * time.Hour) }
	pair, err := q3.AHPair(context.Background(), "601939.SH")
	if err == nil || !strings.Contains(err.Error(), "疑似限流/封禁") {
		t.Fatalf("T17c：过期+刷新失败须上抛限流标签错误:%v", err)
	}
	if pair.Pair {
		t.Fatalf("T17c：不得复用陈旧配对当真：%+v", pair)
	}
}

// T18：GBK 编码响应体必须正常切行解析。
func TestAHTripleGBK(t *testing.T) {
	hits := 0
	q := newTestAHQuote(t, nil, func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write(gbkEnc(tripleBody("601939", "00939")))
	})
	triple, err := q.AHTriple(context.Background(), "601939", "SH", "00939")
	if err != nil || hits != 1 {
		t.Fatalf("T18：err=%v hits=%d", err, hits)
	}
	if triple.A.Price == nil || *triple.A.Price != 10.00 {
		t.Fatalf("T18：GBK 后 A 腿:%+v", triple.A)
	}
}

// T19：三 layout 解析（A 紧凑上海 / H 斜杠港时区 / FX f[5] 紧凑）。含 CJK 的
// fixture 必须 GBK 编码上链——生 UTF-8 汉字经握 GBK 解码会吃掉相邻 ASCII 分隔符、
// 整行字段错位（本仓实测：f[5] 移位成 "1" 时刻静默零值）。
func TestAHTripleLayouts(t *testing.T) {
	q := newTestAHQuote(t, nil, func(w http.ResponseWriter, r *http.Request) {
		w.Write(gbkEnc(tripleBody("601939", "00939")))
	})
	triple, err := q.AHTriple(context.Background(), "601939", "SH", "00939")
	if err != nil {
		t.Fatal(err)
	}
	if triple.A.Price == nil || triple.H.Price == nil || triple.FX.Price == nil {
		t.Fatalf("三腿价:%+v", triple)
	}
	if got := triple.A.Time.Format("2006-01-02 15:04:05"); got != "2026-10-19 16:14:36" {
		t.Fatalf("A 腿时刻（本地时区回读）:%s", got)
	}
	if got := triple.H.Time.UTC().Format("2006-01-02 15:04:05"); got != "2026-10-16 08:08:22" {
		t.Fatalf("H 腿斜杠布局+港时区（UTC 16:08:22-08:00）:%s", got)
	}
	if got := triple.FX.Time.Format("2006-01-02 15:04:05"); got != "2026-10-20 06:59:12" {
		t.Fatalf("FX f[5] 紧凑时刻:%s", got)
	}
	if !triple.ATraded {
		t.Fatal("f[6]>0 应判 ATraded")
	}
}

// T20+T21+T7(数据面)：价格 "0"=na_value、空串/行缺=no_data、停牌腿 f[6]=0 → ATraded=false。
func TestAHTripleMissingLegs(t *testing.T) {
	// H 价格字段 "0"（na 哨兵）+ FX 整行缺失 + A 停牌（f[6]=0）。
	a := `v_sh601939="` + tqRow(40, map[int]string{3: "10.00", 6: "0", 30: "20261019161436"}) + `";`
	h := `v_r_hk00939="` + tqRow(45, map[int]string{3: "0", 30: "2026/10/16 16:08:22"}) + `";`
	q := newTestAHQuote(t, nil, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(a + h)) // 无 FX 行
	})
	triple, err := q.AHTriple(context.Background(), "601939", "SH", "00939")
	if err != nil {
		t.Fatal(err)
	}
	if triple.H.Price != nil || triple.H.Why != "na_value" {
		t.Fatalf("T20：价格 0=na_value 哨兵:%+v", triple.H)
	}
	if triple.FX.Price != nil || triple.FX.Why != "no_data" {
		t.Fatalf("T21：FX 行缺=no_data:%+v", triple.FX)
	}
	if triple.A.Price == nil || triple.ATraded {
		t.Fatalf("T7 数据面：停牌腿价照常回波且 ATraded=false:%+v traded=%v", triple.A, triple.ATraded)
	}

	// 价格字段空串=no_data 与 0 分家（没取到≠0 的第一道闸）。
	a2 := `v_sh601939="` + tqRow(40, map[int]string{3: "", 30: "20261019161436"}) + `";`
	q = newTestAHQuote(t, nil, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(a2)) })
	triple, _ = q.AHTriple(context.Background(), "601939", "SH", "00939")
	if triple.A.Price != nil || triple.A.Why != "no_data" {
		t.Fatalf("T20：空串=no_data:%+v", triple.A)
	}
}

// T22：请求级失败分家——403【疑似限流/封禁】、500 平错。
func TestAHTripleTransportErrors(t *testing.T) {
	q := newTestAHQuote(t, nil, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	if _, err := q.AHTriple(context.Background(), "601939", "SH", "00939"); err == nil ||
		!strings.Contains(err.Error(), "疑似限流/封禁") {
		t.Fatalf("T22：403 带标签:%v", err)
	}
	q = newTestAHQuote(t, nil, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	if _, err := q.AHTriple(context.Background(), "601939", "SH", "00939"); err == nil ||
		strings.Contains(err.Error(), "疑似限流/封禁") {
		t.Fatalf("T22：500 不得误打限流标签:%v", err)
	}
}

// T23：时间解析失败不连带丢价（价保留、时刻零值）。
func TestAHTripleBadTimeKeepsPrice(t *testing.T) {
	a := `v_sh601939="` + tqRow(40, map[int]string{3: "10.00", 6: "100", 30: "垃圾时刻"}) + `";`
	q := newTestAHQuote(t, nil, func(w http.ResponseWriter, r *http.Request) { w.Write(gbkEnc(a)) })
	triple, err := q.AHTriple(context.Background(), "601939", "SH", "00939")
	if err != nil {
		t.Fatal(err)
	}
	if triple.A.Price == nil || *triple.A.Price != 10.00 {
		t.Fatalf("T23：坏时刻不得丢价:%+v", triple.A)
	}
	if !triple.A.Time.IsZero() {
		t.Fatalf("T23：坏时刻须零值透出:%v", triple.A.Time)
	}
}

// T24（工程红线 a）：拼接保持 r_hk 原样不转小写、FX 变量名原样、H/FX 不过归一链。
// 注意：腾讯报价格式 "q=..." 在 URL path 段而非 query，故断言 RequestURI。
func TestAHTripleQueryVerbatim(t *testing.T) {
	var gotURI string
	q := newTestAHQuote(t, nil, func(w http.ResponseWriter, r *http.Request) {
		gotURI = r.URL.RequestURI()
		w.Write(gbkEnc(tripleBody("601939", "00939")))
	})
	if _, err := q.AHTriple(context.Background(), "601939", "SH", "00939"); err != nil {
		t.Fatal(err)
	}
	if gotURI != "/q=sh601939,r_hk00939,whHKDCNY" {
		t.Fatalf("T24：查询串必须逐字保序（r_hk 不转小写、whHKDCNY 原样）:%s", gotURI)
	}
}

/* 对抗复核闭环测试（2026-10-20，workflow wf_61445026-230，14 CONFIRMED）——
 * T25 业务码先于 success、T26 SECUCODE 归属、T27 港股代码形态、T28 整体不可切分、
 * T29 截断行捞救（parse 可达+在场价不丢）、T30 非有限/负价哨兵。 */

// T25：业务码先于 success 布尔判定（复核前 success=true 时 code 被跳过：
// 9201 谎报「成功码畸形」pair_error、9501/9999+有效 data 静默当真配对并正 24h 缓存）。
func TestAHPairCodeBeforeSuccess(t *testing.T) {
	q := newTestAHQuote(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"success":true,"code":9201,"message":"查询成功","result":null}`))
	}, nil)
	pair, err := q.AHPair(context.Background(), "601939.SH")
	if err != nil || pair.Pair {
		t.Fatalf("T25a：success=true+9201 须为 no_pair 不报错:pair=%+v err=%v", pair, err)
	}

	q = newTestAHQuote(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"result":{"data":[{"SECUCODE":"601939.SH","STR_CODEH":"00939.HK"}]},"success":true,"code":9501,"message":"报表配置不存在"}`))
	}, nil)
	if _, err := q.AHPair(context.Background(), "601939.SH"); err == nil ||
		!strings.HasPrefix(err.Error(), "【报表轮换】") {
		t.Fatalf("T25b：success=true+9501 须报轮换不静默当真:%v", err)
	}

	q = newTestAHQuote(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"result":{"data":[{"SECUCODE":"601939.SH","STR_CODEH":"00939.HK"}]},"success":true,"code":9999,"message":"x"}`))
	}, nil)
	if _, err := q.AHPair(context.Background(), "601939.SH"); err == nil ||
		!strings.Contains(err.Error(), "9999") {
		t.Fatalf("T25c：success=true+9999 须上抛失败码:%v", err)
	}
}

// T26：F10 首行 SECUCODE 归属不符 → 上抛，不得错配对静默产假溢价。
func TestAHPairSeccodeMismatch(t *testing.T) {
	q := newTestAHQuote(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"result":{"data":[{"SECUCODE":"601398.SH","STR_CODEH":"01398.HK"}]},"success":true,"code":0}`))
	}, nil)
	if _, err := q.AHPair(context.Background(), "601939.SH"); err == nil ||
		!strings.Contains(err.Error(), "归属不符") {
		t.Fatalf("T26：归属不符须上抛:%v", err)
	}
}

// T27：STR_CODEH 非数字形态 → 【配对畸形】（宁可拒不可拼，hCode 原样进查询 path）。
func TestAHPairBadHKCode(t *testing.T) {
	for _, h := range []string{"ABCDE.HK", "00939,1.HK", "0093/9.HK", "123456.HK", ".HK"} {
		q := newTestAHQuote(t, func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"result":{"data":[{"SECUCODE":"601939.SH","STR_CODEH":"` + h + `"}]},"success":true,"code":0}`))
		}, nil)
		if _, err := q.AHPair(context.Background(), "601939.SH"); err == nil ||
			!strings.Contains(err.Error(), "配对畸形") {
			t.Fatalf("T27：%q 须判配对畸形:%v", h, err)
		}
	}
}

// T28：2xx 但三符号全缺/整体不可切分 → 上抛（biz 映射 source_error，不得谎标 missing）。
func TestAHTripleUnsplittable(t *testing.T) {
	q := newTestAHQuote(t, nil, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("垃圾响应体无任何行"))
	})
	if _, err := q.AHTriple(context.Background(), "601939", "SH", "00939"); err == nil ||
		!strings.Contains(err.Error(), "不可切分") {
		t.Fatalf("T28a：不可切分须上抛:%v", err)
	}
	// 行可切但三个请求符号全缺（源回了别的券）→ 同样上抛。
	q = newTestAHQuote(t, nil, func(w http.ResponseWriter, r *http.Request) {
		w.Write(gbkEnc(`v_sh600519="` + tqRow(40, map[int]string{3: "1500.00"}) + `";`))
	})
	if _, err := q.AHTriple(context.Background(), "601939", "SH", "00939"); err == nil ||
		!strings.Contains(err.Error(), "全缺") {
		t.Fatalf("T28b：三符号全缺须上抛:%v", err)
	}
}

// T29：截断行捞救——在场价不丢（时刻下标不及则零值透出）；行在场拿不到价才 parse
// （复核前 min 全行字段闸错标 no_data 且连坐在场价，parse 态不可达）。
func TestAHTripleTruncatedRowSalvage(t *testing.T) {
	a := `v_sh601939="1~建设银行~601939~10.5~8";`
	q := newTestAHQuote(t, nil, func(w http.ResponseWriter, r *http.Request) { w.Write(gbkEnc(a)) })
	triple, err := q.AHTriple(context.Background(), "601939", "SH", "00939")
	if err != nil {
		t.Fatal(err)
	}
	if triple.A.Price == nil || *triple.A.Price != 10.5 || triple.A.Why != "" {
		t.Fatalf("T29a：截断行在场价不得丢:%+v", triple.A)
	}
	if !triple.A.Time.IsZero() || triple.ATraded {
		t.Fatalf("T29a：时刻下标不及须零值且 ATraded=false:time=%v traded=%v", triple.A.Time, triple.ATraded)
	}

	a2 := `v_sh601939="1~建";`
	q = newTestAHQuote(t, nil, func(w http.ResponseWriter, r *http.Request) { w.Write(gbkEnc(a2)) })
	triple, _ = q.AHTriple(context.Background(), "601939", "SH", "00939")
	if triple.A.Price != nil || triple.A.Why != "parse" {
		t.Fatalf("T29b：行在场拿不到价须 parse 不再错标 no_data:%+v", triple.A)
	}

	fx := `v_whHKDCNY="310~港元人民币~HKDCNY~0.85";`
	q = newTestAHQuote(t, nil, func(w http.ResponseWriter, r *http.Request) { w.Write(gbkEnc(fx)) })
	triple, _ = q.AHTriple(context.Background(), "601939", "SH", "00939")
	if triple.FX.Price == nil || *triple.FX.Price != 0.85 || !triple.FX.Time.IsZero() {
		t.Fatalf("T29c：FX 截断捞救（价留时刻零）:%+v", triple.FX)
	}
}

// T30：NaN/Inf 字面量与负价=parse 哨兵（strconv.ParseFloat 会接受这些串；
// 复核实证裁决：protojson 对其序列化返 200 不报错，坏值会以 status=ok 穿透衍生列）。
func TestAHNumNonFinite(t *testing.T) {
	for _, s := range []string{"NaN", "Inf", "+Inf", "-Inf", "-3.5", "abc"} {
		a := `v_sh601939="` + tqRow(40, map[int]string{3: s, 6: "100", 30: "20261019161436"}) + `";`
		q := newTestAHQuote(t, nil, func(w http.ResponseWriter, r *http.Request) { w.Write(gbkEnc(a)) })
		triple, err := q.AHTriple(context.Background(), "601939", "SH", "00939")
		if err != nil {
			t.Fatal(err)
		}
		if triple.A.Price != nil || triple.A.Why != "parse" {
			t.Fatalf("T30：%q 须判 parse:%+v", s, triple.A)
		}
	}
}
