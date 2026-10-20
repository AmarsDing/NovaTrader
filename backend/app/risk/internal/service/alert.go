package service

import (
	"context"
	"errors"

	v1 "server/api/risk/v1"
	"server/app/risk/internal/biz"
	"server/pkg/alert"

	kerrors "github.com/go-kratos/kratos/v2/errors"
)

// G3 盯盘告警规则 CRUD：只做字段规整与错误映射，语义在 biz.AlertCenter（pkg/alert 引擎）。

func alertErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, biz.ErrAlertNotFound) {
		return kerrors.NotFound("ALERT_RULE_NOT_FOUND", "告警规则不存在")
	}
	return mapErr(err)
}

func toAlertRule(r alert.Rule) *v1.AlertRule {
	out := &v1.AlertRule{
		Id: r.ID, Symbol: r.Symbol, Condition: string(r.Condition), TargetPrice: r.TargetPrice,
		Note: r.Note, Status: string(r.Status),
		ArmedAt: formatTime(r.ArmedAt), CreatedAt: formatTime(r.CreatedAt), UpdatedAt: formatTime(r.UpdatedAt),
	}
	if r.LastPrice != nil {
		v := *r.LastPrice
		out.LastPrice = &v
	}
	if r.TriggeredAt != nil {
		ts := formatTime(*r.TriggeredAt)
		out.TriggeredAt = &ts
	}
	return out
}

func (s *RiskService) alertReply(ctx context.Context) (*v1.AlertRulesReply, error) {
	rules, err := s.alerts.List(ctx)
	if err != nil {
		return nil, alertErr(err)
	}
	out := &v1.AlertRulesReply{}
	for _, r := range rules {
		out.Rules = append(out.Rules, toAlertRule(r))
	}
	return out, nil
}

func (s *RiskService) ListAlertRules(ctx context.Context, _ *v1.ListAlertRulesRequest) (*v1.AlertRulesReply, error) {
	return s.alertReply(ctx)
}

func (s *RiskService) CreateAlertRule(ctx context.Context, req *v1.CreateAlertRuleRequest) (*v1.AlertRulesReply, error) {
	if _, err := s.alerts.Create(ctx, req.GetSymbol(), req.GetCondition(), req.GetTargetPrice(), req.GetNote(), req.GetOperator()); err != nil {
		return nil, alertErr(err)
	}
	return s.alertReply(ctx)
}

func (s *RiskService) EditAlertRule(ctx context.Context, req *v1.EditAlertRuleRequest) (*v1.AlertRulesReply, error) {
	if _, err := s.alerts.Edit(ctx, req.GetId(), req.GetSymbol(), req.GetCondition(), req.GetTargetPrice(), req.GetNote(), req.GetOperator()); err != nil {
		return nil, alertErr(err)
	}
	return s.alertReply(ctx)
}

func (s *RiskService) RearmAlertRule(ctx context.Context, req *v1.RearmAlertRuleRequest) (*v1.AlertRulesReply, error) {
	if _, err := s.alerts.Rearm(ctx, req.GetId(), req.GetOperator()); err != nil {
		return nil, alertErr(err)
	}
	return s.alertReply(ctx)
}

func (s *RiskService) DeleteAlertRule(ctx context.Context, req *v1.DeleteAlertRuleRequest) (*v1.AlertRulesReply, error) {
	if err := s.alerts.Delete(ctx, req.GetId(), req.GetOperator()); err != nil {
		return nil, alertErr(err)
	}
	return s.alertReply(ctx)
}
