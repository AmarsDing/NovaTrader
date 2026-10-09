package data

import (
	"context"
	v1 "server/api/demo/v1"
	"server/conf"

	"github.com/go-kratos/kratos/contrib/registry/discovery/v2"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/middleware/recovery"
	"github.com/go-kratos/kratos/v2/middleware/tracing"
	"github.com/go-kratos/kratos/v2/registry"
	"github.com/go-kratos/kratos/v2/transport/grpc"
	"github.com/google/wire"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
)

var ProviderSet = wire.NewSet(NewData, NewConfigRepo, NewNatsCoreRepo, NewDiscovery, NewTopicRepo, NewConfigClient)

type Data struct {
	config v1.ConfigClient
	log    *log.Helper
}

func NewData(
	config v1.ConfigClient,
	logger log.Logger,
) *Data {
	return &Data{
		config: config,
		log:    log.NewHelper(log.With(logger, "optimization", "data")),
	}
}

// 客户端 discovery 注册
func NewDiscovery(conf *conf.Discovery) registry.Discovery {
	r := discovery.New(&discovery.Config{
		Nodes:  conf.Nodes,
		Env:    conf.Env,
		Region: conf.Region,
		Zone:   conf.Zone,
		Host:   conf.Host,
	})
	return r
}

// cfgcenter----Config
func NewConfigClient(r registry.Discovery, tp *tracesdk.TracerProvider) v1.ConfigClient {
	conn, err := grpc.DialInsecure(
		context.Background(),
		grpc.WithEndpoint("discovery:///demo"),
		grpc.WithDiscovery(r),
		grpc.WithMiddleware(
			tracing.Client(tracing.WithTracerProvider(tp)),
			recovery.Recovery(),
		),
	)
	if err != nil {
		panic(err)
	}
	return v1.NewConfigClient(conn)
}
