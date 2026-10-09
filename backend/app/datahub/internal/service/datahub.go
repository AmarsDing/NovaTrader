package service

import (
	"context"
	"errors"
	"strconv"
	"time"

	v1 "server/api/datahub/v1"
	"server/app/datahub/internal/biz"
	"server/pkg/tradecal"

	kerrors "github.com/go-kratos/kratos/v2/errors"
	"github.com/google/wire"
)

var ProviderSet = wire.NewSet(NewDatahubService)

type DatahubService struct {
	v1.UnimplementedDatahubServer
	c *biz.Collector
}

func NewDatahubService(c *biz.Collector) *DatahubService {
	return &DatahubService{c: c}
}

func mapErr(err error) error {
	if err == nil {
		return nil
	}
	var ns *biz.NoSourceError
	if errors.As(err, &ns) {
		return kerrors.ServiceUnavailable("DATAHUB_NO_SOURCE", err.Error())
	}
	if errors.Is(err, biz.ErrUnsupported) {
		return kerrors.BadRequest("DATAHUB_UNSUPPORTED", err.Error())
	}
	var se *biz.StaleError
	if errors.As(err, &se) {
		return kerrors.ServiceUnavailable("DATAHUB_STALE", err.Error())
	}
	if len(err.Error()) >= 8 && err.Error()[:8] == "datahub:" {
		return kerrors.BadRequest("DATAHUB_INVALID", err.Error())
	}
	return err
}

func (s *DatahubService) ListSources(ctx context.Context, req *v1.ListSourcesRequest) (*v1.ListSourcesReply, error) {
	rows := s.c.Pipeline().Health()
	out := &v1.ListSourcesReply{Sources: make([]*v1.SourceStatus, 0, len(rows))}
	for _, h := range rows {
		if req.GetDomain() != "" && h.Domain != req.GetDomain() {
			continue
		}
		out.Sources = append(out.Sources, &v1.SourceStatus{
			Domain: h.Domain, Source: h.Source, Priority: int32(h.Priority), Enabled: h.Enabled, Official: h.Official,
			State: h.State, ConsecutiveFailures: int32(h.Failures), SuccessCount: h.Success, FailureCount: h.Failure,
			SuccessRate: h.SuccessRate(), LastLatencyMs: h.LastLatency.Milliseconds(), AvgLatencyMs: h.AvgLatency.Milliseconds(),
			LastSuccessAt: rfc(h.LastSuccessAt), LastFailureAt: rfc(h.LastFailureAt), LastError: h.LastError,
			QuotaUsed: h.QuotaUsed, QuotaLimit: h.QuotaLimit, Active: h.Active,
		})
	}
	if meta, err := s.c.SnapshotMeta(ctx); err == nil && !meta.AsOf.IsZero() {
		out.Snapshot = map[string]string{
			"as_of": meta.AsOf.Format(time.RFC3339), "source": meta.Source,
			"count": strconv.Itoa(meta.Count), "latency_ms": strconv.FormatInt(meta.LatencyMs, 10),
			"stale": strconv.FormatBool(meta.Stale),
		}
	}
	return out, nil
}

func (s *DatahubService) SetSourceEnabled(_ context.Context, req *v1.SetSourceEnabledRequest) (*v1.SetSourceEnabledReply, error) {
	if req.GetSource() == "" {
		return nil, kerrors.BadRequest("DATAHUB_INVALID", "source is required")
	}
	if !s.c.Pipeline().SetSourceEnabled(req.GetSource(), req.GetEnabled()) {
		return nil, kerrors.NotFound("DATAHUB_NOT_FOUND", "没有源 "+req.GetSource())
	}
	return &v1.SetSourceEnabledReply{Source: req.GetSource(), Enabled: req.GetEnabled()}, nil
}

