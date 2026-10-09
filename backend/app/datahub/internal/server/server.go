package server

import (
	"net/http"

	v1 "server/api/datahub/v1"
	"server/app/datahub/internal/service"
	"server/conf"
	"server/pkg/metrics"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/middleware/logging"
	"github.com/go-kratos/kratos/v2/middleware/recovery"
	"github.com/go-kratos/kratos/v2/transport/grpc"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	"github.com/google/wire"
)

var ProviderSet = wire.NewSet(NewHTTPServer, NewGRPCServer)

func NewHTTPServer(c *conf.Datahub, svc *service.DatahubService, logger log.Logger) *khttp.Server {
	opts := []khttp.ServerOption{
		khttp.Middleware(recovery.Recovery(), logging.Server(logger)),
		khttp.Address(":2011"),
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
	v1.RegisterDatahubHTTPServer(srv, svc)
	return srv
}

func NewGRPCServer(c *conf.Datahub, svc *service.DatahubService, logger log.Logger) *grpc.Server {
	opts := []grpc.ServerOption{
		grpc.Middleware(recovery.Recovery(), logging.Server(logger)),
		grpc.Address(":2012"),
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
	v1.RegisterDatahubServer(srv, svc)
	return srv
}
