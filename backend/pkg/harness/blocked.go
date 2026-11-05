package harness

import "context"

// blockedClient 恒阻断实现（永驻兜底，ADR：driver=blocked 语义不可逆——
// 即使 http 适配器上线，本实现也不得删除；它是「契约未决≠可用」的
// 代码载体，防有人拿空数据冒充在线）。
type blockedClient struct {
	reason string
}

// blockedErr 组装恒阻断错误：哨兵 + 如实原因（reason 进日志/页面，
// 不含任何密钥材料——NewClient 只传解析失败的错误文案，token 值不进）。
func (c *blockedClient) blockedErr() error {
	if c.reason == "" {
		return ErrHarnessContractUnverified
	}
	return &Error{Kind: ErrHarnessContractUnverified, Detail: c.reason}
}

func (c *blockedClient) Health(context.Context) (*HealthStatus, error) {
	return nil, c.blockedErr()
}

func (c *blockedClient) ListAgents(context.Context) ([]AgentSnapshot, error) {
	return nil, c.blockedErr()
}

func (c *blockedClient) SubmitRun(context.Context, SubmitReq) (*SubmitResp, error) {
	return nil, c.blockedErr()
}

func (c *blockedClient) GetRun(context.Context, string) (*RunDetail, error) {
	return nil, c.blockedErr()
}

func (c *blockedClient) ListRuns(context.Context, string, string, string, int) (*RunListPage, error) {
	return nil, c.blockedErr()
}

func (c *blockedClient) CancelRun(context.Context, string) error {
	return c.blockedErr()
}

func (c *blockedClient) ResumeRun(context.Context, string, string, ApprovalDecision) error {
	return c.blockedErr()
}
