package data

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"server/app/datahub/internal/biz"
	"server/pkg/appcfg"
	"server/pkg/market"
	"server/pkg/symbol"
)

// 沪深京 A 股：深主板、创业板、沪主板、科创板、北交所。
const emAShares = "m:0+t:6,m:0+t:80,m:1+t:2,m:1+t:23,m:0+t:81+s:2048"

const (
	emClist = "https://push2.eastmoney.com/api/qt/clist/get"
	emPool  = "https://push2ex.eastmoney.com/"
	emDC    = "https://datacenter-web.eastmoney.com/api/data/v1/get"
	emPage  = 100
)

// Eastmoney 是东方财富网页公开接口，非官方，official=false。
// push2 短时间请求过密会封 IP（实测 3 秒约 190 次即被重置连接），QPS 默认 4。
// P3-9（_em_get 同款收口）：固定节奏仍会有同步连发，Limiter 令牌后加 [0, gap/2)
// 随机抖动打散；403/429（疑似限流/封禁）与 5xx、网络错误退避重试至多 2 次
// （基准 1s/2s，throttled 乘 4 即 4s/8s）；失败消息带行首「【疑似限流/封禁】」
// 标签，经 healthBook.record 入 source_health.last_error，数据源面板可见。长连接
// 复用由 httpc Transport（MaxIdleConnsPerHost、IdleConnTimeout 90s）承担。
type Eastmoney struct {
	hc *httpc
	// gubaWatch 返回股吧低频扫描清单（持仓∪近 3 日信号股），装配处注入（对齐 Sina/Tencent
	// 的 universe 注入先例）；nil 或空清单时 guba 返回 ErrUnsupported 计跳过不计失败。
	gubaWatch func(ctx context.Context) ([]string, error)
}

// SetGubaWatch 注入股吧观察清单（装配处一行设置，见 data/sources.go build()）。
func (e *Eastmoney) SetGubaWatch(fn func(ctx context.Context) ([]string, error)) { e.gubaWatch = fn }

func NewEastmoney(opt HTTPOptions) *Eastmoney {
	if opt.Concurrency <= 0 {
		opt.Concurrency = 4
	}
	if opt.QPS <= 0 {
		opt.QPS = 4
	}
	if opt.JitterGap <= 0 {
		opt.JitterGap = time.Duration(float64(time.Second) / opt.QPS / 2)
	}
	if opt.Retries <= 0 {
		opt.Retries = 2
	}
	return &Eastmoney{hc: newHTTPC(biz.SourceEastmoney, opt, map[string]string{
		"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36",
		"Referer":    "https://quote.eastmoney.com/",
	})}
}

func (e *Eastmoney) Name() string { return biz.SourceEastmoney }

// jv 兼容东财字段：数字、数字字符串、停牌时的 "-"。
type jv struct {
	n  float64
	ok bool
	s  string
}

func (v *jv) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	if b[0] == '"' {
		if err := json.Unmarshal(b, &v.s); err != nil {
			return err
		}
		if f, err := strconv.ParseFloat(v.s, 64); err == nil {
			v.n, v.ok = f, true
		}
		return nil
	}
	f, err := strconv.ParseFloat(string(b), 64)
	if err != nil {
		return nil
	}
	v.n, v.ok, v.s = f, true, string(b)
	return nil
}

func (v jv) ptr() *float64 {
	if !v.ok {
		return nil
	}
	f := v.n
	return &f
}

type emRow map[string]jv

func (r emRow) str(k string) string { return r[k].s }

// f 哨兵取值：解析失败 → 0。「没取到≠0」纪律下，只容许两类用途：
//  (a) 字段 0 与缺失同语义（消费方先判 >0，如 PreClose/封单额/连板数/两融余额——0 合法或 0=无）；
//  (b) 缺失行已被上游 has(k) 跳过的字段。
// 会把「缺失」与「真 0」混淆的关键字段（如资金流主字段）必须先 has() 判，缺失 skip 行。
func (r emRow) f(k string) float64 { return r[k].n }
func (r emRow) has(k string) bool  { return r[k].ok }

// emSymbol 由 f12 代码和 f13 市场得到内部代码。北交所 f13 为 0，按代码段区分。
func emSymbol(code string, mkt float64) string {
	if mkt == 1 {
		return code + ".SH"
	}
	s, err := symbol.FromAShareCode(code)
	if err != nil {
		return ""
	}
	return s.Tongdaxin()
}

// clist 分页并发拉取列表。每页最多 100 条。
func (e *Eastmoney) clist(ctx context.Context, fs, fields string) ([]emRow, error) {
	page := func(pn int) ([]emRow, int, error) {
		q := url.Values{
			"pn": {strconv.Itoa(pn)}, "pz": {strconv.Itoa(emPage)}, "po": {"1"}, "np": {"1"},
			"fltt": {"2"}, "invt": {"2"}, "fid": {"f12"}, "fs": {fs}, "fields": {fields},
		}
		var out struct {
			Data *struct {
				Total int     `json:"total"`
				Diff  []emRow `json:"diff"`
			} `json:"data"`
		}
		if err := e.hc.getJSON(ctx, emClist+"?"+q.Encode(), &out); err != nil {
			return nil, 0, err
		}
		if out.Data == nil {
			return nil, 0, nil
		}
		return out.Data.Diff, out.Data.Total, nil
	}
	first, total, err := page(1)
	if err != nil {
		return nil, err
	}
	pages := (total + emPage - 1) / emPage
	if pages <= 1 {
		return first, nil
	}
	parts := make([][]emRow, pages)
	parts[0] = first
	errs := make([]error, pages)
	var wg sync.WaitGroup
	for pn := 2; pn <= pages; pn++ {
		wg.Add(1)
		go func(pn int) {
			defer wg.Done()
			parts[pn-1], _, errs[pn-1] = page(pn)
		}(pn)
	}
	wg.Wait()
	rows := make([]emRow, 0, total)
	for i, p := range parts {
		if errs[i] != nil {
			return nil, errs[i]
		}
		rows = append(rows, p...)
	}
	return rows, nil
}

