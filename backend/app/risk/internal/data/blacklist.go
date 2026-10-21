package data

import (
	"context"
	"time"

	"server/ent/stockblacklist"
	"server/pkg/symbol"
)

// Blacklist 实现 biz.Repo 的黑名单数据源：stock_blacklist 里 now 仍有效的行
// （expires_at 为空为永久），代码归一化为 Tongdaxin 形式（600000.SH）。
// 无法解析的存量行显式记日志跳过，不静默吞掉（数据纪律：取数异常不得伪装成空集）。
func (r *Repo) Blacklist(ctx context.Context, now time.Time) (map[string]bool, error) {
	codes, err := r.client.StockBlacklist.Query().
		Where(stockblacklist.Or(stockblacklist.ExpiresAtIsNil(), stockblacklist.ExpiresAtGT(now))).
		Select(stockblacklist.FieldStockCode).
		Strings(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(codes))
	for _, c := range codes {
		sym, err := symbol.Parse(c)
		if err != nil {
			r.log.Warnf("blacklist: skip invalid stock_code %q: %v", c, err)
			continue
		}
		out[sym.Tongdaxin()] = true
	}
	return out, nil
}
