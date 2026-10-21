package service

import (
	"context"
	"encoding/json"
	"strings"

	v1 "server/api/trade/v1"
	"server/app/trade/internal/biz"
	"server/pkg/admission"

	"github.com/go-kratos/kratos/v2/transport"
)

type AdmissionService struct {
	v1.UnimplementedAdmissionServer
	e *biz.Engine
}

func NewAdmissionService(trade *TradeService) *AdmissionService { return &AdmissionService{e: trade.e} }

// admissionReply 统一拼装证据回执：报告 JSON 原样透传（时间戳/哈希零损耗），
// live_enabled/broker_connected 如实取双闸当前状态（T05）。
func admissionReply(e *biz.Engine, r admission.Report) (*v1.AdmissionEvidenceReply, error) {
	p, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	return &v1.AdmissionEvidenceReply{
		ReportJson: string(p), ConsecutiveDays: int32(r.ConsecutiveDays), RequiredDays: int32(r.RequiredDays),
		RunEvidenceReady: r.RunEvidenceReady, LiveEnabled: e.LiveEnabled(), BrokerConnected: e.BrokerConnected(),
	}, nil
}

func (s *AdmissionService) GetAdmission(ctx context.Context, in *v1.AdmissionEvidenceRequest) (*v1.AdmissionEvidenceReply, error) {
	r, err := s.e.PaperAdmission(ctx, in.GetAccount(), int(in.GetLimit()))
	if err != nil {
		return nil, mapErr(err)
	}
	return admissionReply(s.e, r)
}
func (s *AdmissionService) GetEvidence(ctx context.Context, in *v1.AdmissionEvidenceRequest) (*v1.AdmissionEvidenceReply, error) {
	return s.GetAdmission(ctx, in)
}

// GrantLive/RevokeLive 是 LIVE 总闸的唯一开关（M13-T05）：准入评估、审计留痕
// 核验、持久化与翻闸门全部收口在 biz；admin 网关负责 Owner 鉴权并先行写
// audit_log（trade.grant_live / trade.revoke_live），空 operator 一律拒绝。
func (s *AdmissionService) GrantLive(ctx context.Context, in *v1.GrantLiveRequest) (*v1.AdmissionEvidenceReply, error) {
	r, err := s.e.GrantLive(ctx, operatorIdentity(ctx, in.GetOperator()))
	if err != nil {
		return nil, mapErr(err)
	}
	return admissionReply(s.e, r)
}
func (s *AdmissionService) RevokeLive(ctx context.Context, in *v1.RevokeLiveRequest) (*v1.AdmissionEvidenceReply, error) {
	if err := s.e.RevokeLive(ctx, operatorIdentity(ctx, in.GetOperator())); err != nil {
		return nil, mapErr(err)
	}
	return s.GetAdmission(ctx, &v1.AdmissionEvidenceRequest{Account: "SIM"})
}

// operatorIdentity 优先取网关注入的 X-User 头（与网关先写的 audit actor 同一身份），
// 直连 gRPC 无头时退回请求体 operator。身份与审计错位的调用会被 biz 的
// RecentLiveGateAudit 核验 fail-closed 拒掉——这里只做取口，不做豁免。
func operatorIdentity(ctx context.Context, fallback string) string {
	if tr, ok := transport.FromServerContext(ctx); ok {
		if v := strings.TrimSpace(tr.RequestHeader().Get("X-User")); v != "" {
			return v
		}
	}
	return fallback
}
