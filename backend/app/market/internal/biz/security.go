package biz

import (
	"context"
	"strings"
	"time"
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
