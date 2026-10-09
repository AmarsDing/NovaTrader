/*
 * @Description: ##  描述文件功能  ##
 * @Author: AmarsDing
 * @Date: 2022-05-26 15:39:57
 * @Copyright: 北京迈特力德信息技术有限公司, METLED@2021
 */
package websocket

import (
	"context"
	"net/http"
	"time"

	"github.com/go-kratos/kratos/v2/log"

	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/gorilla/websocket"
)

var (
	clientManager = NewClientManager() // 管理者
)
var upGrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func StartWebSocket(ctx context.Context, logger log.Logger) {
	clientManager.log = log.NewHelper(log.With(logger, "module", "websocket"))
	clientManager.log.Info(ctx, "启动:WebSocket")
	go clientManager.start()
	go clientManager.ping(ctx)
}

func GetMessage() []*WResponse {
	return clientManager.Message
}

func WsPage(r *ghttp.Request) {
	conn, err := upGrader.Upgrade(r.Response.ResponseWriter, r.Request, nil)
	if err != nil {
		return
	}
	currentTime := uint64(time.Now().Unix())
	client := NewClient(conn.RemoteAddr().String(), conn, currentTime, clientManager.log)
	go client.read()
	go client.write()
	// 用户连接事件
	clientManager.Register <- client
}

// UpgradeCheck 在升级前校验查询串里的令牌。未设置时放行。
var UpgradeCheck func(r *http.Request) bool

// RangeClients 遍历当前连接。
func RangeClients(f func(*Client)) {
	clientManager.ClientsRange(func(client *Client, _ bool) bool {
		f(client)
		return true
	})
}

func WsPageByHttp(w http.ResponseWriter, r *http.Request) {
	if UpgradeCheck != nil && !UpgradeCheck(r) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"未登录"}`))
		return
	}
	conn, err := upGrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	currentTime := uint64(time.Now().Unix())
	client := NewClient(conn.RemoteAddr().String(), conn, currentTime, clientManager.log)
	go client.read()
	go client.write()
	// 用户连接事件
	clientManager.Register <- client
}
