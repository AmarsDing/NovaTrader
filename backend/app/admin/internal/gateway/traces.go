package gateway

import (
	"context"
	"github.com/go-kratos/kratos/v2/errors"
	"github.com/go-kratos/kratos/v2/transport"
	"net/http"
	v1 "server/api/admin/v1"
	"strings"
)

type TraceReader interface {
	ReadTrace(context.Context, string, int) (*v1.GetTraceReply, error)
}

func (s observabilityService) GetTrace(ctx context.Context, req *v1.GetTraceRequest) (*v1.GetTraceReply, error) {
	tr, ok := transport.FromServerContext(ctx)
	if !ok {
		return nil, errors.Unauthorized("AUTH_REQUIRED", "未登录")
	}
	request := &http.Request{Header: http.Header{"Authorization": []string{tr.RequestHeader().Get("Authorization")}}}
	if _, ok = s.gate.principal(request); !ok {
		return nil, errors.Unauthorized("AUTH_REQUIRED", "未登录")
	}
	trace := strings.TrimSpace(req.TraceId)
	if trace != "" && !observationID.MatchString(trace) || req.Limit < 0 {
		return nil, errors.BadRequest("TRACE_INVALID", "trace需为有限精确标识")
	}
	limit := int(req.Limit)
	if limit == 0 || limit > 200 {
		limit = 200
	}
	reader, ok := s.gate.Audit.(TraceReader)
	if !ok {
		return &v1.GetTraceReply{SchemaVersion: 1, Scope: "durable_outbox_history", Note: "持久事件仓储未接入"}, nil
	}
	out, err := reader.ReadTrace(ctx, trace, limit)
	if err != nil {
		return nil, errors.ServiceUnavailable("TRACE_UNAVAILABLE", "持久事件仓储不可用")
	}
	for _, e := range out.Events {
		e.EventId = safeObservationID(e.EventId)
		e.TraceId = safeObservationID(e.TraceId)
		e.CausationId = safeObservationID(e.CausationId)
		e.EntityId = safeObservationID(e.EntityId)
		if !observationSources[e.Source] {
			e.Source = "unknown"
		}
		e.Subject = safeObservationID(e.Subject)
	}
	for i, id := range out.MissingParentIds {
		out.MissingParentIds[i] = safeObservationID(id)
	}
	return out, nil
}
