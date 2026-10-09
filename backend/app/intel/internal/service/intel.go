// Package service 把 api/intel/v1 的请求转给 biz，只做参数转换。
package service

import (
	"context"
	"errors"
	"time"

	v1 "server/api/intel/v1"
	"server/app/intel/internal/biz"

	kerrors "github.com/go-kratos/kratos/v2/errors"
	"github.com/google/wire"
)

var ProviderSet = wire.NewSet(NewIntelService)

type IntelService struct {
	v1.UnimplementedIntelServer
	uc *biz.Usecase
}

func NewIntelService(uc *biz.Usecase) *IntelService {
	return &IntelService{uc: uc}
}

func mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, biz.ErrInvalid):
		return kerrors.BadRequest("INTEL_INVALID", err.Error())
	case errors.Is(err, biz.ErrNotFound):
		return kerrors.NotFound("INTEL_NOT_FOUND", err.Error())
	}
	return err
}

func (s *IntelService) Ingest(ctx context.Context, req *v1.IngestRequest) (*v1.IngestReply, error) {
	res, err := s.uc.Process(ctx, biz.Raw{
		Source: req.GetSource(), SourceID: req.GetSourceId(), Kind: req.GetKind(),
		Title: req.GetTitle(), Content: req.GetContent(), URL: req.GetUrl(),
		PublishTime: req.GetPublishTime(), Codes: req.GetCodes(),
	})
	if err != nil {
		return nil, mapErr(err)
	}
	return &v1.IngestReply{NewsId: int64(res.NewsID), ClusterId: int64(res.ClusterID), Status: res.Status, Reason: res.Reason}, nil
}

func (s *IntelService) GetItem(ctx context.Context, req *v1.GetItemRequest) (*v1.Item, error) {
	v, err := s.uc.GetItem(ctx, int(req.GetId()))
	if err != nil {
		return nil, mapErr(err)
	}
	return toItem(v), nil
}

func (s *IntelService) Timeline(ctx context.Context, req *v1.TimelineRequest) (*v1.TimelineReply, error) {
	since, err := optionalTime(req.GetSince())
	if err != nil {
		return nil, err
	}
	until, err := optionalTime(req.GetUntil())
	if err != nil {
		return nil, err
	}
	code, composite, items, err := s.uc.Timeline(ctx, req.GetCode(), since, until, int(req.GetLimit()))
	if err != nil {
		return nil, mapErr(err)
	}
	out := &v1.TimelineReply{Code: code, CompositeSentiment: composite, Items: make([]*v1.Item, 0, len(items))}
	for _, v := range items {
		out.Items = append(out.Items, toItem(v))
	}
	return out, nil
}

func (s *IntelService) HotWords(ctx context.Context, req *v1.HotWordsRequest) (*v1.HotWordsReply, error) {
	words, err := s.uc.HotWords(ctx, time.Duration(req.GetWindowMinutes())*time.Minute, int(req.GetLimit()), req.GetType())
	if err != nil {
		return nil, mapErr(err)
	}
	out := &v1.HotWordsReply{Items: make([]*v1.HotWord, 0, len(words))}
	for _, w := range words {
		out.Items = append(out.Items, &v1.HotWord{Type: w.Type, Target: w.Target, Count: int32(w.Count), PrevCount: int32(w.PrevCount), Score: w.Score})
	}
	return out, nil
}

func (s *IntelService) UpsertAlias(ctx context.Context, req *v1.UpsertAliasRequest) (*v1.UpsertAliasReply, error) {
	enabled := true
	if req.Enabled != nil {
		enabled = req.GetEnabled()
	}
	id, err := s.uc.UpsertAlias(ctx, biz.AliasInput{
		Alias: req.GetAlias(), StockCode: req.GetStockCode(), Kind: req.GetKind(),
		Confidence: req.GetConfidence(), Enabled: enabled,
	})
	if err != nil {
		return nil, mapErr(err)
	}
	return &v1.UpsertAliasReply{Id: int64(id)}, nil
}

func (s *IntelService) ReloadAliases(ctx context.Context, _ *v1.ReloadAliasesRequest) (*v1.ReloadAliasesReply, error) {
	d, err := s.uc.ReloadDictionary(ctx)
	if err != nil {
		return nil, mapErr(err)
	}
	return &v1.ReloadAliasesReply{Stocks: int32(d.Stocks), Aliases: int32(d.Aliases), Concepts: int32(d.Concepts)}, nil
}

func optionalTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, kerrors.BadRequest("INTEL_INVALID", "time must be RFC3339: "+s)
	}
	return t, nil
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func toItem(v *biz.ItemView) *v1.Item {
	out := &v1.Item{
		Id: int64(v.ID), ClusterId: int64(v.ClusterID), Kind: v.Kind, Source: v.Source, SourceId: v.SourceID,
		Url: v.URL, Title: v.Title, Content: v.Content, PublishTime: formatTime(v.PublishTime),
		EventType: v.EventType, Sentiment: v.Sentiment, Importance: int32(v.Importance),
		HalfLifeMinutes: int32(v.HalfLifeMinutes), EffectiveSentiment: v.Effective, Scorer: v.Scorer,
		Degraded: v.Degraded, Status: v.Status, Reason: v.Reason, ClusterSize: int32(v.ClusterSize),
	}
	for _, l := range v.Links {
		out.Links = append(out.Links, &v1.Link{Type: l.TargetType, Target: l.Target, Confidence: l.Confidence, Method: l.Method, Matched: l.Matched})
	}
	for _, f := range v.Facts {
		fact := &v1.Fact{Type: f.Type, Unit: f.Unit, Text: f.Text, Start: int32(f.Start), End: int32(f.End)}
		if f.Value != nil {
			fact.Value = *f.Value
		}
		out.Facts = append(out.Facts, fact)
	}
	return out
}
