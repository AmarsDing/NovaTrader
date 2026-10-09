package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-kratos/kratos/v2/log"
)

// Entry 是一条审计。
type Entry struct {
	Actor  string
	Role   string
	Action string
	Target string
	Detail string
	OK     bool
}

// Auditor 把审计写入数据库。没连上库时 Enabled 为 false。
type Auditor interface {
	Enabled() bool
	Write(ctx context.Context, e Entry) error
}

// Gate 是桌面端唯一的 HTTP 入口：登录、页面转发、运维汇总。
type Gate struct {
	Settings Settings
	Audit    Auditor
	Log      *log.Helper
	NATSUp   func() bool
	PingDB   func(context.Context) error
	Now      func() time.Time

	once    sync.Once
	proxies map[string]*httputil.ReverseProxy
}

type route struct {
	name    string
	prefix  string
	trim    string
	join    string
	timeout time.Duration
}

func (g *Gate) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

func (g *Gate) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	writeCORS(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/login":
		g.login(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/ops/status":
		g.ops(w, r)
	default:
		g.forward(w, r)
	}
}

func (g *Gate) login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserName string `json:"user_name"`
		Password string `json:"password"`
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "请求读不了")
		return
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求不是 JSON")
		return
	}
	req.UserName = strings.TrimSpace(req.UserName)
	if req.UserName == "" || req.Password == "" {
		writeErr(w, http.StatusBadRequest, "请输入账号和密码")
		return
	}
	if g.Settings.Secret == "" {
		writeErr(w, http.StatusServiceUnavailable, "令牌密钥未配置")
		return
	}
	user, ok := CheckPassword(g.Settings.Users, req.UserName, req.Password)
	if !ok {
		g.audit(r.Context(), Entry{Actor: clip(req.UserName, 64), Action: "login", OK: false, Detail: "rejected"})
		writeErr(w, http.StatusUnauthorized, "账号或密码错误")
		return
	}
	token, err := Issue(g.Settings.Secret, user, g.now())
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "令牌签发失败")
		return
	}
	if err := g.audit(r.Context(), Entry{
		Actor: user.Name, Role: user.Role, Action: "login", OK: true, Detail: "ok",
	}); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "审计写入失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token": token,
		"user": map[string]string{
			"id":           user.Name,
			"name":         user.Name,
			"display_name": displayOf(user),
			"role":         user.Role,
		},
	})
}

func (g *Gate) forward(w http.ResponseWriter, r *http.Request) {
	principal, ok := g.principal(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "未登录")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead && !isOwner(principal.Role) {
		writeErr(w, http.StatusForbidden, "需要 Owner")
		return
	}
	item, ok := matchRoute(r.URL.Path)
	if !ok {
		writeErr(w, http.StatusNotFound, "没有这个接口")
		return
	}
	if action := auditAction(r.Method, r.URL.Path); action != "" {
		if err := g.audit(r.Context(), Entry{
			Actor: principal.Name, Role: principal.Role, Action: action,
			Target: clip(r.URL.Path, 256), OK: true, Detail: r.Method,
		}); err != nil {
			writeErr(w, http.StatusServiceUnavailable, "审计写入失败")
			return
		}
	}
	proxy := g.proxy(item.name)
	if proxy == nil {
		writeErr(w, http.StatusBadGateway, "上游未配置")
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, item.trim)
	r.URL.Path = item.join + rest
	r.Header.Set("X-User", principal.Name)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), item.timeout)
	defer cancel()
	proxy.ServeHTTP(w, r.WithContext(ctx))
}

func (g *Gate) principal(r *http.Request) (Principal, bool) {
	header := r.Header.Get("Authorization")
	token := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
	if token == "" || token == header {
		return Principal{}, false
	}
	p, err := Parse(g.Settings.Secret, token, g.now())
	if err != nil {
		return Principal{}, false
	}
	return p, true
}

func (g *Gate) audit(ctx context.Context, e Entry) error {
	if g.Audit == nil || !g.Audit.Enabled() {
		if g.Log != nil && (e.Action == "login" || e.OK) {
			g.Log.Warnf("审计未启用，跳过 %s", e.Action)
		}
		if sensitive(e.Action) && e.Action != "login" {
			return errAuditOff
		}
		return nil
	}
	return g.Audit.Write(ctx, e)
}

