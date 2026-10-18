package service

import (
	"context"
	"encoding/json"
	v1 "server/api/trade/v1"
	"server/app/trade/internal/biz"
)

type AdmissionService struct {
	v1.UnimplementedAdmissionServer
	e *biz.Engine
}

func NewAdmissionService(trade *TradeService) *AdmissionService { return &AdmissionService{e: trade.e} }
func (s *AdmissionService) GetAdmission(ctx context.Context, in *v1.AdmissionEvidenceRequest) (*v1.AdmissionEvidenceReply, error) {
	r, err := s.e.PaperAdmission(ctx, in.GetAccount(), int(in.GetLimit()))
	if err != nil {
		return nil, mapErr(err)
	}
	p, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	return &v1.AdmissionEvidenceReply{ReportJson: string(p), ConsecutiveDays: int32(r.ConsecutiveDays), RequiredDays: int32(r.RequiredDays), RunEvidenceReady: r.RunEvidenceReady, LiveEnabled: false}, nil
}
func (s *AdmissionService) GetEvidence(ctx context.Context, in *v1.AdmissionEvidenceRequest) (*v1.AdmissionEvidenceReply, error) {
	return s.GetAdmission(ctx, in)
}
