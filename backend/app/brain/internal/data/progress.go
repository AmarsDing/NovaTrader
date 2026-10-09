package data

import (
	"context"

	"server/app/brain/internal/biz"
	"server/pkg/events"

	"github.com/go-kratos/kratos/v2/log"
)

// progress 把研判阶段发到 brain.progress（普通 NATS，不落盘）。发送失败只记日志，不影响研判。
type progress struct {
	bus *events.Bus
	log *log.Helper
}

func NewProgress(bus *events.Bus, logger log.Logger) biz.Progress {
	return &progress{bus: bus, log: log.NewHelper(log.With(logger, "module", "brain/progress"))}
}

func (p *progress) Publish(ctx context.Context, ev biz.ProgressEvent) {
	env, err := events.New("brain", events.SubjectBrainProgress, ev.TraceID, ev)
	if err != nil {
		p.log.Warnf("progress envelope: %v", err)
		return
	}
	if err := p.bus.Publish(context.WithoutCancel(ctx), env); err != nil {
		p.log.Warnf("progress publish: %v", err)
	}
}
