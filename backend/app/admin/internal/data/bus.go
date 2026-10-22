package data

import (
	"server/conf"
	"server/pkg/events"
	"server/pkg/scheduler"

	"github.com/go-kratos/kratos/v2/log"
)

// NewBus 连接 NATS，连不上返回 nil：登录与运维汇总不受影响，
// sys.session.changed 广播降级为不发（core.Runner 会记日志）。
func NewBus(c *conf.Nats, logger log.Logger) (*events.Bus, func()) {
	bus, err := events.Dial(c.GetUrl())
	if err != nil {
		log.NewHelper(log.With(logger, "module", "admin/data")).Warnf("nats 未连接，sys.session.changed 广播降级：%v", err)
		return nil, func() {}
	}
	return bus, bus.Close
}

// NewSchedulerStore 复用审计库的 *ent.Client 保存调度执行记录（task_run 共享表）。
// 库未配置时退化为内存记录：每日备份本来也会因库缺失失败，调度器照常记录与重试。
func NewSchedulerStore(a *AuditStore) scheduler.Store {
	if !a.Enabled() {
		return scheduler.NewMemory()
	}
	return scheduler.NewEntStore(a.client)
}