func (s *DatahubService) Backfill(ctx context.Context, req *v1.BackfillRequest) (*v1.BackfillJob, error) {
	start, err := parseDay(req.GetStartDate())
	if err != nil {
		return nil, kerrors.BadRequest("DATAHUB_INVALID", err.Error())
	}
	end, err := parseDay(req.GetEndDate())
	if err != nil {
		return nil, kerrors.BadRequest("DATAHUB_INVALID", err.Error())
	}
	job, err := s.c.SubmitBackfill(ctx, biz.BackfillJob{
		Domain: req.GetDomain(), Start: start, End: end, Symbols: req.GetSymbols(),
		Source: req.GetSource(), RequestedBy: req.GetRequestedBy(),
	})
	if err != nil {
		return nil, mapErr(err)
	}
	return toJob(job), nil
}

func (s *DatahubService) GetBackfill(ctx context.Context, req *v1.GetBackfillRequest) (*v1.BackfillJob, error) {
	job, err := s.c.GetBackfill(ctx, int(req.GetId()))
	if err != nil {
		return nil, kerrors.NotFound("DATAHUB_NOT_FOUND", err.Error())
	}
	return toJob(job), nil
}

func (s *DatahubService) Collect(ctx context.Context, req *v1.CollectRequest) (*v1.CollectReply, error) {
	if req.GetDomain() == "" {
		return nil, kerrors.BadRequest("DATAHUB_INVALID", "domain is required")
	}
	r, err := s.c.RunDomain(ctx, req.GetDomain(), req.GetDate(), "")
	if err != nil {
		return &v1.CollectReply{Domain: r.Domain, Source: r.Source, Rows: int64(r.Rows), Stale: r.Stale, Message: err.Error()}, mapErr(err)
	}
	return &v1.CollectReply{Domain: r.Domain, Source: r.Source, Rows: int64(r.Rows), Stale: r.Stale, Message: r.Message}, nil
}

func (s *DatahubService) ListQualityIssues(ctx context.Context, req *v1.ListQualityIssuesRequest) (*v1.ListQualityIssuesReply, error) {
	var day *time.Time
	if req.GetTradeDate() != "" {
		t, err := parseDay(req.GetTradeDate())
		if err != nil {
			return nil, kerrors.BadRequest("DATAHUB_INVALID", err.Error())
		}
		day = &t
	}
	rows, err := s.c.ListIssues(ctx, day, req.GetDomain(), int(req.GetLimit()))
	if err != nil {
		return nil, err
	}
	out := &v1.ListQualityIssuesReply{Issues: make([]*v1.QualityIssue, 0, len(rows))}
	for _, is := range rows {
		out.Issues = append(out.Issues, &v1.QualityIssue{
			Id: int64(is.ID), Domain: is.Domain, CheckName: is.Check, Symbol: is.Symbol,
			TradeDate: is.TradeDate.Format("2006-01-02"), Severity: is.Severity, Source: is.Source,
			OtherSource: is.OtherSource, Message: is.Message, Hits: int32(is.Hits), LastSeen: rfc(is.LastSeen),
		})
	}
	return out, nil
}

func parseDay(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, errors.New("date is required")
	}
	t, err := time.ParseInLocation("2006-01-02", s, tradecal.Shanghai())
	if err != nil {
		return time.Time{}, errors.New("date want YYYY-MM-DD")
	}
	return t, nil
}

func rfc(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func toJob(j biz.BackfillJob) *v1.BackfillJob {
	out := &v1.BackfillJob{
		Id: int64(j.ID), Domain: j.Domain, StartDate: j.Start.Format("2006-01-02"), EndDate: j.End.Format("2006-01-02"),
		Symbols: j.Symbols, Source: j.Source, Status: j.Status, Total: int32(j.Total), Done: int32(j.Done),
		Rows: j.Rows, Error: j.Error, CreatedAt: rfc(j.CreatedAt),
	}
	if j.FinishedAt != nil {
		out.FinishedAt = rfc(*j.FinishedAt)
	}
	return out
}
