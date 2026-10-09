package data

import (
	"context"

	"server/app/risk/internal/biz"
	"server/pkg/market"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-redis/redis/v8"
)

// Quotes 按代码读 Redis snap:latest。
type Quotes struct {
	rdb *redis.Client
	log *log.Helper
}

func NewQuotes(rdb *redis.Client, logger log.Logger) *Quotes {
	return &Quotes{rdb: rdb, log: log.NewHelper(log.With(logger, "module", "risk/quotes"))}
}

// Quotes 返回找得到且能解析的快照；缺的代码不在结果里。
func (q *Quotes) Quotes(ctx context.Context, symbols []string) (map[string]biz.Snapshot, error) {
	out := make(map[string]biz.Snapshot, len(symbols))
	if len(symbols) == 0 {
		return out, nil
	}
	vals, err := q.rdb.HMGet(ctx, market.SnapshotKey, symbols...).Result()
	if err != nil {
		return nil, err
	}
	for i, v := range vals {
		raw, ok := v.(string)
		if !ok {
			continue
		}
		s, err := market.ParseSnapshot([]byte(raw))
		if err != nil {
			q.log.Warnf("snap:latest %s: %v", symbols[i], err)
			continue
		}
		bid, _ := s.Bid1()
		ask, _ := s.Ask1()
		out[symbols[i]] = biz.Snapshot{
			Symbol: symbols[i], Time: s.Time, AsOf: s.AsOf, Stale: s.Stale,
			PreClose: s.PreClose, Last: s.Last, Amount: s.Amount, Bid1: bid, Ask1: ask,
		}
	}
	return out, nil
}
