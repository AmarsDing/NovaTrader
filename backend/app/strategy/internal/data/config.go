package data

import (
	"context"
	"server/pkg/params"
)

func (r *paramRepo) ConfigSnapshot(ctx context.Context) (params.Snapshot, error) {
	return params.Read(ctx, r.d.client)
}
func (r *paramRepo) RecordConfig(ctx context.Context, revision int, status string) error {
	return params.Record(ctx, r.d.client, "strategy", "scan", revision, status)
}
