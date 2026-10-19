package biz

import (
	"context"

	"server/pkg/events"
)

// OpenRunTrace 委托 pkg/events 的合同工具 OpenTrace（语义单点维护）：ctx 已有链号
// 则尊重（调度/手工触发、级联/回放透传的上游链），没有才开 uuid 新链。一次采集
// 运行内发出的所有事件（md.*.ready、intel.raw.*、告警）共享这条链；下游消费方
// （intel 已缝合 ContextWithEnvelope）把评分/告警续到同一条链上，采集→评分→
// 告警沿同一 trace 可查（N0-02 全域因果的采集链头部）。运行本身是链根，
// 不编 causation_id。
func OpenRunTrace(ctx context.Context) context.Context {
	return events.OpenTrace(ctx)
}
