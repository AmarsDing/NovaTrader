// NovaTrader admin：桌面端的 HTTP / gRPC / WebSocket 入口。
package main

import (
	"flag"
	"os"

	"server/app/admin/internal/gateway"
	"server/app/admin/internal/server"
	"server/conf"
	alog "server/utils/log"

	"github.com/go-kratos/kratos/v2"
	"github.com/go-kratos/kratos/v2/config"
	"github.com/go-kratos/kratos/v2/config/file"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/middleware/tracing"
	"github.com/go-kratos/kratos/v2/registry"
	"github.com/go-kratos/kratos/v2/transport/grpc"
	"github.com/go-kratos/kratos/v2/transport/http"
)

var (
	Name     = "admin"
	Version  string
	flagconf string
	id, _    = os.Hostname()
)

func init() {
	flag.StringVar(&flagconf, "conf", "../../../configs", "config path, eg: -conf config.yaml")
}

func newApp(logger log.Logger, hs *http.Server, gs *grpc.Server, rr registry.Registrar, ws *server.WebsocketServer) *kratos.App {
	return kratos.New(
		kratos.ID(id),
		kratos.Name(Name),
		kratos.Version(Version),
		kratos.Logger(logger),
		kratos.Server(hs, gs, ws),
		kratos.Registrar(rr),
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
		"trace.id", tracing.TraceID(),
		"span.id", tracing.SpanID(),
	)
	secret := ""
	if bc.GetAuth() != nil {
		secret = bc.GetAuth().GetKey()
	}
	upstream := ""
	if bc.GetAdmin() != nil {
		upstream = bc.GetAdmin().GetBacktestUpstream()
	}
	var users gateway.FileConfig
	if err := c.Value("admin").Scan(&users); err != nil {
		log.NewHelper(logger).Warnf("读取网关账号：%v", err)
	}
	settings := gateway.BuildSettings(users, upstream, secret)
	if bc.GetRedis() != nil {
		settings.RedisAddr = bc.GetRedis().GetAddr()
	}
	app, cleanup, err := wireApp(bc.GetAdmin(), bc.GetDiscovery(), bc.GetNats(), settings, bc.GetPostgres(), logger)
	if err != nil {
		panic(err)
	}
	defer cleanup()
	if err := app.Run(); err != nil {
		panic(err)
	}
}
