// Package dbinit 在服务启动时创建业务库。建库语句无法参数化，库名只允许字母、数字和下划线。
// 业务表仍由 ent Schema.Create 创建，不在这里写建表 SQL。
package dbinit

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// Config 对应 conf.Postgres 的连接字段。DSN 非空时优先使用。
type Config struct {
	DSN      string
	Host     string
	Port     int32
	User     string
	Password string
	Database string
	SSLMode  string
	Timeout  time.Duration
}

var dbNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// EnsureDatabase 连接 postgres 系统库，在业务库不存在时创建它。
func EnsureDatabase(ctx context.Context, cfg Config) error {
	if !dbNamePattern.MatchString(cfg.Database) && cfg.DSN == "" {
		return fmt.Errorf("dbinit: invalid database name %q", cfg.Database)
	}
	dbName, adminDSN, err := splitAdminDSN(cfg)
	if err != nil {
		return err
	}
	if !dbNamePattern.MatchString(dbName) {
		return fmt.Errorf("dbinit: invalid database name %q", dbName)
	}
	db, err := sql.Open("pgx", adminDSN)
	if err != nil {
		return fmt.Errorf("dbinit: open admin: %w", err)
	}
	defer db.Close()
	pingCtx, cancel := context.WithTimeout(ctx, timeoutOr(cfg.Timeout))
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		return fmt.Errorf("dbinit: ping postgres: %w", err)
	}
	var exists bool
	if err := db.QueryRowContext(pingCtx, `SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)`, dbName).Scan(&exists); err != nil {
		return fmt.Errorf("dbinit: check database: %w", err)
	}
	if exists {
		return nil
	}
	if _, err := db.ExecContext(pingCtx, `CREATE DATABASE `+dbName); err != nil {
		return fmt.Errorf("dbinit: create database: %w", err)
	}
	return nil
}

var extensionName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// EnsureExtension 在业务库中创建扩展。名称只允许小写字母、数字和下划线。
func EnsureExtension(ctx context.Context, cfg Config, name string) error {
	if !extensionName.MatchString(name) {
		return fmt.Errorf("dbinit: invalid extension %q", name)
	}
	dsn, err := businessDSN(cfg)
	if err != nil {
		return err
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("dbinit: open business: %w", err)
	}
	defer db.Close()
	pingCtx, cancel := context.WithTimeout(ctx, timeoutOr(cfg.Timeout))
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		return fmt.Errorf("dbinit: ping business: %w", err)
	}
	if _, err := db.ExecContext(pingCtx, `CREATE EXTENSION IF NOT EXISTS `+name); err != nil {
		return fmt.Errorf("dbinit: create extension %s: %w", name, err)
	}
	return nil
}

// MigrationLockKey 是迁移用的会话级建议锁。多服务同时启动时只有一个能建表。
const MigrationLockKey int64 = 740000001

// WithAdvisoryLock 在业务库连接上持有建议锁，直到 fn 返回。
func WithAdvisoryLock(ctx context.Context, cfg Config, fn func(context.Context, *sql.DB) error) error {
	dsn, err := businessDSN(cfg)
	if err != nil {
		return err
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("dbinit: open business: %w", err)
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("dbinit: conn: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, MigrationLockKey); err != nil {
		return fmt.Errorf("dbinit: lock: %w", err)
	}
	defer conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, MigrationLockKey)
	return fn(ctx, db)
}

// BusinessDSN 返回业务库连接串，供 ent 打开连接。
func BusinessDSN(cfg Config) (string, error) {
	return businessDSN(cfg)
}

func businessDSN(cfg Config) (string, error) {
	if cfg.DSN != "" {
		return cfg.DSN, nil
	}
	_, admin, err := splitAdminDSN(cfg)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(admin)
	if err != nil {
		return "", err
	}
	u.Path = "/" + cfg.Database
	return u.String(), nil
}

func timeoutOr(d time.Duration) time.Duration {
	if d <= 0 {
		return 5 * time.Second
	}
	return d
}

func splitAdminDSN(cfg Config) (dbName, adminDSN string, err error) {
	if cfg.DSN != "" {
		u, err := url.Parse(cfg.DSN)
		if err != nil {
			return "", "", fmt.Errorf("dbinit: parse dsn: %w", err)
		}
		dbName = stringsTrimSlash(u.Path)
		u.Path = "/postgres"
		return dbName, u.String(), nil
	}
	if cfg.Host == "" || cfg.User == "" || cfg.Database == "" {
		return "", "", fmt.Errorf("dbinit: host, user and database are required")
	}
	port := cfg.Port
	if port == 0 {
		port = 5432
	}
	ssl := cfg.SSLMode
	if ssl == "" {
		ssl = "disable"
	}
	u := &url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(cfg.User, cfg.Password),
		Host:     net.JoinHostPort(cfg.Host, strconv.Itoa(int(port))),
		Path:     "/postgres",
		RawQuery: "sslmode=" + url.QueryEscape(ssl),
	}
	return cfg.Database, u.String(), nil
}

func stringsTrimSlash(path string) string {
	for len(path) > 0 && path[0] == '/' {
		path = path[1:]
	}
	return path
}
