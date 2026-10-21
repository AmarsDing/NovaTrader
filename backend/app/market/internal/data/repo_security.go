package data

import (
	"context"
	"strings"
	"time"

	"server/app/market/internal/biz"
	"server/ent/stockbasic"
)

// securityBasicsLimit 是名称字典单次查询的行数上限（全量 A 股约 5400 只）。
const securityBasicsLimit = 6000

// SecurityBasics 读 stock_basic 名称字典：codes 为空返回全量，非空按代码过滤，单次上限 6000 行。
// 返回行（按 stock_code 升序）与结果内最大 update_time；无结果时 maxUpdate 为零值，不是错误。
// 缺字段按零值透传（无行业/概念为空、估值为 0），前端判 hasName，后端不伪造。
func (r *Repo) SecurityBasics(ctx context.Context, codes []string) ([]biz.SecurityBasicRow, time.Time, error) {
	q := r.db.StockBasic.Query()
	if len(codes) > 0 {
		q = q.Where(stockbasic.StockCodeIn(codes...))
	}
	rows, err := q.Order(stockbasic.ByStockCode()).Limit(securityBasicsLimit).All(ctx)
	if err != nil {
		return nil, time.Time{}, err
	}
	out := make([]biz.SecurityBasicRow, 0, len(rows))
	var maxUpdate time.Time
	for _, row := range rows {
		item := biz.SecurityBasicRow{
			Code: row.StockCode, Name: row.StockName, Market: row.Market,
			ST: row.StFlag, Suspended: row.SuspendFlag,
			PeTtm: f64(row.PeTtm), Pb: f64(row.Pb), CircMv: f64(row.CircMv),
			UpdateTime: row.UpdateTime,
		}
		if row.Industry != nil {
			item.Industry = *row.Industry
		}
		if row.Concept != nil {
			item.Concepts = splitConcepts(*row.Concept)
		}
		if row.UpdateTime.After(maxUpdate) {
			maxUpdate = row.UpdateTime
		}
		out = append(out, item)
	}
	return out, maxUpdate, nil
}

// splitConcepts 把 stock_basic.concept 的逗号分隔文本拆成列表；空串返回空列表。
func splitConcepts(raw string) []string {
	var out []string
	for _, c := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == '，' }) {
		if c = strings.TrimSpace(c); c != "" {
			out = append(out, c)
		}
	}
	return out
}
