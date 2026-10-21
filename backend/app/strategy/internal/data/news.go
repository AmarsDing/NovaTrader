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

	intelv1 "server/api/intel/v1"
	"server/app/strategy/internal/biz"
	"server/conf"

	"github.com/go-kratos/kratos/v2/log"
	"google.golang.org/protobuf/encoding/protojson"
)

// M13-T01：strategy → intel(2031) 的只读 HTTP 客户端，查当日突发利空。
// M03 没有「全市场当日利空」查询口，只有按代码的时间线（Timeline），
// 所以这里逐代码查询后按与旧 news_sentiment 直读相同的阈值过滤
// （importance>=4 且 sentiment<=-0.5，24h 窗口），并发受限。
//
// 数据纪律：地址未配置或某只查询失败把该代码放进 missing——空集（确无利空）
// 与「未取到」严格区分，消费方对 missing 覆盖的代码不得按无利空推断。

const (
	newsConcurrency = 4
	newsWindow      = 24 * time.Hour
	newsLimit       = 200
	newsImportance  = 4    // 重要度达到即算突发利空（与原 badNewsLevel 一致）
	newsSentiment   = -0.5 // 情感不高于此值（与原 badNewsScore 一致）
)

// errNoIntelAddr 表示 strategy.intel_addr 未配置。
var errNoIntelAddr = errors.New("strategy: intel_addr is empty")

type newsClient struct {
	addr string
	http *http.Client
	log  *log.Helper
}

// NewNewsClient 装配 biz.NewsSource。addr 为空时所有代码都报 missing（显式缺失）。
func NewNewsClient(c *conf.Strategy, logger log.Logger) biz.NewsSource {
	addr := ""
	if c != nil {
		addr = strings.TrimRight(c.GetIntelAddr(), "/")
	}
	return &newsClient{
		addr: addr,
		http: &http.Client{Timeout: 10 * time.Second},
		log:  log.NewHelper(log.With(logger, "module", "strategy/news-client")),
	}
}

// Negative 返回 symbols 中当日有突发利空的代码集。missing 是没查成的代码；
// err 只在整体不可用（地址未配置）时出现，单只失败收敛进 missing，不拖累整轮。
func (n *newsClient) Negative(ctx context.Context, symbols []string, asOf time.Time) (map[string]bool, map[string]bool, error) {
	set := map[string]bool{}
	missing := map[string]bool{}
	if len(symbols) == 0 {
		return set, missing, nil
	}
	if n == nil || n.addr == "" {
		for _, s := range symbols {
			missing[s] = true
		}
		return set, missing, errNoIntelAddr
	}
	since := asOf.Add(-newsWindow)
	until := asOf
	sem := make(chan struct{}, newsConcurrency)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, sym := range symbols {
		wg.Add(1)
		sem <- struct{}{}
		go func(sym string) {
			defer wg.Done()
			defer func() { <-sem }()
			hit, err := n.negativeOne(ctx, sym, since, until)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err != nil:
				n.log.Warnf("intel timeline %s: %v", sym, err)
				missing[sym] = true
			case hit:
				set[sym] = true
			}
		}(sym)
	}
	wg.Wait()
	return set, missing, nil
}

// negativeOne 查一只代码 [from, to] 时间线，有任一 importance>=4 且 sentiment<=-0.5
// 的条目即算突发利空。404（无该股情报）与空时间线一样——确无利空，不是缺失。
func (n *newsClient) negativeOne(ctx context.Context, symbol string, from, to time.Time) (bool, error) {
	q := url.Values{
		"since": {from.Format(time.RFC3339)},
		"until": {to.Format(time.RFC3339)},
		"limit": {fmt.Sprint(newsLimit)},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		n.addr+"/v1/intel/stocks/"+url.PathEscape(symbol)+"/timeline?"+q.Encode(), nil)
	if err != nil {
		return false, err
	}
	resp, err := n.http.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return false, err
	}
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("intel timeline %s http %d", symbol, resp.StatusCode)
	}
	var reply intelv1.TimelineReply
	if err := protojson.Unmarshal(raw, &reply); err != nil {
		return false, fmt.Errorf("intel timeline %s decode: %w", symbol, err)
	}
	for _, it := range reply.GetItems() {
		pub, perr := time.Parse(time.RFC3339, it.GetPublishTime())
		if perr != nil || pub.Before(from) || pub.After(to) {
			continue
		}
		if it.GetImportance() >= newsImportance && it.GetSentiment() <= newsSentiment {
			return true, nil
		}
	}
	return false, nil
}
