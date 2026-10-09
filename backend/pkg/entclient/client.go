// Package entclient 打开各服务共用的 ent 客户端。
// Schema 仍在 backend/ent，不按服务复制。这里只负责建库、pgvector、迁移和连接池。
package entclient

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"server/ent"
	"server/pkg/dbinit"
	"server/pkg/migrate"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// openTimeout 与各服务原先的启动超时一致。调用方已设截止时间时沿用调用方的。
const openTimeout = 2 * time.Minute

// Config 是建库、迁移和连接池参数。DB 的字段含义见 dbinit.Config。
type Config struct {
	DB           dbinit.Config
	MaxOpenConns int
	MaxIdleConns int
	// SkipVector 为 true 时不执行 CREATE EXTENSION vector。
	// 共享 schema 含 vector 列，业务服务保持 false。
	SkipVector bool
}

// Conn 是 ent 客户端和它拥有的连接池。
// 关闭 Client 会关掉 DB，不要再单独 Close DB。
type Conn struct {
	Client *ent.Client
	DB     *sql.DB
}

// Close 关闭客户端及其连接池。
func (c *Conn) Close() error {
	if c == nil || c.Client == nil {
		return nil
	}
	return c.Client.Close()
}

// Open 按启动顺序建库、按需启用 pgvector、在建议锁下迁移，再打开业务连接。
func Open(ctx context.Context, cfg Config) (*Conn, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, openTimeout)
		defer cancel()
	}
	if err := dbinit.EnsureDatabase(ctx, cfg.DB); err != nil {
		return nil, err
	}
	if !cfg.SkipVector {
		if err := dbinit.EnsureExtension(ctx, cfg.DB, "vector"); err != nil {
			return nil, err
		}
	}
	if err := migrate.Schema(ctx, cfg.DB); err != nil {
		return nil, err
	}
	dsn, err := dbinit.BusinessDSN(cfg.DB)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("entclient: open: %w", err)
	}
	if cfg.MaxOpenConns > 0 {
		db.SetMaxOpenConns(cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns > 0 {
		db.SetMaxIdleConns(cfg.MaxIdleConns)
	}
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	return &Conn{Client: client, DB: db}, nil
}
