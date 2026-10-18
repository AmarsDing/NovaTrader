package server

import (
	"net/http"

	v1 "server/api/trade/v1"
	"server/app/trade/internal/service"
	"server/conf"
	"server/pkg/metrics"
	"server/pkg/observe"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/middleware/logging"
	"github.com/go-kratos/kratos/v2/middleware/recovery"
	"github.com/go-kratos/kratos/v2/transport/grpc"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	"github.com/google/wire"
)

var ProviderSet = wire.NewSet(NewHTTPServer, NewGRPCServer)

func NewHTTPServer(c *conf.Trade, svc *service.TradeService, admission *service.AdmissionService, nav *service.NavService, logger log.Logger) *khttp.Server {
	opts := []khttp.ServerOption{
		khttp.Middleware(recovery.Recovery(), logging.Server(logger)),
		khttp.Address(":2091"),
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
	observe.RegisterHTTP(srv, observe.ForService("trade"))
	srv.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	srv.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_ = metrics.Write(w)
	})
	v1.RegisterTradeHTTPServer(srv, svc)
	v1.RegisterAutomationHTTPServer(srv, svc)
	v1.RegisterPortfolioAllocationHTTPServer(srv, svc)
	v1.RegisterAdmissionHTTPServer(srv, admission)
	v1.RegisterNavHTTPServer(srv, nav)
	mountDesk(srv, svc)
	return srv
}

func NewGRPCServer(c *conf.Trade, svc *service.TradeService, admission *service.AdmissionService, nav *service.NavService, logger log.Logger) *grpc.Server {
	opts := []grpc.ServerOption{
		grpc.Middleware(recovery.Recovery(), logging.Server(logger)),
		grpc.Address(":2092"),
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
	v1.RegisterTradeServer(srv, svc)
	v1.RegisterAutomationServer(srv, svc)
	v1.RegisterPortfolioAllocationServer(srv, svc)
	v1.RegisterAdmissionServer(srv, admission)
	v1.RegisterNavServer(srv, nav)
	return srv
}
