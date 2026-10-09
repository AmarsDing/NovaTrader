// Package server 是 market 的 HTTP（2021）与 gRPC（2022）入口。
package server

import (
	"net/http"

	v1 "server/api/market/v1"
	"server/app/market/internal/service"
	"server/conf"
	"server/pkg/metrics"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/middleware/logging"
	"github.com/go-kratos/kratos/v2/middleware/recovery"
	"github.com/go-kratos/kratos/v2/transport/grpc"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	"github.com/google/wire"
	ggrpc "google.golang.org/grpc"
)

var ProviderSet = wire.NewSet(NewHTTPServer, NewGRPCServer)

// 全市场因子截面约 5000 行 × 60 键，超过 gRPC 默认 4 MB。
const maxMsgBytes = 64 << 20

func NewHTTPServer(c *conf.Market, svc *service.MarketService, logger log.Logger) *khttp.Server {
	opts := []khttp.ServerOption{
		khttp.Middleware(recovery.Recovery(), logging.Server(logger)),
		khttp.Address(":2021"),
	}
	if h := c.GetHttp(); h != nil {
		if h.Network != "" {
			opts = append(opts, khttp.Network(h.Network))
		}
		if h.Addr != "" {
			opts = append(opts, khttp.Address(h.Addr))
		}
		if h.Timeout != nil {
			opts = append(opts, khttp.Timeout(h.Timeout.AsDuration()))
		}
	}
	srv := khttp.NewServer(opts...)
	srv.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	srv.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_ = metrics.Write(w)
	})
	v1.RegisterMarketHTTPServer(srv, svc)
	return srv
}

func NewGRPCServer(c *conf.Market, svc *service.MarketService, logger log.Logger) *grpc.Server {
	opts := []grpc.ServerOption{
		grpc.Middleware(recovery.Recovery(), logging.Server(logger)),
		grpc.Address(":2022"),
		grpc.Options(ggrpc.MaxSendMsgSize(maxMsgBytes)),
	}
	if g := c.GetGrpc(); g != nil {
		if g.Network != "" {
			opts = append(opts, grpc.Network(g.Network))
		}
		if g.Addr != "" {
			opts = append(opts, grpc.Address(g.Addr))
		}
		if g.Timeout != nil {
			opts = append(opts, grpc.Timeout(g.Timeout.AsDuration()))
		}
	}
	srv := grpc.NewServer(opts...)
	v1.RegisterMarketServer(srv, svc)
	return srv
}
