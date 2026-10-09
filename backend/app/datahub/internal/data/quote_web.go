package data

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"server/app/datahub/internal/biz"
	"server/pkg/market"
	"server/pkg/symbol"
)

// Universe 返回全市场 A 股代码，新浪、腾讯按代码批量查询时使用。
type Universe func(ctx context.Context) ([]string, error)

// lowerCode 把 600519.SH 转成 sh600519。
func lowerCode(sym string) (string, bool) {
	s, err := symbol.Parse(sym)
	if err != nil {
		return "", false
	}
	return strings.ToLower(s.Market) + s.Code, true
}

// batchQuotes 按批并发查询，任一批失败则整体失败，避免把半个市场当成全市场。
func batchQuotes(ctx context.Context, symbols []string, size int, fetch func(ctx context.Context, codes []string) ([]market.Snapshot, error)) ([]market.Snapshot, error) {
	var codes []string
	for _, s := range symbols {
		if c, ok := lowerCode(s); ok {
			codes = append(codes, c)
		}
	}
	n := (len(codes) + size - 1) / size
	parts := make([][]market.Snapshot, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		j := min((i+1)*size, len(codes))
		wg.Add(1)
		go func(i int, part []string) {
			defer wg.Done()
			parts[i], errs[i] = fetch(ctx, part)
		}(i, codes[i*size:j])
	}
	wg.Wait()
	var out []market.Snapshot
	for i := range parts {
		if errs[i] != nil {
			return nil, errs[i]
		}
		out = append(out, parts[i]...)
	}
	return out, nil
}

func symbols(ctx context.Context, given []string, u Universe, source string) ([]string, error) {
	if len(given) > 0 {
		return given, nil
	}
	if u == nil {
		return nil, biz.Unavailable(source, "没有股票列表")
	}
	list, err := u(ctx)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, biz.Unavailable(source, "stock_basic 为空，先跑 security 采集")
	}
	return list, nil
}

// fromLower 把 sh600519 转回 600519.SH。
func fromLower(c string) string {
	s, err := symbol.Parse(c)
	if err != nil {
		return ""
	}
	return s.Tongdaxin()
}

func pf(s string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f
}

// Sina 是新浪行情 hq.sinajs.cn，非官方。成交量单位股，成交额元。
type Sina struct {
	hc  *httpc
	all Universe
}

func NewSina(opt HTTPOptions, all Universe) *Sina {
	if opt.Concurrency <= 0 {
		opt.Concurrency = 4
	}
	return &Sina{all: all, hc: newHTTPC(biz.SourceSina, opt, map[string]string{
		"Referer":    "https://finance.sina.com.cn",
		"User-Agent": "Mozilla/5.0",
	})}
}

func (s *Sina) Name() string { return biz.SourceSina }

func (s *Sina) Snapshots(ctx context.Context, given []string) (biz.SnapshotBatch, error) {
	list, err := symbols(ctx, given, s.all, biz.SourceSina)
	if err != nil {
		return biz.SnapshotBatch{}, err
	}
	// 实测 URL 超过约 4KB（500 只）时请求挂起不返回，每批 300 只。
	items, err := batchQuotes(ctx, list, 300, s.fetch)
	return biz.SnapshotBatch{Items: items}, err
}

func (s *Sina) fetch(ctx context.Context, codes []string) ([]market.Snapshot, error) {
	b, err := s.hc.get(ctx, "https://hq.sinajs.cn/list="+strings.Join(codes, ","))
	if err != nil {
		return nil, err
	}
	return parseSina(gbk(b)), nil
}

