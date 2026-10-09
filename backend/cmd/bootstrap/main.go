// 启动前创建业务库。表结构仍由 ent 迁移创建。
//
//	go run ./cmd/bootstrap
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"server/pkg/dbinit"
	"server/pkg/entclient"
	"server/pkg/tradecal"
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
	conn, err := entclient.Open(ctx, entclient.Config{DB: cfg})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer conn.Close()
	from := time.Date(2025, 1, 1, 0, 0, 0, 0, tradecal.Shanghai())
	to := time.Date(2026, 12, 31, 0, 0, 0, 0, tradecal.Shanghai())
	if err := tradecal.Default.Sync(ctx, conn.Client, from, to); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("database %s is ready\n", *database)
}
