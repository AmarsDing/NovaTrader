package data

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"server/app/datahub/internal/biz"
)

// Sina7x24 是新浪财经 7×24 快讯副源（zhibo feed），只提供 flash。
// 与行情快照源 "sina" 分名注册：流水线按 Name() 建 sources map，
// 重名会互覆断路/健康/配额状态。反爬要求带 Referer，经 newHTTPC headers 注入。
// 单请求首页不翻页：水位/overlap 增量由 collect_intel 的 seenSet+水位兜住。
// 去重口径：同源自去重用 feed id 作 SourceID（"flash|sina7x24|<id>" 与东财键不撞）；
// 跨源不打跨源去重键、按 source 标签共存（链式 fallback 下东财健康时副线本不触发）。
type Sina7x24 struct {
	hc      *httpc
	zhiboID string
	base    string
}

const sina7x24Feed = "https://zhibo.sina.com.cn/api/zhibo/feed"

// NewSina7x24 feeds[0] 非空时作 zhibo_id 覆盖（配置槽复用，不动 conf.proto），缺省 152（A股快讯）。
func NewSina7x24(feeds []string, opt HTTPOptions) *Sina7x24 {
	if opt.Concurrency <= 0 {
		opt.Concurrency = 2
	}
	zhiboID := "152"
	if len(feeds) > 0 && strings.TrimSpace(feeds[0]) != "" {
		zhiboID = strings.TrimSpace(feeds[0])
	}
	return &Sina7x24{zhiboID: zhiboID, base: sina7x24Feed, hc: newHTTPC(biz.SourceSina7x24, opt, map[string]string{
		"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36",
		"Referer":    "https://finance.sina.com.cn",
	})}
}

func (s *Sina7x24) Name() string { return biz.SourceSina7x24 }

// Intel 只拉快讯（kind=flash），其余类别 ErrUnsupported 让流水线跳过。
func (s *Sina7x24) Intel(ctx context.Context, kind string, q biz.IntelQuery) ([]biz.Intel, error) {
	if kind != biz.KindFlash {
		return nil, biz.ErrUnsupported
	}
	size := q.Limit
	if size < 20 {
		size = 20
	}
	if size > 50 {
		size = 50
	}
	u := s.base + "?" + url.Values{"page": {"1"}, "page_size": {strconv.Itoa(size)}, "zhibo_id": {s.zhiboID}}.Encode()
	b, err := s.hc.get(ctx, u)
	if err != nil {
		return nil, err
	}
	var out struct {
		Result struct {
			Status struct {
				Code int    `json:"code"`
				Msg  string `json:"msg"`
			} `json:"status"`
			Data struct {
				Feed struct {
					List []struct {
						ID         int64  `json:"id"`
						RichText   string `json:"rich_text"`
						CreateTime string `json:"create_time"`
						IsDelete   int    `json:"is_delete"`
						Ext        string `json:"ext"`
					} `json:"list"`
				} `json:"feed"`
			} `json:"data"`
		} `json:"result"`
	}
	if err := decodeJSON(biz.SourceSina7x24, b, &out); err != nil {
		return nil, err
	}
	if out.Result.Status.Code != 0 {
		return nil, fmt.Errorf("%s: zhibo status %d: %s", biz.SourceSina7x24, out.Result.Status.Code, out.Result.Status.Msg)
	}
	items := make([]biz.Intel, 0, len(out.Result.Data.Feed.List))
	for _, f := range out.Result.Data.Feed.List {
		// 数据纪律：删除标记、正文为空、发布时间解析失败一律 skip，绝不用 now 冒充发布时间。
		if f.IsDelete == 1 {
			continue
		}
		content := plain(f.RichText)
		if content == "" {
			continue
		}
		pub := shTime(f.CreateTime)
		if pub == "" {
			continue
		}
		docurl, codes := sinaExt(f.Ext)
		items = append(items, biz.Intel{Source: biz.SourceSina7x24, SourceID: strconv.FormatInt(f.ID, 10), Kind: biz.KindFlash,
			Title: truncate(content, 60), Content: content, URL: docurl, PublishTime: pub, Codes: codes})
	}
	return items, nil
}

// sinaExt 二次解析 ext 内嵌 JSON：docurl → 原文链接；stocks 去 sh/sz 前缀喂 secu() → Codes。
// 宽容解析：结构不符或代码解不出即丢，不挡正文（ext.stocks 真实样本未核实，见设计 open_questions）。
func sinaExt(raw string) (string, []string) {
	if raw == "" {
		return "", nil
	}
	var ext struct {
		DocURL string            `json:"docurl"`
		Stocks []json.RawMessage `json:"stocks"`
	}
	if json.Unmarshal([]byte(raw), &ext) != nil {
		return "", nil
	}
	var codes []string
	for _, rawStock := range ext.Stocks {
		var code string
		var obj struct {
			Symbol string `json:"symbol"`
			Code   string `json:"code"`
		}
		switch {
		case json.Unmarshal(rawStock, &code) == nil:
		case json.Unmarshal(rawStock, &obj) == nil:
			code = obj.Symbol
			if code == "" {
				code = obj.Code
			}
		default:
			continue
		}
		code = strings.TrimSpace(code)
		// 形如 sh600519 / sz000001：去市场前缀留 6 位代码交 secu()。
		if len(code) == 8 {
			prefix := strings.ToLower(code[:2])
			if prefix == "sh" || prefix == "sz" || prefix == "bj" {
				code = code[2:]
			}
		}
		if sym := secu(code); sym != "" {
			codes = append(codes, sym)
		}
	}
	return ext.DocURL, codes
}
