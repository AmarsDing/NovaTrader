package data

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"server/app/market/internal/biz"

	"github.com/go-kratos/kratos/v2/log"
	"golang.org/x/text/encoding/simplifiedchinese"
)

/* AH 报价链路（目录册增补 B5）：market 侧自建自含薄 client。
 *
 * 白名单端点（护栏①，仅此三点）：
 *   1. 东财 datacenter-web RPT_F10_BASIC_ORGINFO（与 datahub emDC 同宿主同参数形态）
 *   2. 腾讯 qt.gtimg.cn（A/H/HKDCNY 三符号同批一次 GET，FX 同批零新增源）
 * 显式超时 + 并发上限（信号量）+ 按需不轮询。
 *
 * 工程红线（两锚）：
 *   a. r_hk 代码 / whHKDCNY 绝不过 symbol.Parse / fromLower 归一链——
 *      quote_web.go:200/202 两段会把非常规代码清空丢行（「R_HK00939」9 字符
 *      无点拆分必错）。H/FX 符号按原生 var 名就地解析、拼接保持原样；
 *      「r_hk 不转小写」因大小写敏感由此从风格钉升级为正确性红线（T-suite 有钉）。
 *   b. 时间三 layout（2026-10-20 活源验收实证）：A "20060102150405"(Asia/Shanghai)、
 *      H "2006/01/02 15:04:05"(Asia/Hong_Kong，带空格斜杠——按 A layout 解析会静默丢 H 腿)、
 *      FX f[5] 紧凑。时间解析失败不连带丢价（价保留、时刻零值、warn 落日志）。
 *
 * 配对缓存：进程内 map，(pairing, fetched_at) TTL 正 24h / 负 30min（≤交易日级）。
 * 过期即重查；重查失败 → error 上抛（biz 映射 pair_error=配对未知不产溢价数），
 * 绝不复用陈旧当真、绝不把刷新失败谎报成 no_pair（对等立档口径）。
 */

const (
	emF10URL        = "https://datacenter-web.eastmoney.com/api/data/v1/get"
	tencentQuoteURL = "https://qt.gtimg.cn/q="

	ahHTTPTimeout    = 5 * time.Second
	ahMaxConcurrent  = 2
	ahPairTTLPos     = 24 * time.Hour
	ahPairTTLNeg     = 30 * time.Minute
	tencentReferer   = "https://gu.qq.com/"
	tencentUserAgent = "Mozilla/5.0"
)

// AHQuote 是 biz.AHSource 的实现。
type AHQuote struct {
	hc  *http.Client
	sem chan struct{}
	log *log.Helper

	// 端点白名单（测试缝：white-box 测试改写指向 httptest 服务）。
	emURL    string
	quoteURL string

	mu    sync.Mutex
	cache map[string]ahPairCacheEntry // 无淘汰（明文化，对抗复核挂账）：A+H 配对宇宙有界（千级），进程日活占用可忽略，重启即清
	now   func() time.Time            // 测试缝（TTL 用例注入）
}

type ahPairCacheEntry struct {
	pair      biz.AHPairing
	fetchedAt time.Time
}

// NewAHQuote 构造数据源薄 client（wire 需要，无外部清理需求）。
func NewAHQuote(logger log.Logger) *AHQuote {
	return &AHQuote{
		hc:       &http.Client{Timeout: ahHTTPTimeout},
		sem:      make(chan struct{}, ahMaxConcurrent),
		log:      log.NewHelper(logger),
		emURL:    emF10URL,
		quoteURL: tencentQuoteURL,
		cache:    map[string]ahPairCacheEntry{},
		now:      time.Now,
	}
}

// do 在白名单 client 上发 GET：并发上限 + 上下文 + 超时（http.Client.Timeout）；
// 403/429/非 2xx 打行首标签【疑似限流/封禁】上抛，body 限读 1MB。
func (q *AHQuote) do(ctx context.Context, rawURL, referer string) ([]byte, error) {
	select {
	case q.sem <- struct{}{}:
		defer func() { <-q.sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", tencentUserAgent)
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	resp, err := q.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
			return nil, fmt.Errorf("【疑似限流/封禁】GET %s → HTTP %d", rawURL, resp.StatusCode)
		}
		return nil, fmt.Errorf("GET %s → HTTP %d", rawURL, resp.StatusCode)
	}
	return body, nil
}

/* ---------- F10 配对 ---------- */

