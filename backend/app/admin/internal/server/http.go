package server

import (
	"net/http"

	"server/app/admin/internal/data"
	"server/app/admin/internal/gateway"
	"server/conf"
	"server/pkg/metrics"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/middleware/logging"
	"github.com/go-kratos/kratos/v2/middleware/recovery"
	"github.com/go-kratos/kratos/v2/middleware/tracing"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
)

func NewHTTPServer(c *conf.Admin, settings gateway.Settings, audit *data.AuditStore, hub *gateway.Hub, logger log.Logger) *khttp.Server {
	opts := []khttp.ServerOption{
		khttp.Middleware(
			recovery.Recovery(),
			tracing.Server(),
			logging.Server(logger),
		),
	}
	if c.Http.Network != "" {
		opts = append(opts, khttp.Network(c.Http.Network))
	}
	if c.Http.Addr != "" {
		opts = append(opts, khttp.Address(c.Http.Addr))
	}
	if c.Http.Timeout != nil {
		opts = append(opts, khttp.Timeout(c.Http.Timeout.AsDuration()))
	}
	opts = append(opts, khttp.Filter(withCORS))
	srv := khttp.NewServer(opts...)
	srv.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	srv.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_ = metrics.Write(w)
	})
	helper := log.NewHelper(log.With(logger, "module", "admin/http"))
	if len(settings.Users) == 0 {
		helper.Warn("没有配置 admin.users，桌面端无法登录")
	}
	gate := &gateway.Gate{
		Settings: settings,
		Audit:    audit,
		Log:      helper,
		PingDB:   audit.Ping,
	}
	if hub != nil {
		gate.NATSUp = hub.Up
	}
	srv.HandlePrefix("/api/", gate)
	return srv
}
