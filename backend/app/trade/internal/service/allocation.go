package service

import (
	"context"
	"encoding/json"
	v1 "server/api/trade/v1"
)

func (s *TradeService) PreviewPortfolio(ctx context.Context, in *v1.PortfolioPreviewRequest) (*v1.PortfolioPreviewReply, error) {
	out, err := s.e.PreviewPortfolio(ctx, in.ResearchRunId, in.Account)
	if err != nil {
		return nil, mapErr(err)
	}
	body, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	return &v1.PortfolioPreviewReply{PreviewJson: string(body)}, nil
}
