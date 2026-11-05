package ntmcp

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	trpcmcp "trpc.group/trpc-go/trpc-mcp-go"
)

// Server nt-mcp 机器面：trpc-mcp-go MCP server + 独立 HTTP 监听 + Bearer 闸。
// token 启动时经 resolveToken（llm.ResolveAPIKey）解析一次、解析失败即拒启动
// ——NT 无设置页运行时改密面，启动 fail-fast 比「401 空转」更诚实
// （轮换 = 改 env/enc 引用 + 重启，与 harness 同纪律）。
type Server struct {
	srv   *trpcmcp.Server
	http  *http.Server
	conf  *Conf
	token string
	log   *log.Helper
}

// NewServer 组装 MCP server（注册 gated 工具）与外层 HTTP（含 Bearer 闸）。
// resolveToken 为空值（含空串解析结果）即报错——「未配置」绝不等于「放行」。
func NewServer(conf *Conf, resolveToken func(string) (string, error), deps ToolDeps, gates *ToolGates, logger log.Logger) (*Server, error) {
	token, err := resolveToken(conf.TokenRef)
	if err != nil {
		return nil, err
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, errors.New("ntmcp.token_ref 解析为空（未配置），拒启动")
	}
	s := &Server{conf: conf, token: token,
		log: log.NewHelper(log.With(logger, "module", "ntmcp/server"))}

	mcpSrv := trpcmcp.NewServer("nt-mcp", "1.0.0", trpcmcp.WithServerPath(conf.Path))
	if gates == nil {
		g := conf.Tools
		gates = &g
	}
	RegisterTools(mcpSrv, deps, gates)

	// 显式 mux：路径外请求 404（MCP handler 对非法路径静默返回，路由边界自己把牢）。
	mux := http.NewServeMux()
	mux.Handle(conf.Path+"/", s.guard(mcpSrv.Handler()))
	mux.Handle(conf.Path, s.guard(mcpSrv.Handler()))

	s.srv = mcpSrv
	s.http = &http.Server{
		Addr:              conf.Listen,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       conf.HTTPTimeout,
		WriteTimeout:      conf.HTTPTimeout,
		IdleTimeout:       60 * time.Second,
	}
	return s, nil
}

// guard Bearer 认证闸（常量时间比对；token 缺失/失配 → 401，路由本身不区分存在性）。
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
			s.log.Warnf("mcp auth refused from %s", r.RemoteAddr)
			http.Error(w, `{"jsonrpc":"2.0","error":{"code":-32001,"message":"unauthorized"}}`, http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Start 阻塞运行（signal 处理由调用方负责——main 用 NotifyContext 触发 Stop）。
func (s *Server) Start() error {
	s.log.Infof("nt-mcp listening %s%s (tools gated by config)", s.conf.Listen, s.conf.Path)
	if err := s.http.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Stop 优雅关停。
func (s *Server) Stop(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}
