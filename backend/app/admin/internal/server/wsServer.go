package server

import (
	"context"
	"net/http"
	"time"

	"server/app/admin/internal/gateway"
	"server/conf"
	"server/utils/websocket"

	"github.com/go-kratos/kratos/v2/log"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	"github.com/gorilla/mux"
)

// WebsocketServer 是桌面长连接，并转发 notify.desktop。
type WebsocketServer struct {
	*khttp.Server
	stopBridge func()
}

func NewWebsocketServer(c *conf.Admin, nats *conf.Nats, settings gateway.Settings, hub *gateway.Hub, logger log.Logger) *WebsocketServer {
	websocket.StartWebSocket(context.TODO(), logger)
	websocket.UpgradeCheck = func(r *http.Request) bool {
		return gateway.AllowSocket(settings.Secret, r.URL.Query().Get("token"), time.Now())
	}
	router := mux.NewRouter()
	router.HandleFunc("/ws", websocket.WsPageByHttp)

	httpSrv := khttp.NewServer(khttp.Address(c.Websocket.Addr))
	httpSrv.HandlePrefix("/", router)
	stop := func() {}
	if hub != nil {
		url := ""
		if nats != nil {
			url = nats.GetUrl()
		}
		stop = hub.Start(url)
	}
	return &WebsocketServer{Server: httpSrv, stopBridge: stop}
}

// Stop 先停桌面转发，再关 WebSocket 端口。
func (s *WebsocketServer) Stop(ctx context.Context) error {
	if s.stopBridge != nil {
		s.stopBridge()
	}
	return s.Server.Stop(ctx)
}
