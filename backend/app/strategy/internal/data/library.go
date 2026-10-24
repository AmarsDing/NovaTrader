// library.go 是 B4 声明式策略读库入口：strategy_library 表唯一消费方
// （此前该表 code 字段全仓零引用）。只读 status=active 且 code 非空的行，
// 按 name 升序保证装载次序确定；解析与激活裁决全在 biz 层（绝不半激活）。
package data

import (
	"context"

	"server/app/strategy/internal/biz"
	"server/ent"
	"server/ent/strategylibrary"
)

type libraryRepo struct{ client *ent.Client }

// NewLibraryRepo 装配在册声明式策略仓储。Data/ent 为空时退化为空集
// （不可得即不用，不阻塞扫描主链）。
func NewLibraryRepo(d *Data) biz.LibraryRepo {
	if d == nil || d.client == nil {
		return &libraryRepo{}
	}
	return &libraryRepo{client: d.client}
}

func (r *libraryRepo) ActiveLibrary(ctx context.Context) ([]biz.LibraryRow, error) {
	if r.client == nil {
		return nil, nil
	}
	rows, err := r.client.StrategyLibrary.Query().
		Where(
			strategylibrary.StatusEQ("active"),
			strategylibrary.CodeNotNil(),
			strategylibrary.CodeNEQ(""),
		).
		Order(ent.Asc(strategylibrary.FieldName)).
		Select(strategylibrary.FieldName, strategylibrary.FieldCode).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]biz.LibraryRow, 0, len(rows))
	for _, row := range rows {
		code := ""
		if row.Code != nil {
			code = *row.Code
		}
		out = append(out, biz.LibraryRow{Name: row.Name, Code: code})
	}
	return out, nil
}
