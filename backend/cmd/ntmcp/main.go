// NovaTrader nt-mcp：MCP 机器面（三仓联动 P3，2026-11-02）。
//
// 独立只读小进程——aranea 智能体底座经 trpc-mcp-go 消费四个只读工具
// （query_market_snapshot / get_positions / list_trade_signals /
// get_backtest_report）。下单/改仓/审批一律走 M09 风控链与人工审批，
// 不进 MCP（决策红线）。
//
// 鉴权：静态 Bearer——token_ref 走 env:/enc:/明文三写法经
// pkg/llm.ResolveAPIKey 启动时解析（解析失败/空值即拒启动；轮换=改引用+重启，
// 与 pkg/harness 同纪律）。yaml 本身零密钥。
//
//	go run ./cmd/ntmcp -conf ../../configs
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/go-kratos/kratos/v2/config"
	"github.com/go-kratos/kratos/v2/config/file"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-redis/redis/v8"
	_ "github.com/jackc/pgx/v5/stdlib"

	"server/conf"
	"server/ent"
	"server/pkg/dbinit"
	"server/pkg/llm"
	"server/pkg/ntmcp"
	alog "server/utils/log"
)

var flagconf string

func init() {
	flag.StringVar(&flagconf, "conf", "../../configs", "config path, eg: -conf configs")
}

func main() {
	flag.Parse()

	c := config.New(config.WithSource(file.NewSource(flagconf)))
	defer c.Close()
	if err := c.Load(); err != nil {
		panic(err)
	}
	var bc conf.Bootstrap
	if err := c.Scan(&bc); err != nil {
		panic(err)
	}

	var mc ntmcp.Conf
	_ = c.Value("ntmcp").Scan(&mc) // 键缺失时保留零值，Normalize 兜底安全默认（enabled=false）
	mc.Normalize()
	if err := mc.Validate(); err != nil {
		panic(err)
	}
	logger := alog.Logger(bc.GetLog(), "service.name", "ntmcp")
	if !mc.Enabled {
		// 默认关：进程无事可做，干净退出（部署窗口置 true + 配 token_ref 后再跑）。
		logger.Log(log.LevelInfo, "msg", "nt-mcp disabled (ntmcp.enabled=false); exiting")
		return
	}

	// 只读 ent 直连：绕过 entclient.Open 的建库/迁移——schema 归属各业务服务，
	// 本进程只查不写（Store 只有 Query 链）。
	entClient, closeDB, err := openReadOnlyEnt(bc.GetPostgres())
	if err != nil {
		panic(err)
	}
	defer closeDB()

	rdb, closeRedis, err := openRedis(bc.GetRedis())
	if err != nil {
		panic(err)
	}
	defer closeRedis()

	// Bearer token 在 NewServer 内启动时解析（失败/空值即拒启动——「未配置」
	// 绝不等于「放行」；轮换 = 改 env/enc 引用 + 重启，与 pkg/harness 同纪律）。
	srv, err := ntmcp.NewServer(&mc, llm.ResolveAPIKey, ntmcp.ToolDeps{
		Snapshots:   ntmcp.NewRedisSnapshots(rdb, logger),
		Positions:   ntmcp.NewStore(entClient),
		Signals:     ntmcp.NewStore(entClient),
		Reports:     ntmcp.NewStore(entClient),
		MaxPageSize: int32(mc.PageSizeMax),
		Log:         logger,
	}, nil, logger)
	if err != nil {
		panic(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start() }()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Stop(shutdownCtx); err != nil {
			logger.Log(log.LevelError, "msg", "nt-mcp shutdown", "err", err)
		}
	case err := <-errCh:
		if err != nil {
			logger.Log(log.LevelError, "msg", "nt-mcp serve", "err", err)
			os.Exit(1)
		}
	}
}

// openReadOnlyEnt 只读 PG 连接（不建库、不迁移；池上限沿用 postgres 配置）。
func openReadOnlyEnt(c *conf.Postgres) (*ent.Client, func(), error) {
	if c == nil {
		return nil, nil, fmt.Errorf("postgres config is missing")
	}
	cfg := dbinit.Config{
		DSN: c.GetDsn(), Host: c.GetHost(), Port: c.GetPort(),
		User: c.GetUser(), Password: c.GetPassword(), Database: c.GetDatabase(), SSLMode: c.GetSslMode(),
	}
	if d := c.GetDialTimeout(); d != nil {
		cfg.Timeout = d.AsDuration()
	}
	dsn, err := dbinit.BusinessDSN(cfg)
	if err != nil {
		return nil, nil, err
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("ntmcp: open pg: %w", err)
	}
	if n := int(c.GetMaxOpenConns()); n > 0 {
		db.SetMaxOpenConns(n)
	}
	if n := int(c.GetMaxIdleConns()); n > 0 {
		db.SetMaxIdleConns(n)
	}
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	return client, func() { _ = client.Close() }, nil
}

// openRedis 快照 Redis 连接（与 app/market data.NewRedis 同口径：
// 全市场 HGETALL 约 2MB，读超时保底 2s）。
func openRedis(c *conf.Redis) (*redis.Client, func(), error) {
	if c == nil || c.GetAddr() == "" {
		return nil, nil, fmt.Errorf("redis config is missing")
	}
	opt := &redis.Options{Addr: c.GetAddr()}
	if d := c.GetReadTimeout(); d != nil {
		opt.ReadTimeout = maxDuration(d.AsDuration(), 2*time.Second)
	}
	if d := c.GetWriteTimeout(); d != nil {
		opt.WriteTimeout = d.AsDuration()
	}
	rdb := redis.NewClient(opt)
	return rdb, func() { _ = rdb.Close() }, nil
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}