// AHPair 查东财 F10 配对（TTL 缓存命中短路）。Pair=false=证实无 H 配对；
// 失败上抛 error（含缓存过期且刷新失败——biz 映射 pair_error，不复用陈旧）。
// 已知接受项（对抗复核挂账）：冷缓存并发同 symbol 可能双查一次——突发由
// 信号量 2 封顶、配对查询按需低频，不引 singleflight。
func (q *AHQuote) AHPair(ctx context.Context, symbol string) (biz.AHPairing, error) {
	if ent, ok := q.cached(symbol); ok {
		return ent.pair, nil
	}
	pair, err := q.fetchPair(ctx, symbol)
	if err != nil {
		return biz.AHPairing{}, err
	}
	q.mu.Lock()
	q.cache[symbol] = ahPairCacheEntry{pair: pair, fetchedAt: q.now()}
	q.mu.Unlock()
	return pair, nil
}

func (q *AHQuote) cached(symbol string) (ahPairCacheEntry, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	ent, ok := q.cache[symbol]
	if !ok {
		return ahPairCacheEntry{}, false
	}
	ttl := ahPairTTLPos
	if !ent.pair.Pair {
		ttl = ahPairTTLNeg
	}
	if q.now().Sub(ent.fetchedAt) >= ttl {
		return ahPairCacheEntry{}, false
	}
	return ent, true
}

// emF10Resp 是 datacenter-web /api/data/v1/get 响应骨架（业务码语义对齐
// datahub dc()：9201/空=确实没有；9501=报表轮换→pair_error 不自动换名；
// 其他非 0 码/成功码畸形 data=请求坏了→pair_error）。
type emF10Resp struct {
	Success bool   `json:"success"`
	Code    int    `json:"code"`
	Message string `json:"message"`
	Result  *struct {
		Data []struct {
			Seccode  string  `json:"SECUCODE"`
			StrCodeH *string `json:"STR_CODEH"`
			StrNameA *string `json:"STR_NAMEA"`
			Abbr     *string `json:"SECURITY_NAME_ABBR"`
			StrNameH *string `json:"STR_NAMEH"`
		} `json:"data"`
	} `json:"result"`
}

func (q *AHQuote) fetchPair(ctx context.Context, symbol string) (biz.AHPairing, error) {
	code, mkt, _ := strings.Cut(symbol, ".")
	out := biz.AHPairing{ACode: code, AMarket: mkt}

	v := url.Values{}
	v.Set("reportName", "RPT_F10_BASIC_ORGINFO")
	v.Set("columns", "SECUCODE,STR_CODEH,STR_NAMEA,SECURITY_NAME_ABBR,STR_NAMEH")
	v.Set("filter", `(SECUCODE="`+symbol+`")`)
	v.Set("pageNumber", "1")
	v.Set("pageSize", "1")
	v.Set("source", "DataCenter")
	v.Set("client", "PC")
	body, err := q.do(ctx, q.emURL+"?"+v.Encode(), "")
	if err != nil {
		return out, err
	}
	var resp emF10Resp
	if err := json.Unmarshal(body, &resp); err != nil {
		return out, fmt.Errorf("F10 响应畸形（%s）", err)
	}
	// 业务码先于 success 布尔判定（对齐 datahub dc() 先例；对抗复核钉：success=true
	// 也必须尊重 9201/9501/非 0 码，否则 9201 会被谎报 pair_error、9501/其他非 0 码
	// 会被静默当真配对并正 24h 缓存）。
	switch {
	case resp.Code == 9201:
		return out, nil // 确实没有（空也是成功语义的一种）
	case resp.Code == 9501:
		return out, fmt.Errorf("【报表轮换】RPT_F10_BASIC_ORGINFO 不可用（code=9501 %s）", resp.Message)
	case resp.Code != 0:
		return out, fmt.Errorf("F10 失败码 %d（%s）", resp.Code, resp.Message)
	case !resp.Success:
		return out, fmt.Errorf("F10 success=false（code=0）：%s", resp.Message)
	case resp.Result == nil:
		return out, fmt.Errorf("F10 成功码畸形：result 缺")
	}
	if len(resp.Result.Data) == 0 {
		return out, nil
	}
	row := resp.Result.Data[0]
	if row.Seccode != symbol {
		// 归属不符=响应坏：宁可拒，不得让错配对静默产出 status=ok 的假溢价。
		return out, fmt.Errorf("F10 归属不符：请求 %s 响应首行 %s", symbol, row.Seccode)
	}
	out.AName = nameOf(row.StrNameA, row.Abbr)
	out.HName = strOf(row.StrNameH)
	if row.StrCodeH == nil || !strings.HasSuffix(*row.StrCodeH, ".HK") {
		// 三态合一判 no_pair：行无 / STR_CODEH null / 非 .HK 后缀
		// （2026-10-20 活源验收：000001.SZ = 行在场 STR_CODEH null）。
		return out, nil
	}
	h := strings.TrimSuffix(*row.StrCodeH, ".HK")
	if !isHKCodeDigits(h) {
		// F10 声称有配对但代码形态不可读——配对未知、宁可拒不可拼
		// （hCode 原样进腾讯查询 path，非数字串有 query 注入/串行面）。
		return out, fmt.Errorf("【配对畸形】STR_CODEH 非数字港股代码：%s", *row.StrCodeH)
	}
	out.Pair = true
	out.HCode = h
	return out, nil
}

