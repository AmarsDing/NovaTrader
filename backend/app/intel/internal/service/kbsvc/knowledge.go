// Package kbsvc 是 M04 知识库的 gRPC / HTTP 适配层，只做参数转换和错误映射。
package kbsvc

import (
	"context"
	"errors"
	"time"

	v1 "server/api/intel/v1"
	"server/app/intel/internal/biz/kb"
	"server/pkg/errcode"

	kerrors "github.com/go-kratos/kratos/v2/errors"
)

type KnowledgeService struct {
	v1.UnimplementedKnowledgeServer
	uc *kb.KnowledgeUsecase
}

func NewKnowledgeService(uc *kb.KnowledgeUsecase) *KnowledgeService {
	return &KnowledgeService{uc: uc}
}

// toKratos 把业务错误码映射成 HTTP 400 / 404，其余按 500 返回。
func toKratos(err error) error {
	var e *errcode.Error
	if errors.As(err, &e) {
		switch e.Code {
		case errcode.Invalid:
			return kerrors.BadRequest("KB_INVALID", e.Msg)
		case errcode.NotFound:
			return kerrors.NotFound("KB_NOT_FOUND", e.Msg)
		}
	}
	return err
}

func badRequest(msg string) error { return kerrors.BadRequest("KB_INVALID", msg) }

func parseTime(field, s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil, badRequest(field + " 必须是 RFC3339 时间")
	}
	return &t, nil
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func toProtoDoc(d *kb.Document) *v1.KbDocument {
	out := &v1.KbDocument{
		Id:          int64(d.ID),
		Category:    d.Category,
		Title:       d.Title,
		Source:      d.Source,
		SourceType:  d.SourceType,
		Author:      d.Author,
		Format:      d.Format,
		FileName:    d.FileName,
		SizeBytes:   d.SizeBytes,
		ContentHash: d.ContentHash,
		Status:      d.Status,
		Version:     int32(d.Version),
		StockCodes:  d.StockCodes,
		Tags:        d.Tags,
		ChunkCount:  int32(d.ChunkCount),
		EmbedStatus: d.EmbedStatus,
		CreatedAt:   formatTime(d.CreatedAt),
		UpdatedAt:   formatTime(d.UpdatedAt),
	}
	if d.ExpiresAt != nil {
		out.ExpiresAt = formatTime(*d.ExpiresAt)
	}
	return out
}

func (s *KnowledgeService) IngestDocument(ctx context.Context, req *v1.KbIngestRequest) (*v1.KbIngestReply, error) {
	expires, err := parseTime("expires_at", req.GetExpiresAt())
	if err != nil {
		return nil, err
	}
	res, err := s.uc.Ingest(ctx, kb.IngestInput{
		Category:   req.GetCategory(),
		Title:      req.GetTitle(),
		Source:     req.GetSource(),
		SourceType: req.GetSourceType(),
		Author:     req.GetAuthor(),
		FileName:   req.GetFileName(),
		Content:    req.GetContent(),
		StockCodes: req.GetStockCodes(),
		Tags:       req.GetTags(),
		ExpiresAt:  expires,
	})
	if err != nil {
		return nil, toKratos(err)
	}
	return &v1.KbIngestReply{Document: toProtoDoc(res.Document), Duplicate: res.Duplicate, Embedded: res.Embedded}, nil
}

func (s *KnowledgeService) GetDocument(ctx context.Context, req *v1.KbGetDocumentRequest) (*v1.KbGetDocumentReply, error) {
	doc, chunks, err := s.uc.GetDocument(ctx, int(req.GetId()))
	if err != nil {
		return nil, toKratos(err)
	}
	out := &v1.KbGetDocumentReply{Document: toProtoDoc(doc)}
	for _, c := range chunks {
		out.Chunks = append(out.Chunks, &v1.KbChunk{
			Id:         int64(c.ID),
			Seq:        int32(c.Seq),
			Heading:    c.Heading,
			Content:    c.Content,
			TokenCount: int32(c.Tokens),
			Embedded:   c.Embedded,
		})
	}
	return out, nil
}

func (s *KnowledgeService) ListDocuments(ctx context.Context, req *v1.KbListDocumentsRequest) (*v1.KbListDocumentsReply, error) {
	docs, total, err := s.uc.ListDocuments(ctx, kb.ListFilter{
		Category: req.GetCategory(),
		Status:   req.GetStatus(),
		Offset:   int(req.GetOffset()),
		Limit:    int(req.GetLimit()),
	})
	if err != nil {
		return nil, toKratos(err)
	}
	out := &v1.KbListDocumentsReply{Total: int32(total)}
	for i := range docs {
		out.Documents = append(out.Documents, toProtoDoc(&docs[i]))
	}
	return out, nil
}

func (s *KnowledgeService) DeleteDocument(ctx context.Context, req *v1.KbDeleteDocumentRequest) (*v1.KbDeleteDocumentReply, error) {
	if err := s.uc.DeleteDocument(ctx, int(req.GetId())); err != nil {
		return nil, toKratos(err)
	}
	return &v1.KbDeleteDocumentReply{}, nil
}

