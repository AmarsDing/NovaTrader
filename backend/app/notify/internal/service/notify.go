// Package service 把查询请求转成记录，不在这里发通知。
package service

import (
	"context"
	"errors"
	"time"

	v1 "server/api/notify/v1"
	"server/app/notify/internal/biz"

	kerrors "github.com/go-kratos/kratos/v2/errors"
	"github.com/google/wire"
)

var ProviderSet = wire.NewSet(NewNotifyService)

type NotifyService struct {
	v1.UnimplementedNotifyServer
	p *biz.Pipeline
}

func NewNotifyService(p *biz.Pipeline) *NotifyService { return &NotifyService{p: p} }

func (s *NotifyService) ListMessages(ctx context.Context, req *v1.ListMessagesRequest) (*v1.ListMessagesReply, error) {
	rows, err := s.p.List(ctx, int(req.GetLimit()))
	if err != nil {
		return nil, err
	}
	out := &v1.ListMessagesReply{}
	for _, row := range rows {
		out.Messages = append(out.Messages, toReply(row))
	}
	return out, nil
}

func (s *NotifyService) GetMessage(ctx context.Context, req *v1.GetMessageRequest) (*v1.MessageReply, error) {
	if req.GetId() <= 0 {
		return nil, kerrors.BadRequest("NOTIFY_INVALID", "id 无效")
	}
	row, err := s.p.Get(ctx, int(req.GetId()))
	if errors.Is(err, biz.ErrNotFound) {
		return nil, kerrors.NotFound("NOTIFY_NOT_FOUND", "记录不存在")
	}
	if err != nil {
		return nil, err
	}
	return toReply(row), nil
}

func toReply(m biz.Message) *v1.MessageReply {
	return &v1.MessageReply{
		Id: int64(m.ID), EventId: m.EventID, TraceId: m.TraceID, Source: m.Source, Subject: m.Subject,
		Category: m.Category, Priority: m.Priority, Title: m.Title, Body: m.Body, DedupKey: m.DedupKey,
		MergeCount: int32(m.MergeCount), Status: m.Status, Rendered: m.Rendered,
		DesktopStatus: m.DesktopStatus, DesktopAttempts: int32(m.DesktopAttempts), DesktopError: m.DesktopError,
		FeishuStatus: m.FeishuStatus, FeishuAttempts: int32(m.FeishuAttempts), FeishuError: m.FeishuError,
		CreatedAt: fmtTime(m.CreatedAt), SentAt: fmtTime(m.SentAt),
	}
}

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
