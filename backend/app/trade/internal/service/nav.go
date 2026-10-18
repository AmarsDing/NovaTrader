package service

import (
	"context"
	"encoding/json"
	kerrors "github.com/go-kratos/kratos/v2/errors"
	v1 "server/api/trade/v1"
	"server/app/trade/internal/biz"
	"server/pkg/accountnav"
	"time"
)

type NavService struct {
	v1.UnimplementedNavServer
	e *biz.Engine
}

func NewNavService(trade *TradeService) *NavService { return &NavService{e: trade.e} }
func (s *NavService) ExportNav(ctx context.Context, in *v1.NavExportRequest) (*v1.NavExportReply, error) {
	req := accountnav.Request{Account: in.GetAccount(), StartDate: in.GetStartDate(), EndDate: in.GetEndDate(), Frequency: in.GetFrequency(), ManifestID: in.GetManifestId(), BenchmarkJSON: in.GetBenchmarkJson()}
	if in.GetAsOf() != "" {
		at, err := time.Parse(time.RFC3339Nano, in.GetAsOf())
		if err != nil {
			return nil, kerrors.BadRequest("NAV_INVALID_CUTOFF", "as_of requires RFC3339 timezone")
		}
		req.AsOf = at
	}
	result, err := s.e.ExportNAV(ctx, req)
	if err != nil {
		return nil, mapErr(err)
	}
	p, err := json.Marshal(result.Report)
	if err != nil {
		return nil, err
	}
	return &v1.NavExportReply{ReportJson: string(p), InputJson: result.InputJSON, InputSha256: result.InputSHA256, InputReady: result.InputReady, LedgerHash: result.Report.LedgerHash}, nil
}