func (e *Eastmoney) Snapshots(ctx context.Context, symbols []string) (biz.SnapshotBatch, error) {
	if len(symbols) > 0 {
		return biz.SnapshotBatch{}, biz.ErrUnsupported
	}
	rows, err := e.clist(ctx, emAShares, "f2,f5,f6,f12,f13,f15,f16,f17,f18,f124")
	if err != nil {
		return biz.SnapshotBatch{}, err
	}
	items := make([]market.Snapshot, 0, len(rows))
	for _, r := range rows {
		sym := emSymbol(r.str("f12"), r.f("f13"))
		// 「没取到≠0」：f2 缺失或 0 价的快照行 skip——停牌/解析失败的 Last=0 一旦入库，
		// 下游涨跌幅按 (0-昨收)/昨收=-100% 会触发假跌停异动；停牌股 Redis 无行情强过一个伪价。
		// 其余价段（开高低/前收）走 0=缺失哨兵，pkg/market 消费侧先判 >0。
		if sym == "" || !r.has("f2") || r.f("f2") <= 0 || !r.has("f124") {
			continue
		}
		items = append(items, market.Snapshot{
			Symbol: sym, Time: time.Unix(int64(r.f("f124")), 0).In(shanghaiLoc()), Source: biz.SourceEastmoney,
			PreClose: r.f("f18"), Open: r.f("f17"), High: r.f("f15"), Low: r.f("f16"), Last: r.f("f2"),
			Volume: int64(r.f("f5")) * 100, Amount: r.f("f6"),
		})
	}
	return biz.SnapshotBatch{Items: items}, nil
}

// DailyBars 只能给出当日全市场日线：收盘后的全市场快照即当日日线。历史日期与个股区间不支持。
func (e *Eastmoney) DailyBars(ctx context.Context, q biz.BarQuery) (biz.BarBatch, error) {
	today := ymd(time.Now())
	if len(q.Symbols) > 0 || ymd(q.Start) != today || ymd(q.End) != today {
		return biz.BarBatch{}, biz.ErrUnsupported
	}
	snap, err := e.Snapshots(ctx, nil)
	if err != nil {
		return biz.BarBatch{}, err
	}
	d := day(time.Now())
	bars := make([]biz.Bar, 0, len(snap.Items))
	last := ""
	for _, s := range snap.Items {
		if ymd(s.Time) != today || s.Volume <= 0 {
			continue
		}
		if t := s.Time.In(shanghaiLoc()); t.Hour()*100+t.Minute() < 1500 {
			continue
		}
		pc := s.PreClose
		bars = append(bars, biz.Bar{Symbol: s.Symbol, Time: d, Freq: "1d", Open: s.Open, High: s.High, Low: s.Low,
			Close: s.Last, PreClose: &pc, Volume: s.Volume, Amount: s.Amount})
		last = today
	}
	return biz.BarBatch{Bars: bars, LastDate: last, Stale: last == "", Expect: ymd(q.Expect)}, nil
}

func (e *Eastmoney) Securities(ctx context.Context) ([]biz.Security, error) {
	rows, err := e.clist(ctx, emAShares, "f2,f12,f13,f14,f26,f38,f39")
	if err != nil {
		return nil, err
	}
	items := make([]biz.Security, 0, len(rows))
	for _, r := range rows {
		sym := emSymbol(r.str("f12"), r.f("f13"))
		if sym == "" {
			continue
		}
		s := biz.Security{Symbol: sym, Name: r.str("f14"), Market: sym[len(sym)-2:], ST: isST(r.str("f14")), Suspended: !r.has("f2")}
		if d := r.f("f26"); d > 19000000 {
			if t, err := time.ParseInLocation("20060102", strconv.Itoa(int(d)), shanghaiLoc()); err == nil {
				s.ListDate = &t
			}
		}
		if v := int64(r.f("f38")); v > 0 {
			s.TotalShare = &v
		}
		if v := int64(r.f("f39")); v > 0 {
			s.FloatShare = &v
		}
		items = append(items, s)
	}
	return items, nil
}

func (e *Eastmoney) flows(ctx context.Context) ([]biz.IntradayFlow, error) {
	rows, err := e.clist(ctx, emAShares, "f12,f13,f62,f66,f72,f78,f84,f124")
	if err != nil {
		return nil, err
	}
	now := time.Now()
	items := make([]biz.IntradayFlow, 0, len(rows))
	for _, r := range rows {
		sym := emSymbol(r.str("f12"), r.f("f13"))
		// 「没取到≠0」：主力净流入（f62）是该行的存在理由，缺失整行 skip；
		// 分项（超大/大/中/小）与主力的分解源一次取齐，缺省按 0=无该项流向处理（哨兵）。
		if sym == "" || !r.has("f62") {
			continue
		}
		items = append(items, biz.IntradayFlow{Symbol: sym, MainNet: r.f("f62"), Super: r.f("f66"), Big: r.f("f72"),
			Mid: r.f("f78"), Small: r.f("f84"), Time: time.Unix(int64(r.f("f124")), 0).In(shanghaiLoc()), AsOf: now,
			Source: biz.SourceEastmoney})
	}
	return items, nil
}

func (e *Eastmoney) IntradayFlows(ctx context.Context) ([]biz.IntradayFlow, error) {
	return e.flows(ctx)
}

// MoneyFlows 只能取当日：收盘后的实时资金即当日资金流向，历史日期不支持。
func (e *Eastmoney) MoneyFlows(ctx context.Context, d time.Time) ([]biz.MoneyFlow, error) {
	if ymd(d) != ymd(time.Now()) {
		return nil, biz.ErrUnsupported
	}
	flows, err := e.flows(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]biz.MoneyFlow, 0, len(flows))
	for _, f := range flows {
		if ymd(f.Time) != ymd(d) {
			continue
		}
		items = append(items, biz.MoneyFlow{Symbol: f.Symbol, MainNet: f.MainNet, SuperNet: f.Super, BigNet: f.Big, MidNet: f.Mid, SmallNet: f.Small})
	}
	return items, nil
}

var emBoards = []struct{ fs, kind string }{
	{"m:90+t:2+f:!50", biz.SectorIndustry},
	{"m:90+t:3+f:!50", biz.SectorConcept},
}

func (e *Eastmoney) SectorQuotes(ctx context.Context) ([]biz.SectorQuote, error) {
	now := time.Now()
	var items []biz.SectorQuote
	for _, b := range emBoards {
		rows, err := e.clist(ctx, b.fs, "f2,f3,f6,f12,f14,f104,f105,f124,f128,f140,f141")
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			if !r.has("f2") {
				continue
			}
			q := biz.SectorQuote{Code: r.str("f12"), Name: r.str("f14"), Last: r.f("f2"), PctChg: r.f("f3"), Amount: r.f("f6"),
				Up: int(r.f("f104")), Down: int(r.f("f105")), Source: biz.SourceEastmoney, AsOf: now}
			if code := r.str("f140"); code != "" {
				q.Leader = emSymbol(code, r.f("f141"))
			}
			if r.has("f124") {
				q.QuoteAt = time.Unix(int64(r.f("f124")), 0).In(shanghaiLoc())
			}
			items = append(items, q)
		}
	}
	return items, nil
}

