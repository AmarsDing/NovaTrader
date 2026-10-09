package data

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"

	"server/app/datahub/internal/biz"
	"server/pkg/market"
)

// pyworker 是 Python 边车的 HTTP 客户端。一个角色对应一个 Source。
type pyworker struct {
	name string
	base string
	hc   *httpc
}

func newPyworker(name, base string, opt HTTPOptions) pyworker {
	if opt.Concurrency <= 0 {
		opt.Concurrency = 2
	}
	return pyworker{name: name, base: strings.TrimRight(base, "/"), hc: newHTTPC(name, opt, nil)}
}

func (p pyworker) Name() string { return p.name }

// pyError 是 pyworker 的错误体。HTTP 503 表示依赖没就绪（插件缺失、文件为空、主站不兼容）。
type pyError struct {
	Error  string `json:"error"`
	Reason string `json:"reason"`
}

func (p pyworker) call(ctx context.Context, method, path string, body any, out any) error {
	if p.base == "" {
		return biz.Unavailable(p.name, "未配置 pyworker 地址")
	}
	u := p.base + path
	req, err := newRequest(ctx, method, u, body)
	if err != nil {
		return err
	}
	b, err := p.hc.do(ctx, req)
	if err != nil {
		var he *httpError
		switch {
		case errors.As(err, &he) && he.Status == http.StatusServiceUnavailable:
			var pe pyError
			if json.Unmarshal(he.Body, &pe) == nil && pe.Reason != "" {
				return biz.Unavailable(p.name, "%s", pe.Reason)
			}
			return biz.Unavailable(p.name, "%s", snippet(he.Body))
		case errors.As(err, &he) && he.Status == http.StatusNotFound:
			return biz.Unavailable(p.name, "pyworker 未启用该角色")
		case ctx.Err() == nil && isConnRefused(err):
			return biz.Unavailable(p.name, "pyworker 未启动（%s）", p.base)
		}
		return err
	}
	return decodeJSON(p.name, b, out)
}