// isHKCodeDigits：港股主板证券代码为 1–5 位数字（腾讯 r_hk 拼接前的最小可信形态；
// 6 位及以上/空/非数字一律视为源异常）。
func isHKCodeDigits(s string) bool {
	if len(s) == 0 || len(s) > 5 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func nameOf(a, abbr *string) string {
	if a != nil && *a != "" {
		return *a
	}
	return strOf(abbr)
}

func strOf(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

/* ---------- 腾讯三腿同批 ---------- */

// AHTriple 一次 GET 取 A 现价、H 现价、HKDCNY。腿级缺失走 Why 显式（绝不以 0 冒充），
// 请求级失败（非 2xx/超时/整体不可切分）上抛 error（biz 映射 source_error）。
func (q *AHQuote) AHTriple(ctx context.Context, aCode, aMarket, hCode string) (biz.AHTriple, error) {
	out := biz.AHTriple{}
	aq := strings.ToLower(aMarket) + aCode // 工程红线 a：H/FX 变量名保持原样、不过归一链
	rawURL := q.quoteURL + aq + ",r_hk" + hCode + ",whHKDCNY"
	body, err := q.do(ctx, rawURL, tencentReferer)
	if err != nil {
		return out, err
	}
	rows := splitTencentRows(string(q.gbkBytes(body)))
	aRow, hRow, fxRow := rows["v_"+aq], rows["v_r_hk"+hCode], rows["v_whHKDCNY"]
	if aRow == "" && hRow == "" && fxRow == "" {
		// 2xx 但三符号全缺/整体不可切分=源级故障（empty≠source_down 的 source_down 半）：
		// 腿级缺失才走 missing，整体都不到必须上抛 error（biz 映射 source_error），
		// 不得谎标 missing（对抗复核 must_fix）。
		return out, fmt.Errorf("腾讯响应三符号全缺/整体不可切分（响应 %d 字节）", len(body))
	}
	out.A = parseTencentALeg(aRow)
	out.H = parseTencentHKLeg(hRow)
	out.FX = parseTencentFXLeg(fxRow)
	out.ATraded = aTradedOf(aRow)
	// 时间解析失败不连带丢价，但不得静默——warn 落日志（T23）。
	for _, x := range []struct {
		name string
		leg  biz.AHLeg
	}{
		{"a_price", out.A}, {"h_price", out.H}, {"hkdcny", out.FX},
	} {
		if x.leg.Price != nil && x.leg.Time.IsZero() {
			q.log.Warnf("AH 报价腿时刻缺失（价照常回波）: a=%s%s h=%s leg=%s", aMarket, aCode, hCode, x.name)
		}
	}
	return out, nil
}

// gbkBytes 整身解码腾讯 GBK 响应；解码失败回退原始字节并 warn 落日志
// （不得静默——回退后切分多半失败，将由「三符号全缺」判 source_error，日志是唯一留痕）。
func (q *AHQuote) gbkBytes(b []byte) []byte {
	out, err := simplifiedchinese.GBK.NewDecoder().Bytes(b)
	if err != nil {
		q.log.Warnf("GBK 解码失败回退原始字节: %v", err)
		return b
	}
	return out
}

// splitTencentRows 按原生 var 名就地切分响应行：key=`v_xxx`，value=引号内 tilde 串。
// 解析不过 symbol.Parse/fromLower（工程红线 a）。
func splitTencentRows(s string) map[string]string {
	rows := map[string]string{}
	for _, chunk := range strings.Split(s, ";") {
		i := strings.Index(chunk, "v_")
		j := strings.IndexByte(chunk, '"')
		k := strings.LastIndexByte(chunk, '"')
		if i < 0 || j < 0 || k <= j {
			continue
		}
		name := strings.TrimSpace(chunk[i:])
		if eq := strings.IndexByte(name, '='); eq > 0 {
			name = strings.TrimSpace(name[:eq])
		}
		rows[name] = chunk[j+1 : k]
	}
	return rows
}

// tencentNumOrNil：空串=无数据；显式 "0"=无报价哨兵 na_value；不可解析/非有限/负值=parse。
// strconv.ParseFloat 会接受 "NaN"/"Inf" 字面量与负数——源价域必须有限且 >0，
// 否则非有限值会以 Why="" 穿透衍生列（复核实证裁决：protojson 对其序列化为 "NaN"
// 字符串返 200 不报错，坏值会以 status=ok 静默产出假溢价）。绝不以 0 冒充没取到的价。
func tencentNumOrNil(s string) (*float64, string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, biz.AHLegNoData
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return nil, biz.AHLegParse
	}
	if v == 0 {
		return nil, biz.AHLegNAValue
	}
	return &v, ""
}

var (
	locShanghai = func() *time.Location {
		l, err := time.LoadLocation("Asia/Shanghai")
		if err != nil {
			return time.FixedZone("CST", 8*3600)
		}
		return l
	}()
	locHK = func() *time.Location {
		l, err := time.LoadLocation("Asia/Hong_Kong")
		if err != nil {
			return time.FixedZone("HKT", 8*3600)
		}
		return l
	}()
)

// parseTencentTime 按腿定 layout 解析源内 tick 时刻；失败返回零值（价不连带丢）。
func parseTencentTime(s, layout string, loc *time.Location) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	t, err := time.ParseInLocation(layout, s, loc)
	if err != nil {
		return time.Time{}
	}
	return t
}