func (e *Eastmoney) Sectors(ctx context.Context) ([]biz.Sector, error) {
	var boards []biz.Sector
	for _, b := range emBoards {
		rows, err := e.clist(ctx, b.fs, "f12,f14")
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			boards = append(boards, biz.Sector{Code: r.str("f12"), Name: r.str("f14"), Kind: b.kind})
		}
	}
	errs := make([]error, len(boards))
	var wg sync.WaitGroup
	for i := range boards {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rows, err := e.clist(ctx, "b:"+boards[i].Code+"+f:!50", "f12,f13")
			if err != nil {
				errs[i] = err
				return
			}
			for _, r := range rows {
				if sym := emSymbol(r.str("f12"), r.f("f13")); sym != "" {
					boards[i].Members = append(boards[i].Members, sym)
				}
			}
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			return nil, fmt.Errorf("board %s: %w", boards[i].Code, err)
		}
	}
	return boards, nil
}

func (e *Eastmoney) LimitPool(ctx context.Context, d time.Time) ([]biz.LimitEntry, error) {
	pools := []struct{ api, pool, sort string }{
		{"getTopicZTPool", biz.PoolUp, "fbt:asc"},
		{"getTopicDTPool", biz.PoolDown, "fund:asc"},
		{"getTopicZBPool", biz.PoolBroken, "fbt:asc"},
	}
	var all []biz.LimitEntry
	for _, p := range pools {
		q := url.Values{"ut": {"7eea3edcaed734bea9cbfc24409ed989"}, "dpt": {"wz.ztzt"}, "Pageindex": {"0"},
			"pagesize": {"10000"}, "sort": {p.sort}, "date": {d.In(shanghaiLoc()).Format("20060102")}}
		var out struct {
			Data *struct {
				Pool []map[string]jv `json:"pool"`
			} `json:"data"`
		}
		if err := e.hc.getJSON(ctx, emPool+p.api+"?"+q.Encode(), &out); err != nil {
			return nil, err
		}
		if out.Data == nil {
			continue
		}
		for _, r := range out.Data.Pool {
			row := emRow(r)
			sym := emSymbol(row.str("c"), row.f("m"))
			if sym == "" {
				continue
			}
			e := biz.LimitEntry{Symbol: sym, Pool: p.pool, Name: row.str("n"), PctChg: row["zdp"].ptr(), Amount: row["amount"].ptr(),
				OpenCount: int(row.f("zbc")), SealAmount: row.f("fund"), Consecutive: int(row.f("lbc")), Reason: row.str("hybk")}
			if row.has("p") {
				px := row.f("p") / 1000
				e.Close = &px
			}
			if p.pool == biz.PoolDown {
				e.SealAmount = row.f("fba")
				e.OpenCount = int(row.f("oc"))
				e.Consecutive = int(row.f("days"))
			}
			e.FirstSealAt = hhmmss(d, row["fbt"])
			e.LastSealAt = hhmmss(d, row["lbt"])
			all = append(all, e)
		}
	}
	return all, nil
}

func hhmmss(d time.Time, v jv) *time.Time {
	if !v.ok || v.n <= 0 {
		return nil
	}
	n := int(v.n)
	dd := d.In(shanghaiLoc())
	t := time.Date(dd.Year(), dd.Month(), dd.Day(), n/10000, n/100%100, n%100, 0, shanghaiLoc())
	return &t
}

// ReportGoneError 是 datacenter 报表失效/轮换专用错误：对端 HTTP 无恙，是报表目录面
// 变化（实测 code=9501「报表配置不存在」）。行首带「【报表轮换】」标签，沿用 httpc.go
// 「【疑似限流/封禁】」惯例，经 healthBook.record 截 err.Error() 落 source_health.last_error，
// 健康面板红显可辨。错误码白名单先窄后宽：只 9501 换名，新增码需在 dc 分支显式扩展。
type ReportGoneError struct {
	Report  string
	Code    int
	Message string
}

func (e *ReportGoneError) Error() string {
	return fmt.Sprintf("【报表轮换】eastmoney %s: %d %s", e.Report, e.Code, e.Message)
}

// dc 分页读东财数据中心。返回数据为空时源头报 9201，按空结果处理。
// 9501（实测「报表配置不存在」）折成 ReportGoneError 上抛，供采集回退链换名、探测红显。
func (e *Eastmoney) dc(ctx context.Context, report, filter, sortCol string) ([]map[string]any, error) {
	var all []map[string]any
	for pn := 1; pn <= 200; pn++ {
		q := url.Values{"reportName": {report}, "columns": {"ALL"}, "source": {"WEB"}, "client": {"WEB"},
			"pageSize": {"500"}, "pageNumber": {strconv.Itoa(pn)}, "filter": {filter}}
		if sortCol != "" {
			q.Set("sortColumns", sortCol)
			q.Set("sortTypes", "-1")
		}
		var out struct {
			Success bool   `json:"success"`
			Code    int    `json:"code"`
			Message string `json:"message"`
			Result  *struct {
				Pages int              `json:"pages"`
				Data  []map[string]any `json:"data"`
			} `json:"result"`
		}
		if err := e.hc.getJSON(ctx, emDC+"?"+q.Encode(), &out); err != nil {
			return nil, err
		}
		if out.Result == nil {
			if out.Code == 9501 {
				return nil, &ReportGoneError{Report: report, Code: out.Code, Message: out.Message}
			}
			if out.Code == 9201 || out.Success {
				return all, nil
			}
			return nil, fmt.Errorf("eastmoney %s: %d %s", report, out.Code, out.Message)
		}
		all = append(all, out.Result.Data...)
		if pn >= out.Result.Pages {
			break
		}
	}
	return all, nil
}

func anyStr(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return ""
}

func anyNum(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case string:
		f, err := strconv.ParseFloat(x, 64)
		return f, err == nil
	}
	return 0, false
}

// anyF 哨兵取值：解析失败 → 0。纪律同 emRow.f：仅用于 0 与缺失同语义、
// 或下游校验链（CheckBars/CheckSnapshots 非正拦截）会兜住伪 0 的字段；
// 情感分、资金流主字段这类 0 有真实含义的禁止用 anyF（用 anyNum/anyPtr）。
func anyF(v any) float64 { f, _ := anyNum(v); return f }

func anyPtr(v any) *float64 {
	f, ok := anyNum(v)
	if !ok {
		return nil
	}
	return &f
}

func anyIPtr(v any) *int64 {
	f, ok := anyNum(v)
	if !ok {
		return nil
	}
	i := int64(f)
	return &i
}

// secu 解析 600519.SH 形式的 SECUCODE，或 6 位代码。
func secu(v any) string {
	s := anyStr(v)
	if s == "" {
		return ""
	}
	if strings.Contains(s, ".") {
		if sym, err := symbol.Parse(s); err == nil {
			return sym.Tongdaxin()
		}
		return ""
	}
	sym, err := symbol.FromAShareCode(s)
	if err != nil {
		return ""
	}
	return sym.Tongdaxin()
}

