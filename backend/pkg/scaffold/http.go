// Package scaffold 是新服务共用的 HTTP 入口：健康检查和指标。
package scaffold

import (
	"net/http"

	"server/pkg/metrics"

	khttp "github.com/go-kratos/kratos/v2/transport/http"
)

var up = metrics.Counter("novatrader_http_health", "health checks served")

// HTTP 监听 addr，并挂上 /healthz 与 /metrics。
func HTTP(addr string) *khttp.Server {
	srv := khttp.NewServer(khttp.Address(addr))
	srv.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		up.Inc()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	srv.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_ = metrics.Write(w)
	})
	return srv
}
