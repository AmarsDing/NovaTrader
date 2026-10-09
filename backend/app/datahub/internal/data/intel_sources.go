package data

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"server/app/datahub/internal/biz"

	"golang.org/x/text/encoding/simplifiedchinese"
)

var tagRe = regexp.MustCompile(`<[^>]+>`)

func plain(s string) string {
	return strings.TrimSpace(html.UnescapeString(tagRe.ReplaceAllString(s, "")))
}

// Cninfo 是巨潮资讯公告检索，官方披露平台，official=true。
type Cninfo struct {
	hc *httpc
}

func NewCninfo(opt HTTPOptions) *Cninfo {
	if opt.Concurrency <= 0 {
		opt.Concurrency = 2
	}
	return &Cninfo{hc: newHTTPC(biz.SourceCninfo, opt, map[string]string{
		"User-Agent":       "Mozilla/5.0",
		"X-Requested-With": "XMLHttpRequest",
	})}
}

func (c *Cninfo) Name() string { return biz.SourceCninfo }

// 不带 plate 时结果先按市场分组再按时间排，必须分市场查询才能按时间翻页。
var cninfoPlates = []struct{ column, plate string }{{"szse", "sz"}, {"sse", "sh"}, {"bj", "bj"}}

// Intel 只提供公告。每个市场按时间倒序翻页，翻到水位之前或最多 20 页为止。
func (c *Cninfo) Intel(ctx context.Context, kind string, q biz.IntelQuery) ([]biz.Intel, error) {
	if kind != biz.KindAnnouncement {
		return nil, biz.ErrUnsupported
	}
	from := q.Since
	if from.IsZero() {
		from = time.Now().Add(-24 * time.Hour)
	}
	var items []biz.Intel
	for _, p := range cninfoPlates {
		got, err := c.plate(ctx, p.column, p.plate, from)
		if err != nil {
			return nil, err
		}
		items = append(items, got...)
	}
	return items, nil
}

func (c *Cninfo) plate(ctx context.Context, column, plate string, from time.Time) ([]biz.Intel, error) {
	seDate := ymd(from) + "~" + ymd(time.Now())
	var items []biz.Intel
	for page := 1; page <= 20; page++ {
		form := url.Values{"pageNum": {strconv.Itoa(page)}, "pageSize": {"30"}, "column": {column}, "tabName": {"fulltext"},
			"plate": {plate}, "stock": {""}, "searchkey": {""}, "secid": {""}, "category": {""}, "trade": {""},
			"seDate": {seDate}, "sortName": {""}, "sortType": {""}, "isHLtitle": {"true"}}
		b, err := c.hc.postForm(ctx, "http://www.cninfo.com.cn/new/hisAnnouncement/query", form)
		if err != nil {
			return nil, err
		}
		var out struct {
			Announcements []struct {
				SecCode string `json:"secCode"`
				ID      string `json:"announcementId"`
				Title   string `json:"announcementTitle"`
				Time    int64  `json:"announcementTime"`
				URL     string `json:"adjunctUrl"`
			} `json:"announcements"`
			HasMore bool `json:"hasMore"`
		}
		if err := decodeJSON(biz.SourceCninfo, b, &out); err != nil {
			return nil, err
		}
		older := false
		for _, a := range out.Announcements {
			at := time.UnixMilli(a.Time).In(shanghaiLoc())
			if at.Before(from) {
				older = true
				continue
			}
			var codes []string
			if sym := secu(a.SecCode); sym != "" {
				codes = []string{sym}
			}
			items = append(items, biz.Intel{Source: biz.SourceCninfo, SourceID: a.ID, Kind: biz.KindAnnouncement, Title: plain(a.Title),
				URL: "http://static.cninfo.com.cn/" + a.URL, PublishTime: at.Format(time.RFC3339), Codes: codes})
		}
		if older || !out.HasMore || len(out.Announcements) == 0 {
			break
		}
	}
	return items, nil
}

// Tavily 是 Tavily 搜索 API，用于新闻。查询词来自配置 feeds。
type Tavily struct {
	hc      *httpc
	key     string
	queries []string
}

func NewTavily(key string, queries []string, opt HTTPOptions) *Tavily {
	if opt.Concurrency <= 0 {
		opt.Concurrency = 2
	}
	return &Tavily{key: key, queries: queries, hc: newHTTPC(biz.SourceTavily, opt, nil)}
}

func (t *Tavily) Name() string { return biz.SourceTavily }

