package data

import (
	"context"
	"server/app/admin/internal/gateway"
	"server/ent"
	"server/ent/strategyversion"
	"server/pkg/params"
	"time"
)

func (s *AuditStore) ReadConfig(ctx context.Context) (params.Snapshot, error) {
	return params.Read(ctx, s.client)
}
func (s *AuditStore) SaveConfig(ctx context.Context, base int, values map[string]string) (params.Snapshot, error) {
	return params.SaveCAS(ctx, s.client, base, values, "versioned configuration")
}
func (s *AuditStore) VersionConfig(ctx context.Context, id int) (params.Snapshot, error) {
	return params.Version(ctx, s.client, id)
}
func (s *AuditStore) RollbackConfig(ctx context.Context, base, target int) (params.Snapshot, error) {
	return params.RollbackCAS(ctx, s.client, base, target)
}
func (s *AuditStore) ConfigReceipts(ctx context.Context) ([]params.Receipt, error) {
	return params.Receipts(ctx, s.client)
}
func (s *AuditStore) ConfigHistory(ctx context.Context) ([]gateway.ConfigVersion, error) {
	rows, err := s.client.StrategyVersion.Query().Order(ent.Desc(strategyversion.FieldID)).Limit(20).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]gateway.ConfigVersion, 0, len(rows))
	for _, r := range rows {
		out = append(out, gateway.ConfigVersion{Revision: r.ID, CreatedAt: r.CreatedAt.Format(time.RFC3339Nano), Active: r.IsActive})
	}
	return out, nil
}
