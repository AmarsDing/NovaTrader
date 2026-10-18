package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
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

// Row 是审计查询返回的一行（带主键与入库时间）。
type Row struct {
	ID        int       `json:"id"`
	Actor     string    `json:"actor"`
	Role      string    `json:"role"`
	Action    string    `json:"action"`
	Target    string    `json:"target"`
	Detail    string    `json:"detail"`
	OK        bool      `json:"ok"`
	CreatedAt time.Time `json:"created_at"`
}

// Auditor 把审计写入数据库。没连上库时 Enabled 为 false。
type Auditor interface {
	Enabled() bool
	Write(ctx context.Context, e Entry) error
}

// AuditReader 是审计查询能力；实现可选，没接库时不实现。
type AuditReader interface {
	List(ctx context.Context, action string, limit int) ([]Row, error)
}

// BackupRunner 跑一次数据库备份；实现放在 data 层（pg_dump）。
type BackupRunner interface {
	Backup(ctx context.Context) (BackupResult, error)
}

// BackupResult 是一次备份的结果，字段直接回给前端。
type BackupResult struct {
	Path       string    `json:"path"`
	Size       int64     `json:"size"`
	Database   string    `json:"database"`
	DurationMs int64     `json:"durationMs"`
	At         time.Time `json:"at"`
}

// BackupConfirm 是备份的二次确认口令（FR-11-08）：前端必须原样提交，服务端强制校验。
const BackupConfirm = "BACKUP"

// Gate 是桌面端唯一的 HTTP 入口：登录、页面转发、运维汇总。
type Gate struct {
	Settings Settings
	Audit    Auditor
	Params   ParamStore
	Backup   BackupRunner
	Log      *log.Helper
	NATSUp   func() bool
	PingDB   func(context.Context) error
	// P2 运维只读口：连接池/库内省/NATS 统计。由 data.AuditStore 与 Hub 注入。
	PoolStats    PoolStatsFunc
	DBStats      DBStatsFunc
	NATSStats    NATSStatsFunc
	Observations *Hub
	Now          func() time.Time
	// ServerContext 返回一个不被单个请求超时约束的 ctx（备份跑 pg_dump 要几分钟，
	// 不能挂在 kratos 默认 1s 的请求超时上）。nil 时退化为 Background。
	ServerContext func() context.Context

	once               sync.Once
	proxies            map[string]*httputil.ReverseProxy
	observationOnce    sync.Once
	observationHandler http.Handler
	environmentOnce    sync.Once
	environmentHandler http.Handler
	environment        environmentCache
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
	case r.Method == http.MethodGet && r.URL.Path == "/api/ops/gpu":
		g.opsGPU(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/ops/broker-files":
		g.opsBrokerFiles(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/ops/db":
		g.opsDB(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/ops/nats":
		g.opsNATS(w, r)
	case r.URL.Path == "/api/ops/observability" || r.URL.Path == "/api/ops/traces":
		g.serveObservability(w, r)
	case r.URL.Path == "/api/ops/dispositions":
		g.dispositions(w, r)
	case r.URL.Path == "/api/ops/environment":
		g.serveEnvironment(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/ops/backup":
		g.backup(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/audit":
		g.listAudit(w, r)
	case r.URL.Path == "/api/config" || strings.HasPrefix(r.URL.Path, "/api/config/"):
		g.config(w, r)
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

// listAudit 读取审计日志：GET /api/v1/audit?action=&limit=，需登录。
func (g *Gate) listAudit(w http.ResponseWriter, r *http.Request) {
	if _, ok := g.principal(r); !ok {
		writeErr(w, http.StatusUnauthorized, "未登录")
		return
	}
	reader, ok := g.Audit.(AuditReader)
	if !ok || reader == nil {
		writeErr(w, http.StatusServiceUnavailable, "审计未启用")
		return
	}
	action := strings.TrimSpace(r.URL.Query().Get("action"))
	limit, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
	if err != nil {
		limit = 100
	}
	rows, err := reader.List(r.Context(), action, limit)
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "审计读取失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rows})
}

// backup 触发一次数据库备份：POST /api/ops/backup，仅 Owner，且必须带二次确认口令。
// 备份是写盘动作，审计写不进去就不放行（与 config.save 同一口径）。
func (g *Gate) backup(w http.ResponseWriter, r *http.Request) {
	principal, ok := g.principal(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "未登录")
		return
	}
	if !isOwner(principal.Role) {
		writeErr(w, http.StatusForbidden, "需要 Owner")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "请求读不了")
		return
	}
	var req struct {
		Confirm string `json:"confirm"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求不是 JSON")
		return
	}
	if strings.TrimSpace(req.Confirm) != BackupConfirm {
		writeErr(w, http.StatusBadRequest, "备份需要输入 "+BackupConfirm+" 二次确认")
		return
	}
	if g.Backup == nil {
		writeErr(w, http.StatusServiceUnavailable, "备份未启用（数据库未配置）")
		return
	}
	// 请求 ctx 不直接用：kratos 默认给每个请求 1s 超时（见 server/http.go），
	// 而 pg_dump 跑几分钟很正常。挂到 ServerContext 上，进程在跑就能跑完，
	// 客户端断了备份也不作废（AuditStore.Backup 自带 10 分钟上限）。
	ctx := context.Background()
	if g.ServerContext != nil {
		ctx = g.ServerContext()
	}
	result, err := g.Backup.Backup(ctx)
	if err != nil {
		_ = g.audit(r.Context(), Entry{
			Actor: principal.Name, Role: principal.Role, Action: "ops.backup",
			Target: clip(BackupConfirm, 32), OK: false, Detail: clip(err.Error(), 512),
		})
		writeErr(w, http.StatusServiceUnavailable, "备份失败："+clip(err.Error(), 200))
		return
	}
	if err := g.audit(r.Context(), Entry{
		Actor: principal.Name, Role: principal.Role, Action: "ops.backup",
		Target: clip(result.Path, 256), OK: true,
		Detail: result.Database + " " + strconv.FormatInt(result.Size, 10) + " bytes",
	}); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "审计写入失败")
		return
	}
	writeJSON(w, http.StatusOK, result)
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
	if action == "trade.plan" || action == "backtest.dataset" || action == "research.job" {
		return true
	}
	switch action {
	case "login", "risk.mode", "risk.killswitch", "risk.killswitch.reset", "risk.params", "config.save",
		"trade.order", "trade.cancel", "trade.confirm", "strategy.approve", "strategy.reject", "strategy.rollback",
		"ops.backup", "ops.disposition":
		return true
	default:
		return false
	}
}

func auditAction(method, path string) string {
	switch {
	case method == http.MethodPost && strings.HasPrefix(path, "/api/backtest/research/"):
		return "research.job"
	case method == http.MethodPost && strings.HasPrefix(path, "/api/trade/plans"):
		return "trade.plan"
	case method == http.MethodPost && path == "/api/backtest/datasets":
		return "backtest.dataset"
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
	h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
	h.Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-User")
	h.Set("Access-Control-Max-Age", "86400")
}
