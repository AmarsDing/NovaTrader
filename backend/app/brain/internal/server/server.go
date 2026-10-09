// Package server 是 brain 的 HTTP 2051 与 gRPC 2052 入口。
package server

import (
	"net/http"

	v1 "server/api/brain/v1"
	"server/app/brain/internal/service"
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

func NewHTTPServer(c *conf.Brain, svc *service.BrainService, logger log.Logger) *khttp.Server {
	opts := []khttp.ServerOption{khttp.Middleware(recovery.Recovery(), logging.Server(logger))}
	if h := c.GetHttp(); h != nil {
		if h.GetNetwork() != "" {
			opts = append(opts, khttp.Network(h.GetNetwork()))
		}
		if h.GetAddr() != "" {
			opts = append(opts, khttp.Address(h.GetAddr()))
		}
		if h.GetTimeout() != nil {
			opts = append(opts, khttp.Timeout(h.GetTimeout().AsDuration()))
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
	v1.RegisterBrainHTTPServer(srv, svc)
	return srv
}

func NewGRPCServer(c *conf.Brain, svc *service.BrainService, logger log.Logger) *grpc.Server {
	opts := []grpc.ServerOption{grpc.Middleware(recovery.Recovery(), logging.Server(logger))}
	if g := c.GetGrpc(); g != nil {
		if g.GetNetwork() != "" {
			opts = append(opts, grpc.Network(g.GetNetwork()))
		}
		if g.GetAddr() != "" {
			opts = append(opts, grpc.Address(g.GetAddr()))
		}
		if g.GetTimeout() != nil {
			opts = append(opts, grpc.Timeout(g.GetTimeout().AsDuration()))
		}
	}
	srv := grpc.NewServer(opts...)
	v1.RegisterBrainServer(srv, svc)
	return srv
}