func newRequest(ctx context.Context, method, u string, body any) (*http.Request, error) {
	if body == nil {
		return http.NewRequestWithContext(ctx, method, u, nil)
	}
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, u, strings.NewReader(string(b)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func isConnRefused(err error) bool {
	if errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	s := err.Error()
	return strings.Contains(s, "connection refused") || strings.Contains(s, "actively refused") ||
		strings.Contains(s, "connectex") || strings.Contains(s, "no such host")
}

type pyBar struct {
	Symbol   string    `json:"symbol"`
	Time     time.Time `json:"time"`
	Freq     string    `json:"freq"`
	Open     float64   `json:"open"`
	High     float64   `json:"high"`
	Low      float64   `json:"low"`
	Close    float64   `json:"close"`
	PreClose *float64  `json:"pre_close"`
	Volume   int64     `json:"volume"`
	Amount   float64   `json:"amount"`
}

type pyBars struct {
	Bars     []pyBar `json:"bars"`
	LastDate string  `json:"last_date"`
	Stale    bool    `json:"stale"`
}

type barRequest struct {
	Freq    string   `json:"freq"`
	Start   string   `json:"start"`
	End     string   `json:"end"`
	Expect  string   `json:"expect,omitempty"`
	Symbols []string `json:"symbols"`
}

func ymd(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.In(shanghaiLoc()).Format("2006-01-02")
}

func (p pyworker) bars(ctx context.Context, path string, q biz.BarQuery) (biz.BarBatch, error) {
	var out pyBars
	req := barRequest{Freq: q.Freq, Start: ymd(q.Start), End: ymd(q.End), Expect: ymd(q.Expect), Symbols: q.Symbols}
	if err := p.call(ctx, http.MethodPost, path, req, &out); err != nil {
		return biz.BarBatch{}, err
	}
	bars := make([]biz.Bar, 0, len(out.Bars))
	for _, b := range out.Bars {
		freq := b.Freq
		if freq == "" {
			freq = q.Freq
		}
		bars = append(bars, biz.Bar{Symbol: b.Symbol, Time: b.Time, Freq: freq, Open: b.Open, High: b.High, Low: b.Low,
			Close: b.Close, PreClose: b.PreClose, Volume: b.Volume, Amount: b.Amount})
	}
	return biz.BarBatch{Bars: bars, Stale: out.Stale, LastDate: out.LastDate, Expect: ymd(q.Expect)}, nil
}

func (p pyworker) snapshots(ctx context.Context, path string, symbols []string) (biz.SnapshotBatch, error) {
	var out struct {
		Items []market.Snapshot `json:"items"`
	}
	if symbols == nil {
		symbols = []string{}
	}
	if err := p.call(ctx, http.MethodPost, path, map[string]any{"symbols": symbols}, &out); err != nil {
		return biz.SnapshotBatch{}, err
	}
	for i := range out.Items {
		out.Items[i].Source = p.name
	}
	return biz.SnapshotBatch{Items: out.Items}, nil
}

// TdxLocal 是接法 A：通达信官方 tqcenter，经 Windows 上的 pyworker 调用。
type TdxLocal struct{ pyworker }

func NewTdxLocal(base string, opt HTTPOptions) *TdxLocal {
	return &TdxLocal{newPyworker(biz.SourceTdxLocal, base, opt)}
}

func (s *TdxLocal) Snapshots(ctx context.Context, symbols []string) (biz.SnapshotBatch, error) {
	return s.snapshots(ctx, "/api/snapshot", symbols)
}

func (s *TdxLocal) DailyBars(ctx context.Context, q biz.BarQuery) (biz.BarBatch, error) {
	q.Freq = "1d"
	return s.bars(ctx, "/api/bars", q)
}

func (s *TdxLocal) MinuteBars(ctx context.Context, q biz.BarQuery) (biz.BarBatch, error) {
	return s.bars(ctx, "/api/bars", q)
}

func (s *TdxLocal) Securities(ctx context.Context) ([]biz.Security, error) {
	return s.securities(ctx, "/api/securities")
}

func (p pyworker) securities(ctx context.Context, path string) ([]biz.Security, error) {
	var out struct {
		Items []struct {
			Symbol string `json:"symbol"`
			Name   string `json:"name"`
			Market string `json:"market"`
			ST     bool   `json:"st"`
		} `json:"items"`
	}
	if err := p.call(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	items := make([]biz.Security, 0, len(out.Items))
	for _, x := range out.Items {
		items = append(items, biz.Security{Symbol: x.Symbol, Name: x.Name, Market: x.Market, ST: x.ST || isST(x.Name)})
	}
	return items, nil
}

// TdxFile 是接法 B：读通达信盘后下载的 vipdoc 文件和板块文件。
type TdxFile struct{ pyworker }

func NewTdxFile(base string, opt HTTPOptions) *TdxFile {
	return &TdxFile{newPyworker(biz.SourceTdxFile, base, opt)}
}

func (s *TdxFile) DailyBars(ctx context.Context, q biz.BarQuery) (biz.BarBatch, error) {
	q.Freq = "1d"
	return s.bars(ctx, "/file/bars", q)
}

func (s *TdxFile) MinuteBars(ctx context.Context, q biz.BarQuery) (biz.BarBatch, error) {
	return s.bars(ctx, "/file/bars", q)
}

func (s *TdxFile) Sectors(ctx context.Context) ([]biz.Sector, error) {
	var out struct {
		Sectors []struct {
			Code    string   `json:"code"`
			Name    string   `json:"name"`
			Kind    string   `json:"kind"`
			Members []string `json:"members"`
		} `json:"sectors"`
	}
	if err := s.call(ctx, http.MethodGet, "/file/sectors", nil, &out); err != nil {
		return nil, err
	}
	items := make([]biz.Sector, 0, len(out.Sectors))
	for _, x := range out.Sectors {
		items = append(items, biz.Sector{Code: x.Code, Name: x.Name, Kind: x.Kind, Members: x.Members})
	}
	return items, nil
}

// TdxTCP 是接法 C：7709 行情主站，社区协议，official=false。
type TdxTCP struct{ pyworker }

func NewTdxTCP(base string, opt HTTPOptions) *TdxTCP {
	return &TdxTCP{newPyworker(biz.SourceTdxTCP, base, opt)}
}

func (s *TdxTCP) Snapshots(ctx context.Context, symbols []string) (biz.SnapshotBatch, error) {
	return s.snapshots(ctx, "/tcp/snapshot", symbols)
}

func (s *TdxTCP) DailyBars(ctx context.Context, q biz.BarQuery) (biz.BarBatch, error) {
	q.Freq = "1d"
	return s.bars(ctx, "/tcp/bars", q)
}

func (s *TdxTCP) MinuteBars(ctx context.Context, q biz.BarQuery) (biz.BarBatch, error) {
	return s.bars(ctx, "/tcp/bars", q)
}

func (s *TdxTCP) Securities(ctx context.Context) ([]biz.Security, error) {
	return s.securities(ctx, "/tcp/securities")
}

func (s *TdxTCP) AdjFactors(ctx context.Context, day time.Time, symbols []string) ([]biz.AdjFactor, error) {
	var out struct {
		Items []struct {
			Symbol string  `json:"symbol"`
			Date   string  `json:"date"`
			Factor float64 `json:"factor"`
		} `json:"items"`
	}
	if err := s.call(ctx, http.MethodPost, "/tcp/adj_factor", map[string]any{"date": ymd(day), "symbols": symbols}, &out); err != nil {
		return nil, err
	}
	items := make([]biz.AdjFactor, 0, len(out.Items))
	for _, x := range out.Items {
		d, err := time.ParseInLocation("2006-01-02", x.Date, shanghaiLoc())
		if err != nil {
			continue
		}
		items = append(items, biz.AdjFactor{Symbol: x.Symbol, Date: d, Factor: x.Factor})
	}
	return items, nil
}

// Akshare 是 AkShare 边车，用于外围、宏观和涨停池备源。
type Akshare struct{ pyworker }

func NewAkshare(base string, opt HTTPOptions) *Akshare {
	return &Akshare{newPyworker(biz.SourceAkshare, base, opt)}
}

func (s *Akshare) Overseas(ctx context.Context) ([]biz.OverseasQuote, error) {
	var out struct {
		Items []struct {
			Code      string  `json:"code"`
			Name      string  `json:"name"`
			TradeDate string  `json:"trade_date"`
			Close     float64 `json:"close"`
			PctChg    float64 `json:"pct_chg"`
		} `json:"items"`
	}
	if err := s.call(ctx, http.MethodGet, "/ak/global", nil, &out); err != nil {
		return nil, err
	}
	now := time.Now()
	items := make([]biz.OverseasQuote, 0, len(out.Items))
	for _, x := range out.Items {
		d, err := time.Parse("2006-01-02", x.TradeDate)
		if err != nil {
			continue
		}
		items = append(items, biz.OverseasQuote{Code: x.Code, Name: x.Name, TradeDate: d, Close: x.Close, PctChg: x.PctChg, AsOf: now})
	}
	return items, nil
}

func (s *Akshare) Macro(ctx context.Context) ([]biz.MacroPoint, error) {
	var out struct {
		Items []struct {
			Code  string  `json:"code"`
			Name  string  `json:"name"`
			Date  string  `json:"date"`
			Value float64 `json:"value"`
			Unit  string  `json:"unit"`
		} `json:"items"`
	}
	if err := s.call(ctx, http.MethodGet, "/ak/macro", nil, &out); err != nil {
		return nil, err
	}
	items := make([]biz.MacroPoint, 0, len(out.Items))
	for _, x := range out.Items {
		d, err := time.ParseInLocation("2006-01-02", x.Date, shanghaiLoc())
		if err != nil {
			continue
		}
		items = append(items, biz.MacroPoint{Code: x.Code, Name: x.Name, Date: d, Value: x.Value, Unit: x.Unit})
	}
	return items, nil
}

func (s *Akshare) LimitPool(ctx context.Context, day time.Time) ([]biz.LimitEntry, error) {
	var all []biz.LimitEntry
	for _, pool := range []string{biz.PoolUp, biz.PoolDown, biz.PoolBroken} {
		var out struct {
			Items []struct {
				Symbol      string   `json:"symbol"`
				Name        string   `json:"name"`
				Close       *float64 `json:"close"`
				PctChg      *float64 `json:"pct_chg"`
				Amount      *float64 `json:"amount"`
				FirstSealAt string   `json:"first_seal_at"`
				LastSealAt  string   `json:"last_seal_at"`
				OpenCount   int      `json:"open_count"`
				SealAmount  float64  `json:"seal_amount"`
				Consecutive int      `json:"consecutive"`
				Reason      string   `json:"reason"`
			} `json:"items"`
		}
		path := "/ak/limit_pool?" + url.Values{"date": {ymd(day)}, "pool": {pool}}.Encode()
		if err := s.call(ctx, http.MethodGet, path, nil, &out); err != nil {
			return nil, fmt.Errorf("pool %s: %w", pool, err)
		}
		for _, x := range out.Items {
			all = append(all, biz.LimitEntry{Symbol: x.Symbol, Pool: pool, Name: x.Name, Close: x.Close, PctChg: x.PctChg,
				Amount: x.Amount, FirstSealAt: parseISO(x.FirstSealAt), LastSealAt: parseISO(x.LastSealAt),
				OpenCount: x.OpenCount, SealAmount: x.SealAmount, Consecutive: x.Consecutive, Reason: x.Reason})
		}
	}
	return all, nil
}

// MinuteBars 只补指定股票，AkShare 逐只拉取，不做全市场。
func (s *Akshare) MinuteBars(ctx context.Context, q biz.BarQuery) (biz.BarBatch, error) {
	if len(q.Symbols) == 0 || len(q.Symbols) > 50 {
		return biz.BarBatch{}, biz.ErrUnsupported
	}
	var all []biz.Bar
	for _, sym := range q.Symbols {
		var out pyBars
		path := "/ak/minute?" + url.Values{"symbol": {sym}, "start": {ymd(q.Start)}, "end": {ymd(q.End)}, "freq": {q.Freq}}.Encode()
		if err := s.call(ctx, http.MethodGet, path, nil, &out); err != nil {
			return biz.BarBatch{}, err
		}
		for _, b := range out.Bars {
			all = append(all, biz.Bar{Symbol: b.Symbol, Time: b.Time, Freq: q.Freq, Open: b.Open, High: b.High, Low: b.Low,
				Close: b.Close, Volume: b.Volume, Amount: b.Amount})
		}
	}
	return biz.BarBatch{Bars: all}, nil
}

func parseISO(s string) *time.Time {
	if s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil
	}
	return &t
}

func isST(name string) bool {
	n := strings.ToUpper(strings.ReplaceAll(name, " ", ""))
	return strings.Contains(n, "ST")
}
