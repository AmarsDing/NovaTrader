package data

import (
	"context"
	"time"

	"server/app/datahub/internal/biz"
	"server/pkg/events"
	"server/pkg/redact"

	"github.com/go-kratos/kratos/v2/log"
)

// Notice 是 notify.request 的载荷，M10 按 dedup_key 合并重复告警。
type Notice struct {
	Level    string `json:"level"`
	Category string `json:"category"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	DedupKey string `json:"dedup_key"`
}

// Alerter 把告警发到 notify.request，同时写日志。
type Alerter struct {
	pub biz.Publisher
	log *log.Helper
}

func NewAlerter(pub biz.Publisher, logger log.Logger) *Alerter {
	return &Alerter{pub: pub, log: log.NewHelper(log.With(logger, "module", "datahub/alert"))}
}

func (a *Alerter) Alert(ctx context.Context, level, title, body string) {
	body = redact.Line(body)
	a.log.Warnf("[%s] %s: %s", level, title, body)
	if a.pub == nil {
		return
	}
	n := Notice{Level: level, Category: "datahub", Title: title, Body: body, DedupKey: "datahub:" + title}
	env, err := events.New("datahub", events.SubjectNotifyRequest, events.TraceID(ctx), n)
	if err != nil {
		return
	}
	pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := a.pub.Publish(pctx, env); err != nil {
		a.log.Errorf("publish notify.request: %v", err)
	}
}
