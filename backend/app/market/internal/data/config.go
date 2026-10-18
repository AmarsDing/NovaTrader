package data

import (
	"context"
	"server/pkg/params"
)

func (r *Repo) ConfigSnapshot(ctx context.Context) (params.Snapshot, error) {
	return params.Read(ctx, r.db)
}
func (r *Repo) RecordConfig(ctx context.Context, revision int, status string) error {
	return params.Record(ctx, r.db, "market", "warmup", revision, status)
}
