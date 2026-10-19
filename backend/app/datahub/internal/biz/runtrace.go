package biz

import (
	"context"

	"github.com/google/uuid"

	"server/pkg/events"
)

// OpenRunTrace 在一次采集运行的入口开运行链：ctx 已有链号则尊重（调用方串链优先，
// 例如回放/级联触发透传的上游链），没有才开新链。一次运行内发出的所有事件
// （md.*.ready、intel.raw.*、告警）共享这条链；下游消费方（intel 已缝合
// ContextWithEnvelope）会把评分/告警续到同一条链上，采集→评分→告警沿同一
// trace 可查（N0-02 全域因果的采集链头部）。运行本身是链根——没有父事件，
// 不编 causation_id。
func OpenRunTrace(ctx context.Context) context.Context {
	if events.TraceID(ctx) != "" {
		return ctx
	}
	return events.WithTraceID(ctx, uuid.NewString())
}
