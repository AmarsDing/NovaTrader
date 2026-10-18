package service

import (
	"context"
	"encoding/json"
	"google.golang.org/protobuf/types/known/structpb"
	v1 "server/api/trade/v1"
)

func jsonStruct(in any) (*structpb.Struct, error) {
	b, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err = json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return structpb.NewStruct(m)
}
func (s *TradeService) CreatePlan(ctx context.Context, in *v1.CreatePlanRequest) (*v1.PlanReply, error) {
	r, err := s.e.CreatePlan(ctx, int(in.SignalId), in.Account, in.PlanId, int(in.BaseRevision))
	if err != nil {
		return nil, mapErr(err)
	}
	out, err := jsonStruct(r)
	return &v1.PlanReply{Record: out}, err
}
func (s *TradeService) ListPlans(ctx context.Context, in *v1.ListPlansRequest) (*v1.PlansReply, error) {
	rows, err := s.e.Plans(ctx, in.Account)
	if err != nil {
		return nil, mapErr(err)
	}
	out := &v1.PlansReply{Plans: []*structpb.Struct{}}
	for _, r := range rows {
		p, err := jsonStruct(r)
		if err != nil {
			return nil, err
		}
		out.Plans = append(out.Plans, p)
	}
	return out, nil
}
func (s *TradeService) AuthorizePlan(ctx context.Context, in *v1.AuthorizePlanRequest) (*v1.PlanReply, error) {
	r, err := s.e.AuthorizePlan(ctx, in.PlanId, int(in.Revision), in.Mode, in.Operator)
	if err != nil {
		return nil, mapErr(err)
	}
	out, err := jsonStruct(r)
	return &v1.PlanReply{Record: out}, err
}
func (s *TradeService) ExecutePlan(ctx context.Context, in *v1.ExecutePlanRequest) (*v1.Order, error) {
	r, err := s.e.ExecutePlan(ctx, in.PlanId, int(in.Revision), in.Operator)
	if err != nil {
		return nil, mapErr(err)
	}
	return toOrder(r), nil
}
func (s *TradeService) ListIntents(ctx context.Context, in *v1.ListPlansRequest) (*v1.IntentsReply, error) {
	rows, err := s.e.Intents(ctx, in.Account)
	if err != nil {
		return nil, mapErr(err)
	}
	out := &v1.IntentsReply{Intents: []*structpb.Struct{}}
	for _, r := range rows {
		p, err := jsonStruct(r)
		if err != nil {
			return nil, err
		}
		out.Intents = append(out.Intents, p)
	}
	return out, nil
}
