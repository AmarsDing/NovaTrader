// NovaTrader risk：M08 事前风控、盯盘、Kill Switch。trade 下单前必须调 CheckOrder。
//
//	go run ./app/risk/cmd -conf configs
package main

import (
	"flag"
	"os"

	"server/app/risk/internal/core"
	"server/conf"
	alog "server/utils/log"

	"github.com/go-kratos/kratos/v2"
	"github.com/go-kratos/kratos/v2/config"
	"github.com/go-kratos/kratos/v2/config/file"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/transport/grpc"
	"github.com/go-kratos/kratos/v2/transport/http"
)

var (
	Name     = "risk"
	Version  string
	flagconf string
	id, _    = os.Hostname()
)

func init() {
	flag.StringVar(&flagconf, "conf", "../../../configs", "config path, eg: -conf config.yaml")
}

// newApp 把 core 放在最前：它先从库里恢复 Kill Switch 和模式，恢复前 CheckOrder 一律拒绝。
func newApp(logger log.Logger, r *core.Runner, hs *http.Server, gs *grpc.Server) *kratos.App {
	return kratos.New(
		kratos.ID(id),
		kratos.Name(Name),
		kratos.Version(Version),
		kratos.Logger(logger),
		kratos.Server(r, hs, gs),
	)
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
	logger := alog.Logger(bc.Log,
		"ts", log.DefaultTimestamp,
		"caller", log.DefaultCaller,
		"service.id", id,
		"service.name", Name,
		"service.version", Version,
	)
	app, cleanup, err := wireApp(bc.GetRisk(), bc.GetPostgres(), bc.GetNats(), bc.GetRedis(), logger)
	if err != nil {
		panic(err)
	}
	defer cleanup()
	if err := app.Run(); err != nil {
		panic(err)
	}
}
