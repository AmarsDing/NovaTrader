// 启动前创建业务库。表结构仍由 ent 迁移创建。
//
//	go run ./cmd/bootstrap
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"time"

	"server/ent"
	"server/pkg/dbinit"
	"server/pkg/migrate"
	"server/pkg/tradecal"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	host := flag.String("host", "127.0.0.1", "postgres host")
	port := flag.Int("port", 5432, "postgres port")
	user := flag.String("user", "postgres", "postgres user")
	password := flag.String("password", "postgres", "postgres password")
	database := flag.String("database", "novatrader", "business database")
	ssl := flag.String("sslmode", "disable", "ssl mode")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cfg := dbinit.Config{
		Host:     *host,
		Port:     int32(*port),
		User:     *user,
		Password: *password,
		Database: *database,
		SSLMode:  *ssl,
		Timeout:  10 * time.Second,
	}
	err := dbinit.EnsureDatabase(ctx, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := dbinit.EnsureExtension(ctx, cfg, "vector"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := migrate.Schema(ctx, cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := syncCalendar(ctx, cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("database %s is ready\n", *database)
}

func syncCalendar(ctx context.Context, cfg dbinit.Config) error {
	dsn, err := dbinit.BusinessDSN(cfg)
	if err != nil {
		return err
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	defer client.Close()
	from := time.Date(2025, 1, 1, 0, 0, 0, 0, tradecal.Shanghai())
	to := time.Date(2026, 12, 31, 0, 0, 0, 0, tradecal.Shanghai())
	return tradecal.Default.Sync(ctx, client, from, to)
}