// tencentRowFields 切分在场的响应行（"~" 分隔）；行缺/空白 → present=false。
// 行在场但截断：截断点以下字段逐位保真（strings.Split 按位确定性），逐下标守护捞救——
// 拿不到价才落 parse（行级畸形），绝不把在场的价因尾部字段缺失连坐丢掉（取到的不丢，
// 对抗复核 should_fix：原 min 全行字段闸会把在场 f[3] 一起闸掉且错标 no_data）。
func tencentRowFields(row string) (f []string, present bool) {
	if strings.TrimSpace(row) == "" {
		return nil, false
	}
	return strings.Split(row, "~"), true
}

// A 腿：f[3]=现价（CNY），f[30]=紧凑时间（Asia/Shanghai）。行在场拿不到价 → Why=parse；
// 时刻下标不及/坏时刻 → 零值透出（价不连带丢，warn 由 AHTriple 统一落日志）。
func parseTencentALeg(row string) biz.AHLeg {
	f, present := tencentRowFields(row)
	if !present {
		return biz.AHLeg{Why: biz.AHLegNoData}
	}
	var leg biz.AHLeg
	if len(f) > 3 {
		leg.Price, leg.Why = tencentNumOrNil(f[3])
	} else {
		leg.Why = biz.AHLegParse
	}
	if len(f) > 30 {
		leg.Time = parseTencentTime(f[30], "20060102150405", locShanghai)
	}
	return leg
}

// H 腿：f[3]=现价（HKD），f[30]=斜杠布局 "2006/01/02 15:04:05"（Asia/Hong_Kong）——
// 2026-10-20 活源样本：2026/10/16 16:08:22；按 A layout 解析会静默丢 H 腿。
func parseTencentHKLeg(row string) biz.AHLeg {
	f, present := tencentRowFields(row)
	if !present {
		return biz.AHLeg{Why: biz.AHLegNoData}
	}
	var leg biz.AHLeg
	if len(f) > 3 {
		leg.Price, leg.Why = tencentNumOrNil(f[3])
	} else {
		leg.Why = biz.AHLegParse
	}
	if len(f) > 30 {
		leg.Time = parseTencentTime(f[30], "2006/01/02 15:04:05", locHK)
	}
	return leg
}

// FX 腿（whHKDCNY，layout 与 A/H 不同族）：f[3]=汇价，f[5]=紧凑时间（Asia/Shanghai）。
// 2026-10-20 活源样本：v_whHKDCNY="310~港元人民币~HKDCNY~0.8510~0~20261020065912~..."。
func parseTencentFXLeg(row string) biz.AHLeg {
	f, present := tencentRowFields(row)
	if !present {
		return biz.AHLeg{Why: biz.AHLegNoData}
	}
	var leg biz.AHLeg
	if len(f) > 3 {
		leg.Price, leg.Why = tencentNumOrNil(f[3])
	} else {
		leg.Why = biz.AHLegParse
	}
	if len(f) > 5 {
		leg.Time = parseTencentTime(f[5], "20060102150405", locShanghai)
	}
	return leg
}

// aTradedOf 在 A 行 f[6]（成交量手数）判定停牌/无成交腿；停牌腿价格照常回波。
// A 行缺失/截断不及 f[6] 同样 false——零值不承载停牌语义（停牌语义仅在 a_price
// 在场时成立，A 腿缺失走 status=missing，proto 注释已钉分工）。
func aTradedOf(row string) bool {
	f, present := tencentRowFields(row)
	if !present || len(f) <= 6 {
		return false
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(f[6]), 64)
	return err == nil && v > 0
}
