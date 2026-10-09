package data

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"server/app/datahub/internal/biz"
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
type Eastmoney struct {
	hc *httpc
}

func NewEastmoney(opt HTTPOptions) *Eastmoney {
	if opt.Concurrency <= 0 {
		opt.Concurrency = 4
	}
	if opt.QPS <= 0 {
		opt.QPS = 4
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
func (r emRow) f(k string) float64  { return r[k].n }
func (r emRow) has(k string) bool   { return r[k].ok }

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
		if sym == "" || !r.has("f2") || !r.has("f124") {
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
		if sym == "" || !r.has("f62") {
			continue
		}
		items = append(items, biz.IntradayFlow{Symbol: sym, MainNet: r.f("f62"), Super: r.f("f66"), Big: r.f("f72"),
			Mid: r.f("f78"), Small: r.f("f84"), Time: time.Unix(int64(r.f("f124")), 0).In(shanghaiLoc()), AsOf: now,
			Source: biz.SourceEastmoney})
	}
	return items, nil
}

func (e *Eastmoney) IntradayFlows(ctx context.Context) ([]biz.IntradayFlow, error) { return e.flows(ctx) }

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

// dc 分页读东财数据中心。返回数据为空时源头报 9201，按空结果处理。
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

func anyF(v any) float64 { f, _ := anyNum(v); return f }

func anyPtr(v any) *float64 {
	f, ok := anyNum(v)
	if !ok {
		return nil
	}
	return &f
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

func (e *Eastmoney) Margins(ctx context.Context, d time.Time) ([]biz.Margin, error) {
	rows, err := e.dc(ctx, "RPTA_WEB_RZRQ_GGMX", fmt.Sprintf("(DATE='%s')", ymd(d)), "")
	if err != nil {
		return nil, err
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
			} `json:"data"`
			TotalPage int `json:"TotalPage"`
		}
		if err := e.hc.getJSON(ctx, u, &out); err != nil {
			return nil, err
		}
		for _, x := range out.Data {
			var codes []string
			if sym := secu(x.StockCode); sym != "" {
				codes = []string{sym}
			}
			content := strings.TrimSpace(x.OrgSName + " " + x.EmRatingName)
			items = append(items, biz.Intel{Source: biz.SourceEastmoney, SourceID: x.InfoCode, Kind: biz.KindReport, Title: x.Title,
				Content: content, URL: "https://data.eastmoney.com/report/info/" + x.InfoCode + ".html",
				PublishTime: shTime(x.PublishDate), Codes: codes})
		}
		if page >= out.TotalPage {
			break
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].PublishTime > items[j].PublishTime })
	return items, nil
}
