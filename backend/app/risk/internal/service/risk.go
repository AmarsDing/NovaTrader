// Package service 把 api/risk/v1 的请求转给 biz，只做类型转换。
package service

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	v1 "server/api/risk/v1"
	"server/app/risk/internal/biz"
	"server/pkg/params"
	"server/pkg/risk"

	kerrors "github.com/go-kratos/kratos/v2/errors"
	"github.com/google/wire"
)

var ProviderSet = wire.NewSet(NewRiskService)

// checkTimeout 是 CheckOrder 的服务端上限，调用方的更短超时优先。
const checkTimeout = 200 * time.Millisecond

type RiskService struct {
	v1.UnimplementedRiskServer
	e      *biz.Engine
	alerts *biz.AlertCenter
}

func NewRiskService(e *biz.Engine, alerts *biz.AlertCenter) *RiskService {
	return &RiskService{e: e, alerts: alerts}
}

func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, biz.ErrInvalid) {
		return kerrors.BadRequest("RISK_INVALID", err.Error())
	}
	if errors.Is(err, params.ErrConflict) {
		return kerrors.New(409, "CONFIG_CONFLICT", "配置已改变，请重新读取后修改")
	}
	return err
}

func (s *RiskService) CheckOrder(ctx context.Context, req *v1.CheckOrderRequest) (*v1.CheckOrderReply, error) {
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	var expected *int
	if req.ExpectedConfigRevision != nil {
		n := int(*req.ExpectedConfigRevision)
		expected = &n
	}
	d, lat := s.e.Check(ctx, risk.Order{
		ExpectedConfigRevision: expected,
		ClientID:               strings.TrimSpace(req.GetClientOrderId()),
		Account:                risk.AccountType(strings.ToUpper(strings.TrimSpace(req.GetAccountType()))),
		Symbol:                 strings.TrimSpace(req.GetSymbol()),
		Side:                   risk.Side(strings.ToLower(strings.TrimSpace(req.GetSide()))),
		Price:                  req.GetPrice(),
		Volume:                 int(req.GetVolume()),
		Source:                 risk.Source(strings.ToLower(strings.TrimSpace(req.GetSource()))),
		Operator:               strings.TrimSpace(req.GetOperator()),
		Sector:                 strings.TrimSpace(req.GetSector()),
	})
	out := &v1.CheckOrderReply{
		Approved: d.Approved, Volume: int32(d.Volume), Adjusted: d.Adjusted,
		Rule: d.Rule, Reason: d.Reason, LatencyUs: lat.Microseconds(),
	}
	for _, r := range d.Results {
		out.Results = append(out.Results, &v1.RuleResult{Rule: r.Rule, Name: r.Name, Pass: r.Pass, Note: r.Note, Volume: int32(r.Volume)})
	}
	return out, nil
}

// PreviewOrder 干跑试算（G7）：与 CheckOrder 同一装配与守卫，但不落单、不占预约、不进审计。
func (s *RiskService) PreviewOrder(ctx context.Context, req *v1.CheckOrderRequest) (*v1.PreviewOrderReply, error) {
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	var expected *int
	if req.ExpectedConfigRevision != nil {
		n := int(*req.ExpectedConfigRevision)
		expected = &n
	}
	pv := s.e.Preview(ctx, risk.Order{
		ExpectedConfigRevision: expected,
		ClientID:               strings.TrimSpace(req.GetClientOrderId()),
		Account:                risk.AccountType(strings.ToUpper(strings.TrimSpace(req.GetAccountType()))),
		Symbol:                 strings.TrimSpace(req.GetSymbol()),
		Side:                   risk.Side(strings.ToLower(strings.TrimSpace(req.GetSide()))),
		Price:                  req.GetPrice(),
		Volume:                 int(req.GetVolume()),
		Source:                 risk.Source(strings.ToLower(strings.TrimSpace(req.GetSource()))),
		Operator:               strings.TrimSpace(req.GetOperator()),
		Sector:                 strings.TrimSpace(req.GetSector()),
	})
	out := &v1.PreviewOrderReply{
		Approved: pv.Approved, BlockRule: pv.BlockRule, Reason: pv.Reason,
		Volume: int32(pv.Volume), Adjusted: pv.Adjusted,
		Amount: pv.Amount, Fee: pv.Fee, CashDelta: pv.CashDelta,
	}
	for _, r := range pv.Results {
		out.Results = append(out.Results, &v1.RuleResult{Rule: r.Rule, Name: r.Name, Pass: r.Pass, Note: r.Note, Volume: int32(r.Volume)})
	}
	return out, nil
}

func (s *RiskService) GetState(context.Context, *v1.GetStateRequest) (*v1.StateReply, error) {
	return toState(s.e.State()), nil
}

func (s *RiskService) SetMode(ctx context.Context, req *v1.SetModeRequest) (*v1.StateReply, error) {
	st, err := s.e.SetMode(ctx, req.GetMode(), strings.TrimSpace(req.GetOperator()), req.GetReason())
	if err != nil {
		return nil, mapErr(err)
	}
	return toState(st), nil
}

