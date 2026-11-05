package harness

// HealthStatus 底座健康视图（机器面 GET /api/v1/health 忠实投影）。
type HealthStatus struct {
	Status        string `json:"status"`
	Version       string `json:"version"`
	UptimeSeconds int64  `json:"uptime_seconds"`
	AgentCount    int    `json:"agent_count"`
	GraphCount    int    `json:"graph_count"`
	ModelCount    int    `json:"model_count"`
}

// AgentSnapshot agent 目录项（GET /api/v1/agents items[] 投影；
// definition 原文保留，供上层按需解释）。
type AgentSnapshot struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Definition  map[string]any `json:"definition,omitempty"`
}

// SubmitReq run 提交请求（POST /api/v1/runs）。
type SubmitReq struct {
	// GraphID 必填——底座不支持 agent 直跑（无执行记录实体，无法支撑
	// 状态轮询/取消/审批闭环），须以 graph 包装。
	GraphID string
	Input   string
	Params  map[string]any
	// IdempotencyKey 必填纪律：调用方按业务幂等粒度生成（如
	// "nt-<biz>-<biz_id>"），底座按 (caller, key) 去重，重放返回原 run_id。
	IdempotencyKey string
	// WebhookURL/WebhookSecret 可选终态回调；空则轮询 GetRun 兜底。
	// secret 为空时底座生成随机串（NT 侧验签需自存，故建议显式传入）。
	WebhookURL    string
	WebhookSecret string
}

// SubmitResp run 提交应答。
type SubmitResp struct {
	RunID            string `json:"run_id"`
	IdempotentReplay bool   `json:"idempotent_replay,omitempty"`
}

// ApprovalDecision 审批决议（POST /runs/{id}/interrupts/{interrupt_id}/resume
// 请求体投影，键名与底座 twinResumeDecision 对齐）。
// 红线：决议仅来自人工审批面（M09），harness 无任何自动 approve 路径——
// LLM 只产出评分与理由，绝不直接推进执行。
type ApprovalDecision struct {
	Approved   bool   `json:"approved"`
	Comment    string `json:"comment"`
	ApproverID uint32 `json:"approver_id"`
}

// RunNode 单节点时间线项（run 详情 nodes[] 投影）。
type RunNode struct {
	NodeID        string `json:"node_id"`
	NodeName      string `json:"node_name"`
	NodeType      string `json:"node_type"`
	Status        string `json:"status"`
	ErrorMessage  string `json:"error_message,omitempty"`
	OutputSummary string `json:"output_summary,omitempty"`
}

// RunDetail run 详情（GET /api/v1/runs/{id} 投影）。
type RunDetail struct {
	RunID        string    `json:"run_id"`
	GraphID      string    `json:"graph_id"`
	Status       string    `json:"status"` // pending/running/succeeded/failed/cancelled/waiting_approval
	StartedAt    string    `json:"started_at"`
	FinishedAt   string    `json:"finished_at"`
	Output       string    `json:"output"`
	ErrorMessage string    `json:"error_message"`
	Nodes        []RunNode `json:"nodes"`
	DurationMs   int64     `json:"duration_ms"`
	ModelUsed    string    `json:"model_used"`
	TokensInput  int       `json:"tokens_input"`
	TokensOutput int       `json:"tokens_output"`
}

// RunSummary run 列表项（GET /api/v1/runs?graph_id= items[] 投影）。
type RunSummary struct {
	RunID      string `json:"run_id"`
	GraphID    string `json:"graph_id"`
	Status     string `json:"status"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
}

// RunListPage 列表分页（next_cursor 为空即到底）。
type RunListPage struct {
	Items      []RunSummary
	NextCursor string
}

// Terminal 报告 run 状态是否终态（底座状态词表；waiting_approval 属非终态，
// 需审批回路推进，轮询方不得把它当结束）。
func Terminal(status string) bool {
	switch status {
	case "succeeded", "failed", "cancelled":
		return true
	}
	return false
}
