package data

import (
	"context"
	"encoding/json"
	"errors"

	"server/app/datahub/internal/biz"
	"server/pkg/market"

	"github.com/go-redis/redis/v8"
)

const (
	keyMeta   = "snap:meta"
	keySector = "snap:sector"
	keyFlow   = "snap:flow"
)

// SnapshotStore 把实时数据写进 Redis。
type SnapshotStore struct {
	rdb *redis.Client
}

func NewSnapshotStore(rdb *redis.Client) *SnapshotStore { return &SnapshotStore{rdb: rdb} }

var _ biz.SnapshotStore = (*SnapshotStore)(nil)

func (s *SnapshotStore) WriteSnapshots(ctx context.Context, items []biz.Snapshot, meta biz.SnapshotMeta) error {
	fields := make([]interface{}, 0, len(items)*2)
	for _, it := range items {
		b, err := json.Marshal(it)
		if err != nil {
			return err
		}
		fields = append(fields, it.Symbol, b)
	}
	mb, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	pipe := s.rdb.TxPipeline()
	if len(fields) > 0 {
		pipe.HSet(ctx, market.SnapshotKey, fields...)
	}
	pipe.Set(ctx, keyMeta, mb, 0)
	_, err = pipe.Exec(ctx)
	return err
}

// MarkStale 把 meta 和每条快照都标成过期。M02、M08 按单条快照的 stale 判断能否使用。
func (s *SnapshotStore) MarkStale(ctx context.Context, meta biz.SnapshotMeta) error {
	meta.Stale = true
	all, err := s.rdb.HGetAll(ctx, market.SnapshotKey).Result()
	if err != nil {
		return err
	}
	fields := make([]interface{}, 0, len(all)*2)
	for k, v := range all {
		var snap market.Snapshot
		if json.Unmarshal([]byte(v), &snap) != nil || snap.Stale {
			continue
		}
		snap.Stale = true
		b, _ := json.Marshal(snap)
		fields = append(fields, k, b)
	}
	mb, _ := json.Marshal(meta)
	pipe := s.rdb.TxPipeline()
	if len(fields) > 0 {
		pipe.HSet(ctx, market.SnapshotKey, fields...)
	}
	pipe.Set(ctx, keyMeta, mb, 0)
	_, err = pipe.Exec(ctx)
	return err
}

func (s *SnapshotStore) Meta(ctx context.Context) (biz.SnapshotMeta, error) {
	var m biz.SnapshotMeta
	b, err := s.rdb.Get(ctx, keyMeta).Bytes()
	if errors.Is(err, redis.Nil) {
		return m, nil
	}
	if err != nil {
		return m, err
	}
	err = json.Unmarshal(b, &m)
	return m, err
}

func (s *SnapshotStore) WriteSectorQuotes(ctx context.Context, items []biz.SectorQuote) error {
	return s.hset(ctx, keySector, len(items), func(i int) (string, any) { return items[i].Code, items[i] })
}

func (s *SnapshotStore) WriteIntradayFlows(ctx context.Context, items []biz.IntradayFlow) error {
	return s.hset(ctx, keyFlow, len(items), func(i int) (string, any) { return items[i].Symbol, items[i] })
}

func (s *SnapshotStore) hset(ctx context.Context, key string, n int, at func(i int) (string, any)) error {
	if n == 0 {
		return nil
	}
	fields := make([]interface{}, 0, n*2)
	for i := 0; i < n; i++ {
		k, v := at(i)
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		fields = append(fields, k, b)
	}
	return s.rdb.HSet(ctx, key, fields...).Err()
}