func (s *RiskService) TriggerKillSwitch(ctx context.Context, req *v1.TriggerKillSwitchRequest) (*v1.StateReply, error) {
	st, err := s.e.TriggerKill(ctx, strings.TrimSpace(req.GetSource()), req.GetReason(), strings.TrimSpace(req.GetOperator()))
	if err != nil && st.Kill.Active {
		// 内存里已经停了，落库失败只影响重启后的恢复，照样返回状态并带上错误。
		return toState(st), err
	}
	if err != nil {
		return nil, mapErr(err)
	}
	return toState(st), nil
}

func (s *RiskService) ResetKillSwitch(ctx context.Context, req *v1.ResetKillSwitchRequest) (*v1.StateReply, error) {
	st, err := s.e.ResetKill(ctx, strings.TrimSpace(req.GetOperator()), strings.TrimSpace(req.GetConfirm()), req.GetReason())
	if err != nil {
		return nil, mapErr(err)
	}
	return toState(st), nil
}

func (s *RiskService) UpdateParam(ctx context.Context, req *v1.UpdateParamRequest) (*v1.StateReply, error) {
	if req.BaseRevision == nil || req.GetBaseRevision() < 0 {
		return nil, kerrors.New(428, "CONFIG_BASE_REQUIRED", "保存需base_revision")
	}
	ctx = params.WithBase(ctx, int(req.GetBaseRevision()))
	key := risk.KeyPrefix + strings.TrimPrefix(strings.TrimSpace(req.GetKey()), risk.KeyPrefix)
	if _, err := params.ValidatePatch(s.e.State().Params, map[string]string{key: req.GetValue()}); err != nil {
		return nil, kerrors.BadRequest("CONFIG_INVALID", err.Error())
	}
	st, err := s.e.UpdateParam(ctx, req.GetKey(), req.GetValue(), strings.TrimSpace(req.GetOperator()))
	if err != nil {
		return nil, mapErr(err)
	}
	return toState(st), nil
}

func (s *RiskService) DailyReport(ctx context.Context, req *v1.DailyReportRequest) (*v1.DailyReportReply, error) {
	date, r, err := s.e.Report(ctx, strings.TrimSpace(req.GetDate()))
	if err != nil {
		return nil, mapErr(err)
	}
	out := &v1.DailyReportReply{
		Date: date, Checks: int32(r.Checks), Approved: int32(r.Approved), Rejected: int32(r.Rejected),
		Adjusted: int32(r.Adjusted), P99LatencyUs: r.P99Latency.Microseconds(),
	}
	rules := make([]string, 0, len(r.Rejects))
	for k := range r.Rejects {
		rules = append(rules, k)
	}
	sort.Strings(rules)
	for _, k := range rules {
		out.Rejects = append(out.Rejects, &v1.RuleCount{Rule: k, Name: risk.RuleName(k), Count: int32(r.Rejects[k])})
	}
	for _, e := range r.Events {
		out.Events = append(out.Events, &v1.RiskEventItem{
			Kind: e.Kind, Active: e.Active, Mode: e.Mode, Source: e.Source,
			Reason: e.Reason, Operator: e.Operator, At: formatTime(e.At),
		})
	}
	return out, nil
}

// Limits 返回各项限额的上限与当前用量（监控驾驶舱 P1，只读）。
func (s *RiskService) Limits(_ context.Context, req *v1.LimitsRequest) (*v1.LimitsReply, error) {
	out := &v1.LimitsReply{AccountType: strings.ToUpper(strings.TrimSpace(req.GetAccountType()))}
	for _, item := range s.e.Limits(req.GetAccountType()) {
		out.Items = append(out.Items, &v1.LimitItem{
			Key: item.Key, Name: item.Name, Unit: item.Unit,
			Limit: item.Limit, Used: item.Used, Ratio: item.Ratio, Note: item.Note,
		})
	}
	return out, nil
}

func toState(st biz.State) *v1.StateReply {
	configReady := st.ConfigReady
	ready := st.Ready
	out := &v1.StateReply{
		Mode: st.Mode.String(),
		KillSwitch: &v1.KillSwitch{
			Active: st.Kill.Active, Source: st.Kill.Source, Reason: st.Kill.Reason,
			Operator: st.Kill.Operator, At: formatTime(st.Kill.At),
		},
		Breakers:       st.Breakers,
		MarketBreaker:  st.MarketBreaker,
		BuysToday:      int32(st.BuysToday),
		Params:         st.Params,
		AccountAsOf:    map[string]string{},
		LastQuoteAt:    formatTime(st.LastQuoteAt),
		ConfigRevision: int64(st.ConfigRevision),
		ConfigReady:    &configReady,
		Ready:          &ready,
	}
	for k, t := range st.AccountAsOf {
		out.AccountAsOf[k] = formatTime(t)
	}
	return out
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}
