package data

import (
	"context"
	"net/http"
	"time"

	"server/app/datahub/internal/biz"
)

// Baostock 是 pyworker baostock 角色的 Go 适配：盘后回补冗余 + 财务三表。
// 链末定位：domains.daily_bar/finance 的 sources 尾部，enabled 默认 false，
// 前三源先得手时几无运行机会；补数可 SubmitBackfill(source=baostock) 单源钉链。
// 嵌入式复用 pyworker.call 的错误映射：503→UnavailableError（依赖未装/断线重登仍失败）、
// 404→pyworker 未启用该角色、连接拒绝→pyworker 未启动。
// 体积纪律：全市场单批 JSON 可上 30MB 边（httpc maxBody 32MB），故按 200 只/批分批，
// 单批响应压在 1MB 量级；pyworker 侧会话全局互斥串行，>2 并发无收益。
type Baostock struct {
	pyworker
	universe func(context.Context) ([]string, error)
}

// NewBaostock universe 为 repo.ActiveSymbols 闭包（与 NewSina/NewTencent 同款），
// q.Symbols 为空时补全名单；补全失败直接返回错误，不空跑。
func NewBaostock(base string, universe func(context.Context) ([]string, error), opt HTTPOptions) *Baostock {
	return &Baostock{pyworker: newPyworker(biz.SourceBaostock, base, opt), universe: universe}
}

// bsBatch 是 Baostock 单批请求的稳定批量上限（见 Baostock 注释的体积纪律）。
const bsBatch = 200

// bsBarRequest 比 pyworker barRequest 多 AdjustFlag：日线钉 "3"（不复权）。
// 独立类型不动 pyworker.go 现有 barRequest（TDX 三接法在共用）。
type bsBarRequest struct {
	Freq       string   `json:"freq"`
	Start      string   `json:"start"`
	End        string   `json:"end"`
	Expect     string   `json:"expect,omitempty"`
	Symbols    []string `json:"symbols"`
	AdjustFlag string   `json:"adjustflag"`
}

// DailyBars 只提供日线（Freq 钉 "1d"；分钟频率 pyworker 侧 400，本适配不实现 MinuteBarSource）。
// pre_close 空值保持 *float64 nil（数据源可缺，不以 0 冒充）；stale/last_date 跨批聚合透传：
// 任一批 stale 即整体 stale，last_date 取各批最大（ISO 日期字典序即时间序）。
func (s *Baostock) DailyBars(ctx context.Context, q biz.BarQuery) (biz.BarBatch, error) {
	q.Freq = "1d"
	symbols, err := s.symbols(ctx, q.Symbols)
	if err != nil {
		return biz.BarBatch{}, err
	}
	var all []biz.Bar
	stale := false
	last := ""
	for i := 0; i < len(symbols); i += bsBatch {
		end := i + bsBatch
		if end > len(symbols) {
			end = len(symbols)
		}
		var out pyBars
		req := bsBarRequest{Freq: q.Freq, Start: ymd(q.Start), End: ymd(q.End), Expect: ymd(q.Expect),
			Symbols: symbols[i:end], AdjustFlag: "3"}
		if err := s.call(ctx, http.MethodPost, "/bs/bars", req, &out); err != nil {
			return biz.BarBatch{}, err
		}
		for _, b := range out.Bars {
			freq := b.Freq
			if freq == "" {
				freq = q.Freq
			}
			all = append(all, biz.Bar{Availability: b.Availability, Symbol: b.Symbol, Time: b.Time, Freq: freq,
				Open: b.Open, High: b.High, Low: b.Low, Close: b.Close, PreClose: b.PreClose, Volume: b.Volume, Amount: b.Amount})
		}
		if out.Stale {
			stale = true
		}
		if out.LastDate > last {
			last = out.LastDate
		}
	}
	return biz.BarBatch{Bars: all, Stale: stale, LastDate: last, Expect: ymd(q.Expect)}, nil
}

// Finance 自 since 起按季度窗逐股合并 profit/balance/cashflow/growth 四表（pyworker 侧完成）。
// 映射口径：Kind=indicator；pub_date/end_date 解析失败的行 skip（AnnDate 缺失不可入库，
// 不显 0、不假日期）；net_profit/net_profit_yoy 指针透传（坏值 pyworker 侧已置 null）；
// data 原样留存四表原始行。返回切片可空（合法结果，非缺失冒充）。
func (s *Baostock) Finance(ctx context.Context, since time.Time) ([]biz.FinanceItem, error) {
	symbols, err := s.symbols(ctx, nil)
	if err != nil {
		return nil, err
	}
	var items []biz.FinanceItem
	for i := 0; i < len(symbols); i += bsBatch {
		end := i + bsBatch
		if end > len(symbols) {
			end = len(symbols)
		}
		var out struct {
			Items []struct {
				Symbol       string         `json:"symbol"`
				EndDate      string         `json:"end_date"`
				PubDate      string         `json:"pub_date"`
				Kind         string         `json:"kind"`
				NetProfit    *float64       `json:"net_profit"`
				NetProfitYoY *float64       `json:"net_profit_yoy"`
				Data         map[string]any `json:"data"`
			} `json:"items"`
		}
		req := map[string]any{"symbols": symbols[i:end], "since": ymd(since)}
		if err := s.call(ctx, http.MethodPost, "/bs/finance", req, &out); err != nil {
			return nil, err
		}
		for _, x := range out.Items {
			pub, err := time.ParseInLocation("2006-01-02", x.PubDate, shanghaiLoc())
			if err != nil {
				continue
			}
			endDate, err := time.ParseInLocation("2006-01-02", x.EndDate, shanghaiLoc())
			if err != nil {
				continue
			}
			kind := x.Kind
			if kind == "" {
				kind = biz.FinanceIndicator
			}
			items = append(items, biz.FinanceItem{Symbol: x.Symbol, EndDate: endDate, Kind: kind, AnnDate: pub,
				NetProfit: x.NetProfit, NetProfitYoY: x.NetProfitYoY, Summary: "", Data: x.Data})
		}
	}
	return items, nil
}

// symbols 补全名单：显式给了就用，空则走 universe；universe 未接或失败即不可用/报错，不空跑。
func (s *Baostock) symbols(ctx context.Context, given []string) ([]string, error) {
	if len(given) > 0 {
		return given, nil
	}
	if s.universe == nil {
		return nil, biz.Unavailable(biz.SourceBaostock, "未配置证券名单（universe 闭包缺失）")
	}
	return s.universe(ctx)
}