func (t *Tavily) Intel(ctx context.Context, kind string, q biz.IntelQuery) ([]biz.Intel, error) {
	if kind != biz.KindNews {
		return nil, biz.ErrUnsupported
	}
	if t.key == "" {
		return nil, biz.Unavailable(biz.SourceTavily, "未配置 API key（datahub.sources.tavily.token）")
	}
	if len(t.queries) == 0 {
		return nil, biz.Unavailable(biz.SourceTavily, "未配置查询词（datahub.sources.tavily.feeds）")
	}
	var items []biz.Intel
	for _, query := range t.queries {
		b, err := t.hc.postJSON(ctx, "https://api.tavily.com/search", map[string]any{
			"api_key": t.key, "query": query, "topic": "news", "days": 1, "max_results": 20,
		})
		if err != nil {
			var he *httpError
			if errors.As(err, &he) && (he.Status == 401 || he.Status == 432) {
				return nil, biz.Unavailable(biz.SourceTavily, "API key 无效或额度用完（HTTP %d）", he.Status)
			}
			return nil, err
		}
		var out struct {
			Results []struct {
				Title         string `json:"title"`
				URL           string `json:"url"`
				Content       string `json:"content"`
				PublishedDate string `json:"published_date"`
			} `json:"results"`
		}
		if err := decodeJSON(biz.SourceTavily, b, &out); err != nil {
			return nil, err
		}
		for _, r := range out.Results {
			items = append(items, biz.Intel{Source: biz.SourceTavily, SourceID: hashID(r.URL), Kind: biz.KindNews, Title: r.Title,
				Content: r.Content, URL: r.URL, PublishTime: feedTime(r.PublishedDate)})
		}
	}
	return items, nil
}

// RSS 读 RSS 2.0 与 Atom 订阅，作为新闻源。地址来自配置 feeds。
type RSS struct {
	hc    *httpc
	feeds []string
}

func NewRSS(feeds []string, opt HTTPOptions) *RSS {
	if opt.Concurrency <= 0 {
		opt.Concurrency = 2
	}
	return &RSS{feeds: feeds, hc: newHTTPC(biz.SourceRSS, opt, map[string]string{"User-Agent": "Mozilla/5.0 NovaTrader"})}
}

func (r *RSS) Name() string { return biz.SourceRSS }

func (r *RSS) Intel(ctx context.Context, kind string, q biz.IntelQuery) ([]biz.Intel, error) {
	if kind != biz.KindNews {
		return nil, biz.ErrUnsupported
	}
	if len(r.feeds) == 0 {
		return nil, biz.Unavailable(biz.SourceRSS, "未配置订阅地址（datahub.sources.rss.feeds）")
	}
	var items []biz.Intel
	var errs []string
	for _, f := range r.feeds {
		b, err := r.hc.get(ctx, f)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		got, err := parseFeed(b)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", f, err))
			continue
		}
		items = append(items, got...)
	}
	// 部分订阅失败不算源失败，全部失败才算。
	if len(items) == 0 && len(errs) > 0 {
		return nil, fmt.Errorf("rss: %s", strings.Join(errs, "; "))
	}
	return items, nil
}

func parseFeed(b []byte) ([]biz.Intel, error) {
	var doc struct {
		Channel struct {
			Items []struct {
				Title       string `xml:"title"`
				Link        string `xml:"link"`
				Description string `xml:"description"`
				PubDate     string `xml:"pubDate"`
				GUID        string `xml:"guid"`
			} `xml:"item"`
		} `xml:"channel"`
		Entries []struct {
			Title   string `xml:"title"`
			ID      string `xml:"id"`
			Updated string `xml:"updated"`
			Summary string `xml:"summary"`
			Link    []struct {
				Href string `xml:"href,attr"`
			} `xml:"link"`
		} `xml:"entry"`
	}
	dec := xml.NewDecoder(bytes.NewReader(b))
	dec.CharsetReader = func(charset string, in io.Reader) (io.Reader, error) {
		switch strings.ToLower(charset) {
		case "gbk", "gb2312", "gb18030":
			return simplifiedchinese.GB18030.NewDecoder().Reader(in), nil
		}
		return in, nil
	}
	dec.Strict = false
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	var out []biz.Intel
	for _, it := range doc.Channel.Items {
		id := it.GUID
		if id == "" {
			id = it.Link
		}
		out = append(out, biz.Intel{Source: biz.SourceRSS, SourceID: hashID(id), Kind: biz.KindNews, Title: plain(it.Title),
			Content: plain(it.Description), URL: strings.TrimSpace(it.Link), PublishTime: feedTime(it.PubDate)})
	}
	for _, e := range doc.Entries {
		link := ""
		if len(e.Link) > 0 {
			link = e.Link[0].Href
		}
		id := e.ID
		if id == "" {
			id = link
		}
		out = append(out, biz.Intel{Source: biz.SourceRSS, SourceID: hashID(id), Kind: biz.KindNews, Title: plain(e.Title),
			Content: plain(e.Summary), URL: link, PublishTime: feedTime(e.Updated)})
	}
	return out, nil
}

func feedTime(s string) string {
	s = strings.TrimSpace(s)
	// 不带时区的时间按 UTC 处理，Tavily 与多数英文订阅如此。
	for _, layout := range []string{time.RFC1123Z, time.RFC1123, time.RFC3339, "Mon, 2 Jan 2006 15:04:05 -0700", "Mon, 2 Jan 2006 15:04:05 MST", "2006-01-02T15:04:05.999999", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.In(shanghaiLoc()).Format(time.RFC3339)
		}
	}
	return ""
}

func hashID(s string) string {
	h := sha1.Sum([]byte(strings.TrimSpace(s)))
	return hex.EncodeToString(h[:12])
}
