// Package migrate 用 ent 在业务库上建表。只追加，不删列。
// 建表前先拿建议锁，避免多个服务同时迁移。
package migrate

import (
	"context"
	"database/sql"
	"fmt"

	"server/ent"
	"server/pkg/dbinit"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// Schema 连接业务库并执行 ent 自动迁移。
func Schema(ctx context.Context, cfg dbinit.Config) error {
	return dbinit.WithAdvisoryLock(ctx, cfg, func(ctx context.Context, db *sql.DB) error {
		if err := db.PingContext(ctx); err != nil {
			return fmt.Errorf("migrate: ping: %w", err)
		}
		drv := entsql.OpenDB(dialect.Postgres, db)
		client := ent.NewClient(ent.Driver(drv))
		defer client.Close()
		if err := client.Schema.Create(ctx); err != nil {
			return fmt.Errorf("migrate: schema: %w", err)
		}
		return nil
	})
}
