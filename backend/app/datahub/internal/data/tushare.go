package data

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"server/app/datahub/internal/biz"
	"server/pkg/symbol"
)

const tushareURL = "http://api.tushare.pro"

// Tushare 是 Tushare Pro 官方 HTTP 接口，official=true。
// 单位：vol 手、amount 千元、资金流万元、股本万股，入库前换成股和元。
type Tushare struct {
	hc    *httpc
	url   string
	token string
}

func NewTushare(url, token string, opt HTTPOptions) *Tushare {
	if url == "" {
		url = tushareURL
	}
	if opt.Concurrency <= 0 {
		opt.Concurrency = 2
	}
	return &Tushare{url: url, token: token, hc: newHTTPC(biz.SourceTushare, opt, nil)}
}

func (t *Tushare) Name() string { return biz.SourceTushare }

// tsTable 是 Tushare 的表格式返回。
type tsTable struct {
	Fields []string `json:"fields"`
	Items  [][]any  `json:"items"`
}

func (tb tsTable) rows() []map[string]any {
	out := make([]map[string]any, 0, len(tb.Items))
	for _, it := range tb.Items {
		m := make(map[string]any, len(tb.Fields))
		for i, f := range tb.Fields {
			if i < len(it) {
				m[f] = it[i]
			}
		}
		out = append(out, m)
	}
	return out
}

func (t *Tushare) query(ctx context.Context, api string, params map[string]any, fields string) ([]map[string]any, error) {
	if t.token == "" {
		return nil, biz.Unavailable(biz.SourceTushare, "未配置令牌（datahub.sources.tushare.token）")
	}
	b, err := t.hc.postJSON(ctx, t.url, map[string]any{"api_name": api, "token": t.token, "params": params, "fields": fields})
	if err != nil {
		return nil, err
	}
	return parseTushare(api, b)
}

