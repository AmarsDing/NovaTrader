package data

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	marketv1 "server/api/market/v1"
	"server/conf"
	"server/pkg/rules"
	"server/pkg/tradecal"

	"github.com/go-kratos/kratos/v2/log"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// M13-T01：strategy → market(2021) 的只读 HTTP 客户端。情绪截面（当日阶段，
// 60s 缓存）与日线（按代码取回后客户端换算前复权）都从这里取，data 层不再直读
// market_sentiment / market_data 表（横切越权清理，见 M13 设计文档 T01）。
//
// 数据纪律：地址未配置、请求失败、404 当日无截面都是显式缺失（ok=false 或
// 返回 err），绝不回落成编出来的 WARM 或空序列冒充有数据。

const (
	sentimentCacheTTL = 60 * time.Second
	maxBarsPerSymbol  = 1000
)

// errNoMarketAddr 表示 strategy.market_addr 未配置。
var errNoMarketAddr = errors.New("strategy: market_addr is empty")

// marketClient 实现 biz.SentimentSource，并作为日线只读口供 marketSource 使用。
type marketClient struct {
	addr string
	http *http.Client
	log  *log.Helper

	// 情绪 60s 缓存（同日同窗口复用；404 也缓存，避免早盘前每轮扫描都打一次）。
	mu      sync.Mutex
	day     string // 缓存对应的交易日 2006-01-02
	stage   rules.Stage
	stageOK bool
	cached  bool
	at      time.Time
}

// NewMarketClient 装配 market 只读客户端。addr 为空不致命：情绪按缺失返回
// ok=false，日线返回 errNoMarketAddr 让扫描显式失败（没取到≠0，不编数）。
func NewMarketClient(c *conf.Strategy, logger log.Logger) *marketClient {
	addr := ""
	if c != nil {
		addr = strings.TrimRight(c.GetMarketAddr(), "/")
	}
	return &marketClient{
		addr: addr,
		http: &http.Client{Timeout: 10 * time.Second},
		log:  log.NewHelper(log.With(logger, "module", "strategy/market-client")),
	}
}

// Stage 是 biz.SentimentSource：asOf 当日最新情绪阶段。ok=false 为显式缺失
// （404 当日无截面、地址未配置、源端阶段未知）；传输或 5xx 错误用 err 表达。
func (m *marketClient) Stage(ctx context.Context, asOf time.Time) (rules.Stage, bool, error) {
	if m == nil || m.addr == "" {
		return "", false, errNoMarketAddr
	}
	day := asOf.In(tradecal.Shanghai()).Format("2006-01-02")
	now := time.Now()
	m.mu.Lock()
	if m.cached && m.day == day && now.Sub(m.at) < sentimentCacheTTL {
		stage, ok := m.stage, m.stageOK
		m.mu.Unlock()
		return stage, ok, nil
	}
	m.mu.Unlock()

	q := url.Values{
		"trade_date": {day},
		"as_of":      {asOf.Format(time.RFC3339)},
	}
	var reply marketv1.Sentiment
	status, err := m.doGet(ctx, "/v1/market/sentiment", q, &reply)
	if err != nil {
		return "", false, err
	}
	stage, ok := rules.Stage(""), false
	switch {
	case status == http.StatusOK:
		switch s := rules.Stage(reply.GetPhase()); s {
		case rules.StageIce, rules.StageRecover, rules.StageWarm, rules.StageHot, rules.StageFade:
			stage, ok = s, true
		default:
			// 源端回了未知阶段：按缺失处理，不猜（数据纪律）。
			m.log.Warnf("market sentiment phase %q unknown, treated as missing", reply.GetPhase())
		}
	case status == http.StatusNotFound: // 当日还没有情绪截面：缺失，不是故障。
	default:
		return "", false, fmt.Errorf("market sentiment http %d", status)
	}
	m.mu.Lock()
	m.cached, m.day, m.stage, m.stageOK, m.at = true, day, stage, ok, now
	m.mu.Unlock()
	return stage, ok, nil
}

// dailyBars 取 [day-historyDays, day) 的日线（adjust=NONE 拿回原始价与复权因子），
// 客户端以最后一根为基准换成前复权并截尾 keepBars。基准显式固定在 day 之前，
// 与原直读库口径（queryDailyBars）一致；没有用 M02 的 QFQ 参数，是为避免
// 「返回窗口最后一根」随调用区间漂移基准。
func (m *marketClient) dailyBars(ctx context.Context, symbol string, day time.Time) ([]rules.Bar, error) {
	start := day.AddDate(0, 0, -historyDays).Format("2006-01-02")
	end := day.AddDate(0, 0, -1).Format("2006-01-02")
	rows, err := m.barsRaw(ctx, symbol, start, end, maxBarsPerSymbol)
	if err != nil {
		return nil, err
	}
	if len(rows) > keepBars {
		rows = rows[len(rows)-keepBars:]
	}
	base := 0.0
	if n := len(rows); n > 0 {
		base = rows[n-1].GetAdjFactor()
	}
	if base <= 0 {
		base = 1
	}
	out := make([]rules.Bar, 0, len(rows))
	for _, r := range rows {
		t, terr := time.Parse(time.RFC3339, r.GetTime())
		if terr != nil {
			return nil, fmt.Errorf("market bars %s time %q: %w", symbol, r.GetTime(), terr)
		}
		k := 1.0
		if r.GetAdjFactor() > 0 {
			k = r.GetAdjFactor() / base
		}
		out = append(out, rules.Bar{
			Time: t, Open: r.GetOpen() * k, High: r.GetHigh() * k, Low: r.GetLow() * k, Close: r.GetClose() * k,
			Volume: r.GetVolume(), Amount: r.GetAmount(),
		})
	}
	return out, nil
}

// closesRaw 取 [from-200d, to] 日线原始价与复权因子。多取的窗口只为找到
// from 之前最后一根因子（折算基准，与原 DailyCloses 口径一致；停牌跨长假
// 也能覆盖）；消费方按自己的 [from, to] 区间过滤结果。
func (m *marketClient) closesRaw(ctx context.Context, symbol string, from, to time.Time) ([]*marketv1.Bar, error) {
	start := dayStart(from).AddDate(0, 0, -200).Format("2006-01-02")
	end := dayStart(to).Format("2006-01-02")
	return m.barsRaw(ctx, symbol, start, end, maxBarsPerSymbol)
}

func (m *marketClient) barsRaw(ctx context.Context, symbol, start, end string, limit int) ([]*marketv1.Bar, error) {
	if m == nil || m.addr == "" {
		return nil, errNoMarketAddr
	}
	q := url.Values{
		"symbol": {symbol},
		"freq":   {"1d"},
		"start":  {start},
		"end":    {end},
		"adjust": {marketv1.Adjust_NONE.String()},
		"limit":  {fmt.Sprint(limit)},
	}
	var reply marketv1.GetBarsReply
	status, err := m.doGet(ctx, "/v1/market/bars", q, &reply)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("market bars %s http %d", symbol, status)
	}
	return reply.GetBars(), nil
}

// doGet 发 GET，200 时按 protojson 解码；非 2xx（含 404 无截面）只回状态码不解码。
func (m *marketClient) doGet(ctx context.Context, path string, query url.Values, out proto.Message) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.addr+path+"?"+query.Encode(), nil)
	if err != nil {
		return 0, err
	}
	resp, err := m.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return 0, err
	}
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, nil
	}
	if err := protojson.Unmarshal(raw, out); err != nil {
		return resp.StatusCode, fmt.Errorf("market %s decode: %w", path, err)
	}
	return resp.StatusCode, nil
}