func dcDate(v any) (time.Time, bool) {
	s := anyStr(v)
	if len(s) < 10 {
		return time.Time{}, false
	}
	t, err := time.ParseInLocation("2006-01-02", s[:10], shanghaiLoc())
	return t, err == nil
}

func (e *Eastmoney) LhbSeats(ctx context.Context, d time.Time) ([]biz.LhbSeat, error) {
	filter := fmt.Sprintf("(TRADE_DATE='%s')", ymd(d))
	var items []biz.LhbSeat
	for _, rpt := range []string{"RPT_BILLBOARD_DAILYDETAILSBUY", "RPT_BILLBOARD_DAILYDETAILSSELL"} {
		rows, err := e.dc(ctx, rpt, filter, "")
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			sym := secu(r["SECUCODE"])
			seat := anyStr(r["OPERATEDEPT_NAME"])
			if sym == "" || seat == "" {
				continue
			}
			items = append(items, biz.LhbSeat{Symbol: sym, Reason: anyStr(r["EXPLANATION"]), SeatName: seat,
				BuyAmount: anyF(r["BUY"]), SellAmount: anyF(r["SELL"])})
		}
	}
	// 买榜和卖榜会重复列出同一席位，金额相同，只保留一份。
	type key struct{ sym, reason, seat string }
	seen := map[key]bool{}
	out := items[:0]
	for _, it := range items {
		k := key{it.Symbol, it.Reason, it.SeatName}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, it)
	}
	return out, nil
}

func (e *Eastmoney) UnlockPlans(ctx context.Context, from, to time.Time) ([]biz.UnlockPlan, error) {
	// RPT_LIFT_STAGE 是全市场解禁批次报表，按 FREE_DATE 窗口过滤；窗口内空是合法结果。
	filter := fmt.Sprintf("(FREE_DATE>='%s')(FREE_DATE<='%s')", ymd(from), ymd(to))
	rows, err := e.dc(ctx, "RPT_LIFT_STAGE", filter, "FREE_DATE")
	if err != nil {
		return nil, err
	}
	items := make([]biz.UnlockPlan, 0, len(rows))
	for _, r := range rows {
		sym := secu(r["SECURITY_CODE"])
		freeDate, ok := dcDate(r["FREE_DATE"])
		if sym == "" || !ok {
			continue
		}
		p := biz.UnlockPlan{
			Symbol:        sym,
			FreeDate:      freeDate,
			LiftType:      truncate(anyStr(r["FREE_SHARES_TYPE"]), 64),
			Shares:        anyF(r["ABLE_FREE_SHARES"]), // 万股
			RatioPct:      anyPtr(r["FREE_RATIO"]),
			TotalRatioPct: anyPtr(r["TOTAL_RATIO"]),
		}
		if v, ok := anyNum(r["BATCH_HOLDER_NUM"]); ok {
			n := int(v)
			p.BatchHolderNum = &n
		}
		items = append(items, p)
	}
	return items, nil
}

// emMarginReportDefault 两融个股明细默认报表名。事实钉死：2026-10-19 实测有效；
// 汇总旧表 RPTA_WEB_RZRQ_LSHJ 同日实测 9501 已下线，不入默认链。
const emMarginReportDefault = "RPTA_WEB_RZRQ_GGMX"

// emMarginReportsKey 两融报表名回退链的运行时键（strategy_config 下发、appcfg 5s 缓存）。
// 逗号分隔、首个为主；误填全灭名单时采集+探测双双红显可自纠，ValidatePatch 有意不验名。
const emMarginReportsKey = "app.ds.eastmoney.margin_reports"

// parseReportChain 解析逗号分隔的报表名链：trim、去重、去空；空串返回 nil（调用方回退默认）。
func parseReportChain(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.Split(s, ",") {
		name := strings.TrimSpace(part)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// marginReports 返回两融明细报表名回退链：运行时键优先，空值回退默认。
// 每次采集读取（appcfg 5s 缓存内复用）；探测的 GGMX 条目不读该键——探测测的是
// 默认链实际在跑的名，与采集同源同生命周期，避免键改了探测还在测旧名。
func (e *Eastmoney) marginReports(ctx context.Context) []string {
	if names := parseReportChain(appcfg.Get(ctx, emMarginReportsKey)); len(names) > 0 {
		return names
	}
	return []string{emMarginReportDefault}
}

func (e *Eastmoney) Margins(ctx context.Context, d time.Time) ([]biz.Margin, error) {
	// 回退链：仅 ReportGoneError（9501 报表轮换）换下一个名；限流/网络/校验错误
	// 立即返回不换名，避免把「在的请求失败」误当轮换导致雪崩式探名。
	var firstGone *ReportGoneError
	var rows []map[string]any
	var err error
	for _, report := range e.marginReports(ctx) {
		rows, err = e.dc(ctx, report, fmt.Sprintf("(DATE='%s')", ymd(d)), "")
		if err == nil {
			break
		}
		var gone *ReportGoneError
		if errors.As(err, &gone) {
			if firstGone == nil {
				firstGone = gone
			}
			continue
		}
		return nil, err
	}
	if err != nil {
		// 全链皆灭：返回首个轮换错误（行首【报表轮换】标签入健康面板），tushare 备源按 Run 链接管。
		return nil, firstGone
	}
	items := make([]biz.Margin, 0, len(rows))
	for _, r := range rows {
		sym := secu(r["SCODE"])
		if sym == "" {
			continue
		}
		items = append(items, biz.Margin{Symbol: sym, Rzye: anyF(r["RZYE"]), Rzmre: anyF(r["RZMRE"]), Rzche: anyF(r["RZCHE"]),
			Rqye: anyF(r["RQYE"]), Rqmcl: int64(anyF(r["RQMCL"])), Rzrqye: anyF(r["RZRQYE"])})
	}
	return items, nil
}

func (e *Eastmoney) HsgtTop10(ctx context.Context, d time.Time) ([]biz.HsgtTop, error) {
	rows, err := e.dc(ctx, "RPT_MUTUAL_TOP10DEAL", fmt.Sprintf(`(TRADE_DATE='%s')(MUTUAL_TYPE in ("001","003"))`, ymd(d)), "")
	if err != nil {
		return nil, err
	}
	items := make([]biz.HsgtTop, 0, len(rows))
	for _, r := range rows {
		sym := secu(r["SECURITY_CODE"])
		if sym == "" {
			continue
		}
		ch := "SH"
		if anyStr(r["MUTUAL_TYPE"]) == "003" {
			ch = "SZ"
		}
		items = append(items, biz.HsgtTop{Symbol: sym, Channel: ch, Rank: int(anyF(r["RANK"])), Name: anyStr(r["SECURITY_NAME"]),
			Close: anyPtr(r["CLOSE_PRICE"]), PctChg: anyPtr(r["CHANGE_RATE"]), Amount: anyF(r["DEAL_AMT"]), NetAmount: anyPtr(r["NET_BUY_AMT"])})
	}
	return items, nil
}

// Finance 拉公告日不早于 since 的业绩预告和快报。
func (e *Eastmoney) Finance(ctx context.Context, since time.Time) ([]biz.FinanceItem, error) {
	filter := fmt.Sprintf("(NOTICE_DATE>='%s')", ymd(since))
	var items []biz.FinanceItem
	rows, err := e.dc(ctx, "RPT_PUBLIC_OP_NEWPREDICT", filter, "NOTICE_DATE")
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		sym := secu(r["SECUCODE"])
		end, ok1 := dcDate(r["REPORT_DATE"])
		ann, ok2 := dcDate(r["NOTICE_DATE"])
		if sym == "" || !ok1 || !ok2 {
			continue
		}
		// 一份预告按指标拆成多行，只保留归母净利润那一行的数值。
		it := biz.FinanceItem{Symbol: sym, EndDate: end, Kind: biz.FinanceForecast, AnnDate: ann,
			ForecastType: anyStr(r["PREDICT_TYPE"]), Summary: anyStr(r["PREDICT_CONTENT"]),
			Data: map[string]any{"finance": r["PREDICT_FINANCE"], "amp_lower": r["ADD_AMP_LOWER"], "amp_upper": r["ADD_AMP_UPPER"],
				"forecast_jz": r["FORECAST_JZ"], "increase_jz": r["INCREASE_JZ"]}}
		if strings.Contains(anyStr(r["PREDICT_FINANCE"]), "归属") {
			it.NetProfit = anyPtr(r["PREDICT_AMT_UPPER"])
			it.NetProfitYoY = anyPtr(r["ADD_AMP_UPPER"])
		} else {
			continue
		}
		items = append(items, it)
	}
	rows, err = e.dc(ctx, "RPT_FCI_PERFORMANCEE", filter, "NOTICE_DATE")
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		sym := secu(r["SECUCODE"])
		end, ok1 := dcDate(r["REPORT_DATE"])
		ann, ok2 := dcDate(r["NOTICE_DATE"])
		if sym == "" || !ok1 || !ok2 {
			continue
		}
		items = append(items, biz.FinanceItem{Symbol: sym, EndDate: end, Kind: biz.FinanceExpress, AnnDate: ann,
			NetProfit: anyPtr(r["PARENT_NETPROFIT"]), NetProfitYoY: anyPtr(r["JLRTBZCL"]),
			Data: map[string]any{"revenue": r["TOTAL_OPERATE_INCOME"], "eps": r["BASIC_EPS"], "roe": r["WEIGHTAVG_ROE"]}})
	}
	return items, nil
}

