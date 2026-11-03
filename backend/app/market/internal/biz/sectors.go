package biz

import (
	"context"
	"sort"
	"time"
)

// SectorMembershipRow 是一只股票在一个板块的有效归属（BE-05 反向查询的数据层行）。
type SectorMembershipRow struct {
	Symbol     string
	SectorCode string
	SectorName string
}

// SectorRefRow 板块引用（代码+名称），供 service 层组包；name 为空串表示板块名缺失。
type SectorRefRow struct {
	Code string
	Name string
}

// SectorMembership 返回 symbols 在 day 的有效板块归属，键为规整后的证券代码。
// 无归属的 symbol 对应空切片——「未归属」是正常态，不是错误；后端不编归属。
func (u *Usecase) SectorMembership(ctx context.Context, symbols []string, day time.Time) (map[string][]SectorRefRow, error) {
	rows, err := u.repo.SectorMembership(ctx, symbols, day)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]SectorRefRow, len(symbols))
	for _, sym := range symbols {
		out[sym] = []SectorRefRow{}
	}
	for _, r := range rows {
		out[r.Symbol] = append(out[r.Symbol], SectorRefRow{Code: r.SectorCode, Name: r.SectorName})
	}
	// 同一只股票的多个板块按代码排序，保证回包顺序确定。
	for sym := range out {
		sort.Slice(out[sym], func(i, j int) bool { return out[sym][i].Code < out[sym][j].Code })
	}
	return out, nil
}
