package gateway

import (
	"context"
	"net/http"
	"time"

	v1 "server/api/admin/v1"

	"github.com/go-kratos/kratos/v2/errors"
	"github.com/go-kratos/kratos/v2/transport"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
)

// systemClockService 实现 v1.SystemClockHTTPServer（P2-10）。
// 直读 admin 进程当前时刻：无缓存、无转发——前端要的就是这台机器处理请求那一刻的钟。
type systemClockService struct{ gate *Gate }

func (s systemClockService) GetServerTime(ctx context.Context, _ *v1.GetServerTimeRequest) (*v1.GetServerTimeReply, error) {
	tr, ok := transport.FromServerContext(ctx)
	if !ok {
		return nil, errors.Unauthorized("AUTH_REQUIRED", "未登录")
	}
	r := &http.Request{Header: http.Header{"Authorization": []string{tr.RequestHeader().Get("Authorization")}}}
	if _, ok := s.gate.principal(r); !ok {
		return nil, errors.Unauthorized("AUTH_REQUIRED", "未登录")
	}
	now := s.gate.now()
	return &v1.GetServerTimeReply{
		ServerTime: now.Format(time.RFC3339Nano),
		EpochMs:    now.UnixMilli(),
	}, nil
}

func (g *Gate) serveSystemClock(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeErr(w, http.StatusMethodNotAllowed, "服务端时钟接口只读")
		return
	}
	/* P2-10 评审：合同写明「无缓存、无转发」——时钟响应任何一层缓存都会直接污染前端补偿。
	 * 无 ETag/Last-Modified 时 RFC 7234 启发式缓存本不生效，此处显式钉死。 */
	w.Header().Set("Cache-Control", "no-store")
	g.systemClockOnce.Do(func() {
		server := khttp.NewServer()
		v1.RegisterSystemClockHTTPServer(server, systemClockService{gate: g})
		g.systemClockHandler = server
	})
	g.systemClockHandler.ServeHTTP(w, r)
}