func (e *Eastmoney) HotRank(ctx context.Context) ([]biz.HotItem, error) {
	var out struct {
		Data []struct {
			Sc string `json:"sc"`
			Rk int    `json:"rk"`
		} `json:"data"`
	}
	body := map[string]any{"appId": "appId01", "globalId": "786e4c21-70dc-435a-93bb-38", "marketType": "", "pageNo": 1, "pageSize": 100}
	b, err := e.hc.postJSON(ctx, "https://emappdata.eastmoney.com/stockrank/getAllCurrentList", body)
	if err != nil {
		return nil, err
	}
	if err := decodeJSON(biz.SourceEastmoney, b, &out); err != nil {
		return nil, err
	}
	items := make([]biz.HotItem, 0, len(out.Data))
	for _, x := range out.Data {
		sym, err := symbol.Parse(x.Sc)
		if err != nil {
			continue
		}
		items = append(items, biz.HotItem{Board: "eastmoney_popularity", Rank: x.Rk, Symbol: sym.Tongdaxin()})
	}
	return items, nil
}

func (e *Eastmoney) Intel(ctx context.Context, kind string, q biz.IntelQuery) ([]biz.Intel, error) {
	switch kind {
	case biz.KindFlash, biz.KindNews:
		return e.flash(ctx, q)
	case biz.KindAnnouncement:
		return e.announcements(ctx, q)
	case biz.KindReport:
		return e.reports(ctx, q)
	case biz.KindSocial:
		return e.guba(ctx, q)
	}
	return nil, biz.ErrUnsupported
}

// shTime 把上海时间字符串转成 RFC3339。
func shTime(s string) string {
	s = strings.TrimSpace(s)
	// 公告的 display_time 形如 2026-10-08 22:03:34:367，毫秒前是冒号。
	if len(s) == 23 && s[19] == ':' {
		s = s[:19] + "." + s[20:]
	}
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04:05.000", "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, strings.TrimSpace(s), shanghaiLoc()); err == nil {
			return t.Format(time.RFC3339)
		}
	}
	return ""
}

func (e *Eastmoney) flash(ctx context.Context, q biz.IntelQuery) ([]biz.Intel, error) {
	size := q.Limit
	if size <= 0 || size > 200 {
		size = 200
	}
	u := "https://np-weblist.eastmoney.com/comm/web/getFastNewsList?" + url.Values{"client": {"web"}, "biz": {"web_724"},
		"fastColumn": {"102"}, "sortEnd": {""}, "pageSize": {strconv.Itoa(size)}, "req_trace": {strconv.FormatInt(time.Now().UnixMilli(), 10)}}.Encode()
	var out struct {
		Data *struct {
			List []struct {
				Code      string   `json:"code"`
				Title     string   `json:"title"`
				Summary   string   `json:"summary"`
				ShowTime  string   `json:"showTime"`
				StockList []string `json:"stockList"`
			} `json:"fastNewsList"`
		} `json:"data"`
	}
	if err := e.hc.getJSON(ctx, u, &out); err != nil {
		return nil, err
	}
	if out.Data == nil {
		return nil, fmt.Errorf("eastmoney flash: empty data")
	}
	items := make([]biz.Intel, 0, len(out.Data.List))
	for _, x := range out.Data.List {
		title := x.Title
		if title == "" {
			title = truncate(x.Summary, 60)
		}
		items = append(items, biz.Intel{Source: biz.SourceEastmoney, SourceID: x.Code, Kind: biz.KindFlash, Title: title,
			Content: x.Summary, URL: "https://finance.eastmoney.com/a/" + x.Code + ".html", PublishTime: shTime(x.ShowTime),
			Codes: emStockList(x.StockList)})
	}
	return items, nil
}

