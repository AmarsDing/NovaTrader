package service

import (
	"context"
	"github.com/google/wire"
	v1 "server/api/backtest/v1"
	"server/app/backtest/internal/biz"
	"time"
)

var ResearchProviderSet = wire.NewSet(NewResearchService)

type ResearchService struct {
	v1.UnimplementedResearchServer
	uc *biz.ResearchUsecase
}

func NewResearchService(uc *biz.ResearchUsecase) *ResearchService { return &ResearchService{uc: uc} }
func researchReply(row *biz.ResearchJob) *v1.ResearchJob {
	return &v1.ResearchJob{RunId: row.RunID, Kind: row.Kind, ManifestId: row.ManifestID, ManifestHash: row.ManifestHash, InputHash: row.InputHash, Status: row.Status, Reason: row.Reason, CancelRequested: row.CancelRequested, Revision: row.Revision, AsOf: fmtTime(row.AsOf), CreatedAt: fmtTime(row.CreatedAt), StartedAt: fmtTimePtr(row.StartedAt), FinishedAt: fmtTimePtr(row.FinishedAt), ResultJson: row.ResultJSON, ArtifactHash: row.ArtifactHash, TimeoutSeconds: int32(row.TimeoutSeconds)}
}
func (s *ResearchService) CreateResearch(ctx context.Context, r *v1.CreateResearchRequest) (*v1.ResearchJob, error) {
	asOf, err := time.Parse(time.RFC3339Nano, r.GetAsOf())
	if err != nil {
		return nil, mapErr(biz.ErrBadRequest)
	}
	row, err := s.uc.CreateResearch(ctx, biz.ResearchRequest{Kind: r.Kind, ManifestID: r.ManifestId, InputJSON: r.InputJson, OptionsJSON: r.OptionsJson, AsOf: asOf, Seed: r.Seed, TimeoutSeconds: int(r.TimeoutSeconds)}, operator(ctx))
	if err != nil {
		return nil, mapErr(err)
	}
	return researchReply(row), nil
}
func (s *ResearchService) GetResearch(ctx context.Context, r *v1.GetResearchRequest) (*v1.ResearchJob, error) {
	row, err := s.uc.GetResearch(ctx, r.RunId)
	if err != nil {
		return nil, mapErr(err)
	}
	return researchReply(row), nil
}
func (s *ResearchService) ListResearch(ctx context.Context, r *v1.ListResearchRequest) (*v1.ListResearchReply, error) {
	rows, err := s.uc.ListResearch(ctx, r.Status, int(r.Limit), int(r.Offset))
	if err != nil {
		return nil, mapErr(err)
	}
	out := &v1.ListResearchReply{}
	for _, row := range rows {
		out.Jobs = append(out.Jobs, researchReply(row))
	}
	return out, nil
}
func (s *ResearchService) CancelResearch(ctx context.Context, r *v1.GetResearchRequest) (*v1.ResearchJob, error) {
	row, err := s.uc.CancelResearch(ctx, r.RunId)
	if err != nil {
		return nil, mapErr(err)
	}
	return researchReply(row), nil
}
func (s *ResearchService) GetResearchArtifact(ctx context.Context, r *v1.ResearchArtifactRequest) (*v1.ResearchArtifactReply, error) {
	a, err := s.uc.ResearchArtifact(ctx, r.RunId, r.Name)
	if err != nil {
		return nil, mapErr(err)
	}
	return &v1.ResearchArtifactReply{Name: a.Name, Sha256: a.SHA256, Content: a.Content}, nil
}