func (s *KnowledgeService) Search(ctx context.Context, req *v1.KbSearchRequest) (*v1.KbSearchReply, error) {
	res, err := s.uc.Search(ctx, kb.SearchInput{
		Query:      req.GetQuery(),
		TopK:       int(req.GetTopK()),
		Categories: req.GetCategories(),
		StockCode:  req.GetStockCode(),
	})
	if err != nil {
		return nil, toKratos(err)
	}
	out := &v1.KbSearchReply{Degraded: res.Degraded}
	for _, h := range res.Hits {
		out.Hits = append(out.Hits, &v1.KbHit{
			DocId:       int64(h.DocID),
			ChunkId:     int64(h.ChunkID),
			Title:       h.Title,
			Heading:     h.Heading,
			Content:     h.Content,
			Source:      h.Source,
			SourceType:  h.SourceType,
			Category:    h.Category,
			Score:       h.Score,
			KeywordRank: int32(h.KeywordRank),
			VectorRank:  int32(h.VectorRank),
		})
	}
	return out, nil
}

func (s *KnowledgeService) SimilarCases(ctx context.Context, req *v1.KbSimilarCasesRequest) (*v1.KbSimilarCasesReply, error) {
	asOf, err := parseTime("as_of", req.GetAsOf())
	if err != nil {
		return nil, err
	}
	in := kb.SimilarInput{
		Situation:    req.GetSituation(),
		Pattern:      req.GetPattern(),
		EmotionPhase: req.GetEmotionPhase(),
		StockCode:    req.GetStockCode(),
		Strategy:     req.GetStrategy(),
		Book:         req.GetBook(),
		TopK:         int(req.GetTopK()),
	}
	if asOf != nil {
		in.AsOf = *asOf
	}
	res, err := s.uc.SimilarCases(ctx, in)
	if err != nil {
		return nil, toKratos(err)
	}
	out := &v1.KbSimilarCasesReply{Degraded: res.Degraded}
	for _, h := range res.Cases {
		out.Cases = append(out.Cases, &v1.KbCaseHit{Case: toProtoCase(h.Case), ChunkId: int64(h.ChunkID), Score: h.Score})
	}
	return out, nil
}

func toProtoCase(c kb.Case) *v1.KbCase {
	out := &v1.KbCase{
		Id:           int64(c.ID),
		Book:         c.Book,
		SignalId:     c.SignalID,
		StockCode:    c.StockCode,
		StockName:    c.StockName,
		Strategy:     c.Strategy,
		Pattern:      c.Pattern,
		EmotionPhase: c.EmotionPhase,
		SignalType:   c.SignalType,
		FinalStatus:  c.FinalStatus,
		SignalTime:   formatTime(c.SignalTime),
		ClosedAt:     formatTime(c.ClosedAt),
		EntryPrice:   c.EntryPrice,
		ExitPrice:    c.ExitPrice,
		PnlPct:       c.PnlPct,
		Outcome:      c.Outcome,
		Attribution:  c.Attribution,
	}
	if c.HoldingDays != nil {
		d := int32(*c.HoldingDays)
		out.HoldingDays = &d
	}
	if c.DocID != nil {
		out.DocId = int64(*c.DocID)
	}
	return out
}

// SignalCaseFromProto 把接口或事件里的信号转成业务结构，事件消费端也用它。
func SignalCaseFromProto(req *v1.KbSignalCase) (kb.SignalCase, error) {
	signalTime, err := parseTime("signal_time", req.GetSignalTime())
	if err != nil {
		return kb.SignalCase{}, err
	}
	closedAt, err := parseTime("closed_at", req.GetClosedAt())
	if err != nil {
		return kb.SignalCase{}, err
	}
	s := kb.SignalCase{
		SignalID:     req.GetSignalId(),
		Book:         req.GetBook(),
		Status:       req.GetStatus(),
		StockCode:    req.GetStockCode(),
		StockName:    req.GetStockName(),
		Strategy:     req.GetStrategy(),
		SignalType:   req.GetSignalType(),
		EntryPrice:   req.EntryPrice,
		ExitPrice:    req.ExitPrice,
		PnlPct:       req.PnlPct,
		Pattern:      req.GetPattern(),
		EmotionPhase: req.GetEmotionPhase(),
		Attribution:  req.GetAttribution(),
		Reasoning:    req.GetReasoning(),
		Review:       req.GetReview(),
	}
	if signalTime != nil {
		s.SignalTime = *signalTime
	}
	if closedAt != nil {
		s.ClosedAt = *closedAt
	}
	if req.HoldingDays != nil {
		d := int(*req.HoldingDays)
		s.HoldingDays = &d
	}
	return s, nil
}

func (s *KnowledgeService) RecordCase(ctx context.Context, req *v1.KbSignalCase) (*v1.KbRecordCaseReply, error) {
	sc, err := SignalCaseFromProto(req)
	if err != nil {
		return nil, err
	}
	res, err := s.uc.RecordCase(ctx, sc)
	if err != nil {
		return nil, toKratos(err)
	}
	return &v1.KbRecordCaseReply{CaseId: int64(res.CaseID), DocId: int64(res.DocID), Created: res.Created}, nil
}