// emStockList 解析快讯关联股票，形如 "1.600519"、"0.000001"，非 A 股丢弃。
func emStockList(list []string) []string {
	var out []string
	for _, s := range list {
		i := strings.IndexByte(s, '.')
		if i <= 0 {
			continue
		}
		mkt, code := s[:i], s[i+1:]
		if mkt != "0" && mkt != "1" {
			continue
		}
		m, _ := strconv.ParseFloat(mkt, 64)
		if sym := emSymbol(code, m); sym != "" {
			out = append(out, sym)
		}
	}
	return out
}

func (e *Eastmoney) announcements(ctx context.Context, q biz.IntelQuery) ([]biz.Intel, error) {
	var items []biz.Intel
	for page := 1; page <= 10; page++ {
		u := "https://np-anotice-stock.eastmoney.com/api/security/ann?" + url.Values{"sr": {"-1"}, "page_size": {"100"},
			"page_index": {strconv.Itoa(page)}, "ann_type": {"A"}, "client_source": {"web"}, "f_node": {"0"}, "s_node": {"0"}}.Encode()
		var out struct {
			Data *struct {
				List []struct {
					ArtCode string `json:"art_code"`
					Codes   []struct {
						StockCode string `json:"stock_code"`
					} `json:"codes"`
					Title       string `json:"title"`
					NoticeDate  string `json:"notice_date"`
					DisplayTime string `json:"display_time"`
				} `json:"list"`
			} `json:"data"`
		}
		if err := e.hc.getJSON(ctx, u, &out); err != nil {
			return nil, err
		}
		if out.Data == nil || len(out.Data.List) == 0 {
			break
		}
		older := false
		for _, x := range out.Data.List {
			pt := shTime(x.DisplayTime)
			if pt == "" {
				pt = shTime(x.NoticeDate)
			}
			var codes []string
			first := ""
			for _, c := range x.Codes {
				if sym := secu(c.StockCode); sym != "" {
					codes = append(codes, sym)
					if first == "" {
						first = c.StockCode
					}
				}
			}
			items = append(items, biz.Intel{Source: biz.SourceEastmoney, SourceID: x.ArtCode, Kind: biz.KindAnnouncement, Title: x.Title,
				URL: "https://data.eastmoney.com/notices/detail/" + first + "/" + x.ArtCode + ".html", PublishTime: pt, Codes: codes})
			if t, err := time.Parse(time.RFC3339, pt); err == nil && t.Before(q.Since) {
				older = true
			}
		}
		if older {
			break
		}
	}
	return items, nil
}

// parseReportNum 把东财字符串数字解析为指针：空串=未披露→nil（「没取到≠0」纪律）。
func parseReportNum(s string) *float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	return &f
}

// intPtrOf 透传东财整型字段（ratingChange/reportType），非数或缺失留 nil。
func intPtrOf(v any) *int {
	f, ok := anyNum(v)
	if !ok {
		return nil
	}
	i := int(f)
	return &i
}

// reportContent 把研报预测组一条可读内容：「东吴证券 增持（前次 增持）｜2026E EPS 0.64 / PE 20.18｜目标价 12.50–14.00 元」。
// EPS/PE 不带「元/%」单位词，避免 intel ExtractFacts 误抽；目标价带「元」会被抽成
// amount fact，语义可接受（M03 文档钉死）。
func reportContent(org string, fc *biz.ReportForecast) string {
	head := org
	if fc.Rating != "" {
		head = strings.TrimSpace(head + " " + fc.Rating)
		if fc.LastRating != "" && fc.LastRating != fc.Rating {
			head += "（前次 " + fc.LastRating + "）"
		}
	}
	var parts []string
	for _, p := range fc.EPS {
		seg := fmt.Sprintf("%dE", p.Year)
		if p.EPS != nil {
			seg += fmt.Sprintf(" EPS %.2f", *p.EPS)
		}
		if p.PE != nil {
			seg += fmt.Sprintf(" / PE %.2f", *p.PE)
		}
		parts = append(parts, seg)
	}
	aim := ""
	if fc.AimPriceLow != nil && fc.AimPriceHigh != nil {
		aim = fmt.Sprintf("目标价 %.2f–%.2f 元", *fc.AimPriceLow, *fc.AimPriceHigh)
	} else if fc.AimPriceHigh != nil {
		aim = fmt.Sprintf("目标价 %.2f 元", *fc.AimPriceHigh)
	} else if fc.AimPriceLow != nil {
		aim = fmt.Sprintf("目标价 %.2f 元", *fc.AimPriceLow)
	}
	if aim != "" {
		parts = append(parts, aim)
	}
	if len(parts) == 0 {
		return head
	}
	if head == "" {
		return strings.Join(parts, "｜")
	}
	return head + "｜" + strings.Join(parts, "｜")
}

