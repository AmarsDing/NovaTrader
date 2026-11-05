package harness

import (
	"context"
	"errors"
)

// harness 错误域（诚实边界——调用方按哨兵分流 UI 三态，禁止塌成空数据）。
var (
	// ErrHarnessContractUnverified 恒阻断哨兵：driver=blocked / token 未配置 /
	// 契约未实机验证。永驻——即使 http 适配器上线，blocked 实现与本哨兵
	// 不得删除（HCF ADR-AGT-01 同构纪律；对应测试标记「永不删除」）。
	ErrHarnessContractUnverified = errors.New("HARNESS_CONTRACT_UNVERIFIED")
	// ErrHarnessUnavailable 契约已切 http 但底座不可达/鉴权失败/5xx——
	// 子错误如实带上游原因（不猜数据）。
	ErrHarnessUnavailable = errors.New("HARNESS_UPSTREAM_UNAVAILABLE")
	// ErrHarnessBadRequest 底座 400（参数/白名单/幂等冲突等）——如实透传
	// 底座错误文案。
	ErrHarnessBadRequest = errors.New("HARNESS_BAD_REQUEST")
	// ErrHarnessNotFound 底座 404（run/graph 不存在或对 caller 不可见）。
	ErrHarnessNotFound = errors.New("HARNESS_NOT_FOUND")
	// ErrWebhookUnauthorized webhook 验签失败四态合一（缺 secret/畸形/失配/越窗）
	// ——401 同面防枚举，绝不区分原因给投递方（HCF W10 同构）。
	ErrWebhookUnauthorized = errors.New("HARNESS_WEBHOOK_UNAUTHORIZED")
	// ErrWebhookBadRequest webhook 信封契约漂移（畸形 JSON/词表外事件类型/缺必填）。
	ErrWebhookBadRequest = errors.New("HARNESS_WEBHOOK_BAD_REQUEST")
)

// Client 底座机器面客户端端口。实现二态：
//   - blockedClient（blocked.go）：恒阻断，永驻兜底；
//   - httpClient（http.go）：Bearer 适配 /api/v1/*。
//
// 组装入口 NewClient(conf)：driver=blocked 或 token 解析失败时返回
// blocked 实现（配置缺失≠可用，如实恒阻断）。
type Client interface {
	// Health 存活探测（GET /api/v1/health）。
	Health(ctx context.Context) (*HealthStatus, error)
	// ListAgents 可用 agent 目录（GET /api/v1/agents；caller 租户面=共享+自有）。
	ListAgents(ctx context.Context) ([]AgentSnapshot, error)
	// SubmitRun 提交图执行（POST /api/v1/runs；GraphID 与 IdempotencyKey 必填，
	// 参数缺失属调用方 bug，返回 ErrHarnessBadRequest 且不发请求）。
	SubmitRun(ctx context.Context, req SubmitReq) (*SubmitResp, error)
	// GetRun 单 run 详情（GET /api/v1/runs/{id}）。
	GetRun(ctx context.Context, runID string) (*RunDetail, error)
	// ListRuns 按图枚举 run（GET /api/v1/runs?graph_id=；graphID 必填，
	// 底座按图归属做租户门禁）。
	ListRuns(ctx context.Context, graphID, status, cursor string, pageSize int) (*RunListPage, error)
	// CancelRun 取消执行（POST /api/v1/runs/{id}/cancel）。
	CancelRun(ctx context.Context, runID string) error
	// ResumeRun 审批决议回投（POST /api/v1/runs/{id}/interrupts/{interrupt_id}/resume；
	// interrupt_id 来自 run.waiting_approval 事件 payload）。
	// 红线：harness 只提供能力，决议仅来自人工审批面——本包无任何自动
	// approve 路径（NT 纪律：LLM 只产出评分与理由，绝不直接推进执行）。
	ResumeRun(ctx context.Context, runID, interruptID string, decision ApprovalDecision) error
}

// NewClient 按配置组装客户端。token 解析失败（env 未设置/enc 解密失败）
// 不视为致命错误——返回 blocked 实现，让健康面如实呈现「契约未决」而非
// 进程起不来（对齐 HCF AGT 同构语义）。
func NewClient(conf Conf, resolveToken func(string) (string, error)) Client {
	conf.Normalize()
	if err := conf.Validate(); err != nil {
		return &blockedClient{reason: err.Error()}
	}
	if conf.Driver == DriverBlocked {
		return &blockedClient{reason: "driver=blocked"}
	}
	if resolveToken == nil {
		return &blockedClient{reason: "token resolver 未注入"}
	}
	token, err := resolveToken(conf.TokenRef)
	if err != nil || token == "" {
		reason := "token 未配置"
		if err != nil {
			reason = "token 解析失败: " + err.Error()
		}
		return &blockedClient{reason: reason}
	}
	return newHTTPClient(conf.URL, token, conf.Timeout)
}