// parseSina 解析 var hq_str_sh600519="名称,今开,昨收,现价,最高,最低,买一,卖一,量,额,买一量,买一价,…,日期,时间,…";
func parseSina(body string) []market.Snapshot {
	var out []market.Snapshot
	for _, line := range strings.Split(body, ";") {
		line = strings.TrimSpace(line)
		i := strings.Index(line, "hq_str_")
		j := strings.Index(line, "=\"")
		if i < 0 || j < 0 {
			continue
		}
		sym := fromLower(line[i+7 : j])
		f := strings.Split(strings.Trim(line[j+1:], "\""), ",")
		if sym == "" || len(f) < 32 {
			continue
		}
		t, err := time.ParseInLocation("2006-01-02 15:04:05", f[30]+" "+f[31], shanghaiLoc())
		if err != nil || pf(f[3]) <= 0 {
			continue
		}
		snap := market.Snapshot{Symbol: sym, Time: t, Source: biz.SourceSina, Open: pf(f[1]), PreClose: pf(f[2]), Last: pf(f[3]),
			High: pf(f[4]), Low: pf(f[5]), Volume: int64(pf(f[8])), Amount: pf(f[9])}
		for k := 0; k < 5; k++ {
			snap.Bid = append(snap.Bid, [2]float64{pf(f[11+2*k]), pf(f[10+2*k])})
			snap.Ask = append(snap.Ask, [2]float64{pf(f[21+2*k]), pf(f[20+2*k])})
		}
		out = append(out, snap)
	}
	return out
}

// Tencent 是腾讯行情 qt.gtimg.cn，非官方。字段以 ~ 分隔，成交量单位手。
type Tencent struct {
	hc  *httpc
	all Universe
}

func NewTencent(opt HTTPOptions, all Universe) *Tencent {
	if opt.Concurrency <= 0 {
		opt.Concurrency = 4
	}
	return &Tencent{all: all, hc: newHTTPC(biz.SourceTencent, opt, map[string]string{"User-Agent": "Mozilla/5.0"})}
}

func (s *Tencent) Name() string { return biz.SourceTencent }

func (s *Tencent) Snapshots(ctx context.Context, given []string) (biz.SnapshotBatch, error) {
	list, err := symbols(ctx, given, s.all, biz.SourceTencent)
	if err != nil {
		return biz.SnapshotBatch{}, err
	}
	items, err := batchQuotes(ctx, list, 300, s.fetch)
	return biz.SnapshotBatch{Items: items}, err
}

func (s *Tencent) fetch(ctx context.Context, codes []string) ([]market.Snapshot, error) {
	b, err := s.hc.get(ctx, "https://qt.gtimg.cn/q="+strings.Join(codes, ","))
	if err != nil {
		return nil, err
	}
	return parseTencent(gbk(b))
}

// parseTencent 解析 v_sh600519="1~名称~代码~现价~昨收~今开~量(手)~…~时间(30)~…~最高(33)~最低(34)~价/量/额(35)~…";
func parseTencent(body string) ([]market.Snapshot, error) {
	var out []market.Snapshot
	for _, line := range strings.Split(body, ";") {
		line = strings.TrimSpace(line)
		j := strings.Index(line, "=\"")
		if !strings.HasPrefix(line, "v_") || j < 0 {
			continue
		}
		if strings.HasPrefix(line, "v_pv_none_match") {
			continue
		}
		sym := fromLower(line[2:j])
		f := strings.Split(strings.Trim(line[j+1:], "\""), "~")
		if sym == "" || len(f) < 38 {
			continue
		}
		t, err := time.ParseInLocation("20060102150405", f[30], shanghaiLoc())
		if err != nil || pf(f[3]) <= 0 {
			continue
		}
		pva := strings.Split(f[35], "/")
		if len(pva) != 3 {
			return nil, fmt.Errorf("tencent %s: unexpected field 35 %q", sym, f[35])
		}
		snap := market.Snapshot{Symbol: sym, Time: t, Source: biz.SourceTencent, Last: pf(f[3]), PreClose: pf(f[4]), Open: pf(f[5]),
			High: pf(f[33]), Low: pf(f[34]), Volume: int64(pf(pva[1])) * 100, Amount: pf(pva[2])}
		for k := 0; k < 5; k++ {
			snap.Bid = append(snap.Bid, [2]float64{pf(f[9+2*k]), pf(f[10+2*k]) * 100})
			snap.Ask = append(snap.Ask, [2]float64{pf(f[19+2*k]), pf(f[20+2*k]) * 100})
		}
		out = append(out, snap)
	}
	return out, nil
}