func (e *Eastmoney) reports(ctx context.Context, q biz.IntelQuery) ([]biz.Intel, error) {
	begin := q.Since
	if begin.IsZero() {
		begin = time.Now().AddDate(0, 0, -1)
	}
	var items []biz.Intel
	for page := 1; page <= 10; page++ {
		u := "https://reportapi.eastmoney.com/report/list?" + url.Values{"industryCode": {"*"}, "pageSize": {"100"}, "industry": {"*"},
			"rating": {"*"}, "ratingChange": {"*"}, "beginTime": {ymd(begin)}, "endTime": {ymd(time.Now())},
			"pageNo": {strconv.Itoa(page)}, "fields": {""}, "qType": {"0"}, "orgCode": {""}, "code": {"*"}, "rcode": {""}}.Encode()
		var out struct {
			Data []struct {
				Title        string `json:"title"`
				StockCode    string `json:"stockCode"`
				OrgSName     string `json:"orgSName"`
				PublishDate  string `json:"publishDate"`
				InfoCode     string `json:"infoCode"`
				EmRatingName string `json:"emRatingName"`
				// W6 预测字段（2026-10-19 当日响应实测拿到字段名）：全部字符串，空串=未披露。
				Researcher            string `json:"researcher"`
				IndvInduName          string `json:"indvInduName"`
				OrgCode               string `json:"orgCode"`
				OrgName               string `json:"orgName"`
				PredictThisYearEps    string `json:"predictThisYearEps"`
				PredictThisYearPe     string `json:"predictThisYearPe"`
				PredictNextYearEps    string `json:"predictNextYearEps"`
				PredictNextYearPe     string `json:"predictNextYearPe"`
				PredictNextTwoYearEps string `json:"predictNextTwoYearEps"`
				PredictNextTwoYearPe  string `json:"predictNextTwoYearPe"`
				IndvAimPriceT         string `json:"indvAimPriceT"` // 目标价上限
				IndvAimPriceL         string `json:"indvAimPriceL"` // 目标价下限
				LastEmRatingName      string `json:"lastEmRatingName"`
				RatingChange          any    `json:"ratingChange"`
				ReportType            any    `json:"reportType"`
			} `json:"data"`
			TotalPage   int `json:"TotalPage"`
			CurrentYear int `json:"currentYear"` // 响应级年度映射兜底锚（主锚=item 级 publishDate 年份，见下方 #7）
		}
		if err := e.hc.getJSON(ctx, u, &out); err != nil {
			return nil, err
		}
		for _, x := range out.Data {
			var codes []string
			if sym := secu(x.StockCode); sym != "" {
				codes = []string{sym}
			}
			it := biz.Intel{Source: biz.SourceEastmoney, SourceID: x.InfoCode, Kind: biz.KindReport, Title: x.Title,
				URL:         "https://data.eastmoney.com/report/info/" + x.InfoCode + ".html",
				PublishTime: shTime(x.PublishDate), Codes: codes}
			fc := &biz.ReportForecast{
				AimPriceLow: parseReportNum(x.IndvAimPriceL), AimPriceHigh: parseReportNum(x.IndvAimPriceT),
				Rating: x.EmRatingName, LastRating: x.LastEmRatingName,
				RatingChange: intPtrOf(x.RatingChange), ReportType: intPtrOf(x.ReportType),
				Researcher: x.Researcher, Industry: x.IndvInduName, OrgCode: x.OrgCode, OrgName: x.OrgName,
				OrgSName: x.OrgSName,
			}
			// 年度映射：以 item 级 publishDate 年份为锚——PIT＝研报成文时刻视角，「今年/明年/
			// 后年」按发布年展开、不随采集时刻漂移。旧锚序（响应级 currentYear 优先）在跨年
			// 窗口（12/31 发布的研报被 24h+2min 水位回看在次年 1 月初重采）使 currentYear
			// （=采集时刻年）漂移 +1：同一 info_code 三年 est_year 整体错 +1——「今年」列
			// 静默覆盖相邻年正确行、多幽灵 max+1 行、残留孤儿 min 行（对抗复核 #7，对等
			// 裁决锚序反转）。publishDate 缺失/解析失败时降级 currentYear；都拿不到丢整段 EPS。
			year := 0
			if t, err := time.Parse(time.RFC3339, it.PublishTime); err == nil {
				year = t.Year()
			}
			if year <= 0 {
				year = out.CurrentYear
			}
			if year > 0 {
				for i, p := range [][2]string{
					{x.PredictThisYearEps, x.PredictThisYearPe},
					{x.PredictNextYearEps, x.PredictNextYearPe},
					{x.PredictNextTwoYearEps, x.PredictNextTwoYearPe},
				} {
					eps, pe := parseReportNum(p[0]), parseReportNum(p[1])
					if eps == nil && pe == nil {
						continue // 双 nil 的年度点整条丢弃——没取到≠0
					}
					fc.EPS = append(fc.EPS, biz.EPSPoint{Year: year + i, EPS: eps, PE: pe})
				}
			}
			if len(fc.EPS) > 0 || fc.AimPriceLow != nil || fc.AimPriceHigh != nil || fc.Rating != "" || fc.LastRating != "" {
				it.Forecast = fc
				it.Content = reportContent(x.OrgSName, fc)
			} else {
				// 全部预测缺失：content 维持旧格式（机构+评级）。
				it.Content = strings.TrimSpace(x.OrgSName + " " + x.EmRatingName)
			}
			items = append(items, it)
		}
		if page >= out.TotalPage {
			break
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].PublishTime > items[j].PublishTime })
	return items, nil
}

// BlockTrades 拉某个交易日的大宗交易明细（RPT_DATA_BLOCKTRADE，盘后披露）。
// 当日一笔都没有是合法结果；解析哨兵沿用 secu/dcDate/anyF/anyPtr 公约，
// 缺列留 nil——没取到≠0。实际列名以首日 columns=ALL 实测为准，字段漂移照哨兵留 nil。
func (e *Eastmoney) BlockTrades(ctx context.Context, day time.Time) ([]biz.BlockTrade, error) {
	rows, err := e.dc(ctx, "RPT_DATA_BLOCKTRADE", fmt.Sprintf("(TRADE_DATE='%s')", ymd(day)), "TRADE_DATE")
	if err != nil {
		return nil, err
	}
	items := make([]biz.BlockTrade, 0, len(rows))
	for _, r := range rows {
		sym := secu(r["SECUCODE"])
		tradeDate, ok := dcDate(r["TRADE_DATE"])
		if sym == "" || !ok {
			continue // 证券代码空行、交易日解析失败行 skip
		}
		items = append(items, biz.BlockTrade{
			Symbol:       sym,
			TradeDate:    tradeDate,
			TradePrice:   anyF(r["TRADE_PRICE"]),
			Volume:       anyF(r["VOLUME"]),         // 万股，与源头一致
			Amount:       anyF(r["TURNOVER"]) * 1e4, // 源头万元 ×1e4 归一为元
			Buyer:        truncate(anyStr(r["BUYER_NAME"]), 128),
			Seller:       truncate(anyStr(r["SELLER_NAME"]), 128),
			PremiumRatio: anyPtr(r["PREMIUM_RATIO"]),
			ClosePrice:   anyPtr(r["CLOSE_PRICE"]),
		})
	}
	return items, nil
}

// gubaInterval 股吧每股请求间隔：gbapi 高频会触发验证码封禁，每股间强制 ≥2s。
// 测试可调 0 免真睡（同 httpc.backoff 先例）。
var gubaInterval = 2 * time.Second