var errAuditOff = errString("audit disabled")

type errString string

func (e errString) Error() string { return string(e) }

func sensitive(action string) bool {
	switch action {
	case "login", "risk.mode", "risk.killswitch", "risk.killswitch.reset", "risk.params",
		"trade.order", "trade.cancel", "trade.confirm", "strategy.approve", "strategy.reject", "strategy.rollback":
		return true
	default:
		return false
	}
}

func auditAction(method, path string) string {
	switch {
	case method == http.MethodPost && path == "/api/risk/mode":
		return "risk.mode"
	case method == http.MethodPost && path == "/api/risk/killswitch/reset":
		return "risk.killswitch.reset"
	case method == http.MethodPost && path == "/api/risk/killswitch":
		return "risk.killswitch"
	case method == http.MethodPost && path == "/api/risk/params":
		return "risk.params"
	case method == http.MethodPost && path == "/api/trade/orders":
		return "trade.order"
	case method == http.MethodPost && strings.HasSuffix(path, "/cancel"):
		return "trade.cancel"
	case method == http.MethodPost && strings.HasSuffix(path, "/confirm"):
		return "trade.confirm"
	case method == http.MethodPost && strings.Contains(path, "/approve"):
		return "strategy.approve"
	case method == http.MethodPost && strings.Contains(path, "/reject"):
		return "strategy.reject"
	case method == http.MethodPost && strings.Contains(path, "/rollback"):
		return "strategy.rollback"
	default:
		return ""
	}
}

func matchRoute(path string) (route, bool) {
	for _, item := range routes {
		if strings.HasPrefix(path, item.prefix) {
			return item, true
		}
	}
	return route{}, false
}

var routes = []route{
	{name: "market", prefix: "/api/market/", trim: "/api/market", join: "/v1/market", timeout: 20 * time.Second},
	{name: "intel", prefix: "/api/intel/", trim: "/api/intel", join: "/v1/intel", timeout: 20 * time.Second},
	{name: "intel", prefix: "/api/kb/", trim: "/api/kb", join: "/v1/kb", timeout: 20 * time.Second},
	{name: "risk", prefix: "/api/risk/", trim: "/api/risk", join: "/risk/v1", timeout: 20 * time.Second},
	{name: "trade", prefix: "/api/trade/", trim: "/api/trade", join: "/v1/trade", timeout: 20 * time.Second},
	{name: "strategy", prefix: "/api/strategy/", trim: "/api/strategy", join: "/v1/strategy", timeout: 20 * time.Second},
	{name: "backtest", prefix: "/api/backtest/", trim: "/api/backtest", join: "/v1/backtest", timeout: 3 * time.Minute},
	{name: "brain", prefix: "/api/brain/", trim: "/api/brain", join: "/v1/brain", timeout: 20 * time.Second},
	{name: "notify", prefix: "/api/notify/", trim: "/api/notify", join: "/notify/v1", timeout: 20 * time.Second},
	{name: "datahub", prefix: "/api/datahub/", trim: "/api/datahub", join: "/v1/datahub", timeout: 20 * time.Second},
}

func (g *Gate) proxy(name string) *httputil.ReverseProxy {
	g.once.Do(func() {
		g.proxies = map[string]*httputil.ReverseProxy{}
		for name, raw := range g.Settings.Upstreams {
			target, err := url.Parse(raw)
			if err != nil || target.Scheme == "" || target.Host == "" {
				continue
			}
			proxy := httputil.NewSingleHostReverseProxy(target)
			proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
				writeCORS(w)
				writeErr(w, http.StatusBadGateway, "服务不可用")
			}
			g.proxies[name] = proxy
		}
	})
	return g.proxies[name]
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func writeErr(w http.ResponseWriter, code int, message string) {
	writeJSON(w, code, map[string]string{"message": message})
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func writeCORS(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	h.Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-User")
	h.Set("Access-Control-Max-Age", "86400")
}
