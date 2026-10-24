package biz

import (
	"context"
	"strings"
	"time"

	"server/pkg/pinyin"
)

// SecurityBasicRow 是名称字典的一行（UI 显示方案 §6.2 / 附录 B-1）。
// 缺字段按零值透传（名称缺失为空串、估值为 0），由前端判 hasName，后端不伪造。
type SecurityBasicRow struct {
	Code       string
	Name       string
	Market     string // SH / SZ / BJ
	Industry   string
	Concepts   []string
	ST         bool
	Suspended  bool
	PeTtm      float64
	Pb         float64
	CircMv     float64
	// TotalMv 字典日级口径（stock_basic.total_mv 采集落数）；未采集=0，service 层 >0 才置 optional。
	// 与 Quote.total_mv 的盘中 last×total_share 即席口径不同，勿互相替代。
	TotalMv    float64
	UpdateTime time.Time
}

// SecurityBasics 名称字典查询：codes 去重、大写规整后直通数据层；空 codes 返回全量（数据层上限 6000 行）。
// 返回行与结果内最大 update_time（版本戳），无结果时 maxUpdate 为零值。
func (u *Usecase) SecurityBasics(ctx context.Context, codes []string) ([]SecurityBasicRow, time.Time, error) {
	if len(codes) > 0 {
		seen := make(map[string]struct{}, len(codes))
		dedup := make([]string, 0, len(codes))
		for _, raw := range codes {
			c := strings.ToUpper(strings.TrimSpace(raw))
			if c == "" {
				continue
			}
			if _, ok := seen[c]; ok {
				continue
			}
			seen[c] = struct{}{}
			dedup = append(dedup, c)
		}
		codes = dedup
	}
	return u.repo.SecurityBasics(ctx, codes)
}

// maxSearchResults 是 SearchSecurities 的返回上限（前端下拉面一页）。
const maxSearchResults = 50

// SecurityMatch 是一条联想命中。MatchKind 显式命中通道（code/name/pinyin），
// Pinyin 是名称拼音首字母串（诊断/前端高亮用，多音字错位属已知口径）。
type SecurityMatch struct {
	Code      string
	Name      string
	Market    string
	ST        bool
	Suspended bool
	MatchKind string
	Pinyin    string
}

// SearchSecurities 代码/名称/拼音联想（G2 后端先落）。
// 通道归组：code 前缀 → 名称前缀 → 名称子串 → 拼音首字母前缀；组内按代码序稳定。
// 复用名称字典全量读（数据层 6000 行上限自带闸门），载入后在内存匹配。
func (u *Usecase) SearchSecurities(ctx context.Context, query string, limit int) ([]SecurityMatch, error) {
	q := strings.ToUpper(strings.TrimSpace(query))
	if q == "" {
		return nil, nil
	}
	if limit <= 0 || limit > maxSearchResults {
		limit = 20
	}
	rows, _, err := u.repo.SecurityBasics(ctx, nil)
	if err != nil {
		return nil, err
	}
	codeQ := strings.ReplaceAll(q, ".", "")
	groups := [4][]SecurityMatch{}
	for _, r := range rows {
		if r.Code == "" || r.Name == "" {
			continue
		}
		py := pinyin.Initials(r.Name)
		m := SecurityMatch{Code: r.Code, Name: r.Name, Market: r.Market, ST: r.ST, Suspended: r.Suspended, Pinyin: py}
		plain := strings.ReplaceAll(r.Code, ".", "")
		switch {
		case strings.HasPrefix(plain, codeQ):
			groups[0] = append(groups[0], withKind(m, "code"))
		case strings.HasPrefix(r.Name, q):
			groups[1] = append(groups[1], withKind(m, "name"))
		case strings.Contains(r.Name, q):
			groups[2] = append(groups[2], withKind(m, "name"))
		case py != "" && strings.HasPrefix(py, strings.ToLower(q)):
			groups[3] = append(groups[3], withKind(m, "pinyin"))
		}
	}
	var out []SecurityMatch
	for _, g := range groups {
		for _, m := range g {
			if len(out) >= limit {
				return out, nil
			}
			out = append(out, m)
		}
	}
	return out, nil
}

func withKind(m SecurityMatch, kind string) SecurityMatch {
	m.MatchKind = kind
	return m
}