// guba 低频拉观察清单内各股的股吧主贴（gbapi 一页 30 条，sorttype=1 按最新）。
// 实测 sorttype=2 时 code 过滤失效会混入跨吧帖，故即使过滤生效也保留 stockbar_code
// 客户端校验作双保险；无发布时间行 skip（没取到≠0，不冒充）。
func (e *Eastmoney) guba(ctx context.Context, _ biz.IntelQuery) ([]biz.Intel, error) {
	if e.gubaWatch == nil {
		return nil, biz.ErrUnsupported
	}
	watch, err := e.gubaWatch(ctx)
	if err != nil {
		return nil, err
	}
	if len(watch) == 0 {
		return nil, biz.ErrUnsupported
	}
	var items []biz.Intel
	for i, sym := range watch {
		if i > 0 {
			// ctx 感知间隔睡眠：P3-9 节流批落地后换回 httpc.sleepCtx 单点调用（挂账第 8 条）。
			if err := sleepCtx(ctx, gubaInterval); err != nil {
				return items, err
			}
		}
		code := strings.TrimSuffix(strings.ToUpper(sym), ".SH")
		code = strings.TrimSuffix(code, ".SZ")
		code = strings.TrimSuffix(code, ".BJ")
		if len(code) != 6 {
			continue
		}
		u := "https://gbapi.eastmoney.com/webarticlelist/api/Article/Articlelist?" + url.Values{
			"code": {code}, "ps": {"30"}, "pageindex": {"1"}, "sorttype": {"1"},
		}.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Referer", "https://guba.eastmoney.com/list,"+code+".html")
		body, err := e.hc.do(ctx, req)
		if err != nil {
			return nil, err
		}
		var out struct {
			Rc int `json:"rc"`
			Re []struct {
				PostID       string `json:"post_id"`
				Title        string `json:"post_title"`
				ClickCount   int64  `json:"post_click_count"`
				CommentCount int64  `json:"post_comment_count"`
				PublishTime  string `json:"post_publish_time"`
				StockbarCode string `json:"stockbar_code"`
			} `json:"re"`
		}
		// 验证码/HTML 页走 decodeJSON 解析失败计入错误路径（断路器与【疑似限流/封禁】链条观测）。
		if err := decodeJSON(biz.SourceEastmoney, body, &out); err != nil {
			return nil, err
		}
		if out.Rc != 0 {
			return nil, fmt.Errorf("eastmoney guba %s: rc=%d", code, out.Rc)
		}
		for _, p := range out.Re {
			if p.StockbarCode != "" && p.StockbarCode != code {
				continue // 跨吧帖防守
			}
			if p.PostID == "" || p.Title == "" {
				continue
			}
			pt := shTime(p.PublishTime)
			if pt == "" {
				continue // 无发布时间行 skip，不冒充
			}
			items = append(items, biz.Intel{
				Source: biz.SourceEastmoney, SourceID: p.PostID, Kind: biz.KindSocial,
				Title:       p.Title,
				Content:     fmt.Sprintf("%s｜点击%d｜评论%d", p.Title, p.ClickCount, p.CommentCount),
				URL:         fmt.Sprintf("https://guba.eastmoney.com/news,%s,%s.html", code, p.PostID),
				PublishTime: pt,
				Codes:       []string{sym},
			})
		}
	}
	return items, nil
}

// emDCProbe 一张在用 datacenter 报表的探测定义：单页 pageSize=1 请求，轮换错误
// 或（预期非空而 0 行）记为失败。7 张报表约 2-3 秒（QPS4+抖动）。
type emDCProbe struct {
	report     string
	domain     string
	filter     func(day time.Time) string
	allowEmpty bool // 窗口内合法为空的报表（解禁窗口、近 7 日预告）
}

// emDCProbes 探测的是「默认链实际在跑的名」，与采集同源同生命周期：GGMX 条目不读
// app.ds.eastmoney.margin_reports 运行时键（避免键改了探测还在测旧名）。
var emDCProbes = []emDCProbe{
	{report: emMarginReportDefault, domain: biz.DomainMargin,
		filter: func(day time.Time) string { return fmt.Sprintf("(DATE='%s')", ymd(day)) }},
	{report: "RPT_LIFT_STAGE", domain: biz.DomainUnlock, allowEmpty: true,
		filter: func(day time.Time) string {
			return fmt.Sprintf("(FREE_DATE>='%s')(FREE_DATE<='%s')", ymd(day), ymd(day.AddDate(0, 0, 90)))
		}},
	{report: "RPT_MUTUAL_TOP10DEAL", domain: biz.DomainHsgtTop10,
		filter: func(day time.Time) string {
			return fmt.Sprintf(`(TRADE_DATE='%s')(MUTUAL_TYPE in ("001","003"))`, ymd(day))
		}},
	{report: "RPT_PUBLIC_OP_NEWPREDICT", domain: biz.DomainFinance, allowEmpty: true,
		filter: func(day time.Time) string { return fmt.Sprintf("(NOTICE_DATE>='%s')", ymd(day.AddDate(0, 0, -7))) }},
	{report: "RPT_FCI_PERFORMANCEE", domain: biz.DomainFinance, allowEmpty: true,
		filter: func(day time.Time) string { return fmt.Sprintf("(NOTICE_DATE>='%s')", ymd(day.AddDate(0, 0, -7))) }},
	{report: "RPT_BILLBOARD_DAILYDETAILSBUY", domain: biz.DomainLhb,
		filter: func(day time.Time) string { return fmt.Sprintf("(TRADE_DATE='%s')", ymd(day)) }},
	{report: "RPT_BILLBOARD_DAILYDETAILSSELL", domain: biz.DomainLhb,
		filter: func(day time.Time) string { return fmt.Sprintf("(TRADE_DATE='%s')", ymd(day)) }},
}

// ProbeReports 实现 biz.ReportProber：逐张报表一次 pageSize=1 单页请求（走既有 dc 解析，
// 不翻页），轮换错误或预期非空而 0 行记入 ProbeResult.Err。
func (e *Eastmoney) ProbeReports(ctx context.Context, day time.Time) []biz.ProbeResult {
	out := make([]biz.ProbeResult, 0, len(emDCProbes))
	for _, p := range emDCProbes {
		res := biz.ProbeResult{Domain: p.domain, Report: p.report}
		rows, err := e.dcPage(ctx, p.report, p.filter(day))
		if err != nil {
			res.Err = err
		} else {
			res.Rows = len(rows)
			if len(rows) == 0 && !p.allowEmpty {
				res.Err = fmt.Errorf("报表 %s 预期非空而 0 行", p.report)
			}
		}
		out = append(out, res)
	}
	return out
}

// dcPage 只取报表首页一行（探测用），复用 dc 的响应解析与 9501/9201 语义。
func (e *Eastmoney) dcPage(ctx context.Context, report, filter string) ([]map[string]any, error) {
	q := url.Values{"reportName": {report}, "columns": {"ALL"}, "source": {"WEB"}, "client": {"WEB"},
		"pageSize": {"1"}, "pageNumber": {"1"}, "filter": {filter}}
	var out struct {
		Success bool   `json:"success"`
		Code    int    `json:"code"`
		Message string `json:"message"`
		Result  *struct {
			Pages int              `json:"pages"`
			Data  []map[string]any `json:"data"`
		} `json:"result"`
	}
	if err := e.hc.getJSON(ctx, emDC+"?"+q.Encode(), &out); err != nil {
		return nil, err
	}
	if out.Result == nil {
		if out.Code == 9501 {
			return nil, &ReportGoneError{Report: report, Code: out.Code, Message: out.Message}
		}
		if out.Code == 9201 || out.Success {
			return nil, nil
		}
		return nil, fmt.Errorf("eastmoney %s: %d %s", report, out.Code, out.Message)
	}
	return out.Result.Data, nil
}