func parseTushare(api string, b []byte) ([]map[string]any, error) {
	var out struct {
		Code int     `json:"code"`
		Msg  string  `json:"msg"`
		Data tsTable `json:"data"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("tushare %s: decode: %v", api, err)
	}
	switch {
	case out.Code == 0:
		return out.Data.rows(), nil
	case out.Code == 40101 || out.Code == 40203:
		// 40101 令牌无效，40203 积分不足或无权限。
		return nil, biz.Unavailable(biz.SourceTushare, "%s：%d %s", api, out.Code, out.Msg)
	}
	return nil, fmt.Errorf("tushare %s: %d %s", api, out.Code, out.Msg)
}

func tsDate(d time.Time) string { return d.In(shanghaiLoc()).Format("20060102") }

func tsParseDate(v any) (time.Time, bool) {
	s := anyStr(v)
	if len(s) != 8 {
		return time.Time{}, false
	}
	t, err := time.ParseInLocation("20060102", s, shanghaiLoc())
	return t, err == nil
}

func tsSymbol(v any) string {
	s, err := symbol.Parse(anyStr(v))
	if err != nil {
		return ""
	}
	return s.Tongdaxin()
}

func (t *Tushare) Securities(ctx context.Context) ([]biz.Security, error) {
	rows, err := t.query(ctx, "stock_basic", map[string]any{"list_status": "L"}, "ts_code,name,industry,list_date,delist_date")
	if err != nil {
		return nil, err
	}
	items := make([]biz.Security, 0, len(rows))
	for _, r := range rows {
		sym := tsSymbol(r["ts_code"])
		if sym == "" {
			continue
		}
		s := biz.Security{Symbol: sym, Name: anyStr(r["name"]), Market: sym[len(sym)-2:], Industry: anyStr(r["industry"]), ST: isST(anyStr(r["name"]))}
		if d, ok := tsParseDate(r["list_date"]); ok {
			s.ListDate = &d
		}
		if d, ok := tsParseDate(r["delist_date"]); ok {
			s.DelistDate = &d
		}
		items = append(items, s)
	}
	return items, nil
}

// DailyBars 按交易日拉全市场，或按个股拉区间。
func (t *Tushare) DailyBars(ctx context.Context, q biz.BarQuery) (biz.BarBatch, error) {
	fields := "ts_code,trade_date,open,high,low,close,pre_close,vol,amount"
	var rows []map[string]any
	if len(q.Symbols) == 0 {
		if ymd(q.Start) != ymd(q.End) {
			return biz.BarBatch{}, biz.ErrUnsupported
		}
		r, err := t.query(ctx, "daily", map[string]any{"trade_date": tsDate(q.Start)}, fields)
		if err != nil {
			return biz.BarBatch{}, err
		}
		rows = r
	} else {
		for _, sym := range q.Symbols {
			r, err := t.query(ctx, "daily", map[string]any{"ts_code": sym, "start_date": tsDate(q.Start), "end_date": tsDate(q.End)}, fields)
			if err != nil {
				return biz.BarBatch{}, err
			}
			rows = append(rows, r...)
		}
	}
	bars := make([]biz.Bar, 0, len(rows))
	last := ""
	for _, r := range rows {
		sym := tsSymbol(r["ts_code"])
		d, ok := tsParseDate(r["trade_date"])
		if sym == "" || !ok {
			continue
		}
		pc := anyF(r["pre_close"])
		bars = append(bars, biz.Bar{Symbol: sym, Time: d, Freq: "1d", Open: anyF(r["open"]), High: anyF(r["high"]), Low: anyF(r["low"]),
			Close: anyF(r["close"]), PreClose: &pc, Volume: int64(anyF(r["vol"])*100 + 0.5), Amount: anyF(r["amount"]) * 1000})
		if s := ymd(d); s > last {
			last = s
		}
	}
	return biz.BarBatch{Bars: bars, LastDate: last, Stale: q.Expect.IsZero() == false && last < ymd(q.Expect), Expect: ymd(q.Expect)}, nil
}

func (t *Tushare) AdjFactors(ctx context.Context, d time.Time, symbols []string) ([]biz.AdjFactor, error) {
	rows, err := t.query(ctx, "adj_factor", map[string]any{"trade_date": tsDate(d)}, "ts_code,trade_date,adj_factor")
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, s := range symbols {
		want[s] = true
	}
	items := make([]biz.AdjFactor, 0, len(rows))
	for _, r := range rows {
		sym := tsSymbol(r["ts_code"])
		dd, ok := tsParseDate(r["trade_date"])
		if sym == "" || !ok || (len(want) > 0 && !want[sym]) {
			continue
		}
		items = append(items, biz.AdjFactor{Symbol: sym, Date: dd, Factor: anyF(r["adj_factor"])})
	}
	return items, nil
}

func (t *Tushare) MoneyFlows(ctx context.Context, d time.Time) ([]biz.MoneyFlow, error) {
	rows, err := t.query(ctx, "moneyflow", map[string]any{"trade_date": tsDate(d)},
		"ts_code,buy_sm_amount,sell_sm_amount,buy_md_amount,sell_md_amount,buy_lg_amount,sell_lg_amount,buy_elg_amount,sell_elg_amount")
	if err != nil {
		return nil, err
	}
	const wan = 10000
	items := make([]biz.MoneyFlow, 0, len(rows))
	for _, r := range rows {
		sym := tsSymbol(r["ts_code"])
		if sym == "" {
			continue
		}
		net := func(k string) float64 { return (anyF(r["buy_"+k+"_amount"]) - anyF(r["sell_"+k+"_amount"])) * wan }
		m := biz.MoneyFlow{Symbol: sym, SuperNet: net("elg"), BigNet: net("lg"), MidNet: net("md"), SmallNet: net("sm")}
		m.MainNet = m.SuperNet + m.BigNet
		items = append(items, m)
	}
	return items, nil
}

func (t *Tushare) LhbSeats(ctx context.Context, d time.Time) ([]biz.LhbSeat, error) {
	rows, err := t.query(ctx, "top_inst", map[string]any{"trade_date": tsDate(d)}, "ts_code,exalter,buy,sell,reason")
	if err != nil {
		return nil, err
	}
	items := make([]biz.LhbSeat, 0, len(rows))
	for _, r := range rows {
		sym := tsSymbol(r["ts_code"])
		seat := anyStr(r["exalter"])
		if sym == "" || seat == "" {
			continue
		}
		items = append(items, biz.LhbSeat{Symbol: sym, Reason: anyStr(r["reason"]), SeatName: seat, BuyAmount: anyF(r["buy"]), SellAmount: anyF(r["sell"])})
	}
	return items, nil
}

func (t *Tushare) Margins(ctx context.Context, d time.Time) ([]biz.Margin, error) {
	rows, err := t.query(ctx, "margin_detail", map[string]any{"trade_date": tsDate(d)}, "ts_code,rzye,rzmre,rzche,rqye,rqmcl,rzrqye")
	if err != nil {
		return nil, err
	}
	items := make([]biz.Margin, 0, len(rows))
	for _, r := range rows {
		sym := tsSymbol(r["ts_code"])
		if sym == "" {
			continue
		}
		items = append(items, biz.Margin{Symbol: sym, Rzye: anyF(r["rzye"]), Rzmre: anyF(r["rzmre"]), Rzche: anyF(r["rzche"]),
			Rqye: anyF(r["rqye"]), Rqmcl: int64(anyF(r["rqmcl"])), Rzrqye: anyF(r["rzrqye"])})
	}
	return items, nil
}

func (t *Tushare) HsgtTop10(ctx context.Context, d time.Time) ([]biz.HsgtTop, error) {
	rows, err := t.query(ctx, "hsgt_top10", map[string]any{"trade_date": tsDate(d)}, "ts_code,name,close,change,rank,market_type,amount,net_amount")
	if err != nil {
		return nil, err
	}
	items := make([]biz.HsgtTop, 0, len(rows))
	for _, r := range rows {
		sym := tsSymbol(r["ts_code"])
		if sym == "" {
			continue
		}
		ch := "SH"
		if anyStr(r["market_type"]) == "3" {
			ch = "SZ"
		}
		items = append(items, biz.HsgtTop{Symbol: sym, Channel: ch, Rank: int(anyF(r["rank"])), Name: anyStr(r["name"]),
			Close: anyPtr(r["close"]), PctChg: anyPtr(r["change"]), Amount: anyF(r["amount"]), NetAmount: anyPtr(r["net_amount"])})
	}
	return items, nil
}

// Finance 按公告日逐日拉预告和快报，since 到今天，最多 31 天。
func (t *Tushare) Finance(ctx context.Context, since time.Time) ([]biz.FinanceItem, error) {
	today := day(time.Now())
	start := day(since)
	if today.Sub(start) > 31*24*time.Hour {
		start = today.AddDate(0, 0, -31)
	}
	var items []biz.FinanceItem
	for d := start; !d.After(today); d = d.AddDate(0, 0, 1) {
		rows, err := t.query(ctx, "forecast", map[string]any{"ann_date": tsDate(d)},
			"ts_code,ann_date,end_date,type,p_change_min,p_change_max,net_profit_min,net_profit_max,summary,change_reason")
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			sym := tsSymbol(r["ts_code"])
			ann, ok1 := tsParseDate(r["ann_date"])
			end, ok2 := tsParseDate(r["end_date"])
			if sym == "" || !ok1 || !ok2 {
				continue
			}
			it := biz.FinanceItem{Symbol: sym, EndDate: end, Kind: biz.FinanceForecast, AnnDate: ann, ForecastType: anyStr(r["type"]),
				Summary: anyStr(r["summary"]), NetProfitYoY: anyPtr(r["p_change_max"]),
				Data: map[string]any{"p_change_min": r["p_change_min"], "net_profit_min": r["net_profit_min"], "change_reason": r["change_reason"]}}
			if v, ok := anyNum(r["net_profit_max"]); ok {
				v *= 10000
				it.NetProfit = &v
			}
			items = append(items, it)
		}
		rows, err = t.query(ctx, "express", map[string]any{"ann_date": tsDate(d)},
			"ts_code,ann_date,end_date,revenue,n_income,yoy_net_profit,diluted_eps,diluted_roe")
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			sym := tsSymbol(r["ts_code"])
			ann, ok1 := tsParseDate(r["ann_date"])
			end, ok2 := tsParseDate(r["end_date"])
			if sym == "" || !ok1 || !ok2 {
				continue
			}
			items = append(items, biz.FinanceItem{Symbol: sym, EndDate: end, Kind: biz.FinanceExpress, AnnDate: ann,
				NetProfit: anyPtr(r["n_income"]), NetProfitYoY: anyPtr(r["yoy_net_profit"]),
				Data: map[string]any{"revenue": r["revenue"], "eps": r["diluted_eps"], "roe": r["diluted_roe"]}})
		}
	}
	return items, nil
}
