// Package service 把 api/backtest/v1 的请求转给 biz，只做类型转换。
package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	v1 "server/api/backtest/v1"
	"server/app/backtest/internal/biz"
	"server/pkg/backtest"

	kerrors "github.com/go-kratos/kratos/v2/errors"
	"github.com/go-kratos/kratos/v2/transport"
	"github.com/google/wire"
	"google.golang.org/protobuf/types/known/structpb"
)

var ProviderSet = wire.NewSet(NewBacktestService)

type BacktestService struct {
	v1.UnimplementedBacktestServer
	uc *biz.Usecase
}

func NewBacktestService(uc *biz.Usecase) *BacktestService { return &BacktestService{uc: uc} }

func mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, biz.ErrNotFound):
		return kerrors.NotFound("NOT_FOUND", err.Error())
	case errors.Is(err, biz.ErrBadRequest):
		return kerrors.BadRequest("BAD_REQUEST", err.Error())
	case errors.Is(err, biz.ErrConflict):
		return kerrors.Conflict("CONFLICT", err.Error())
	default:
		return err
	}
}

func operator(ctx context.Context) string {
	tr, ok := transport.FromServerContext(ctx)
	if !ok {
		return ""
	}
	return tr.RequestHeader().Get("X-User")
}

func day(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return backtest.ParseDay(s)
}

func fmtDay(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return backtest.Day(t).Format("2006-01-02")
}

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func fmtTimePtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return fmtTime(*t)
}

func toStruct(v any) (*structpb.Struct, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string]any{}
	}
	return structpb.NewStruct(m)
}

func rawOf(s *structpb.Struct) json.RawMessage {
	if s == nil {
		return nil
	}
	b, err := s.MarshalJSON()
	if err != nil || string(b) == "null" {
		return nil
	}
	return b
}

func (s *BacktestService) ListStrategies(ctx context.Context, _ *v1.ListStrategiesRequest) (*v1.ListStrategiesReply, error) {
	list, err := s.uc.Strategies(ctx)
	if err != nil {
		return nil, mapErr(err)
	}
	out := &v1.ListStrategiesReply{}
	for _, it := range list {
		info := &v1.StrategyInfo{Name: it.Spec.Name, Title: it.Spec.Title, Evolve: it.Evolve}
		for _, p := range it.Spec.Params {
			info.Params = append(info.Params, &v1.ParamInfo{
				Name: p.Name, Title: p.Title, Default: p.Default, Min: p.Min, Max: p.Max, Step: p.Step,
				Current: it.Current[p.Name], Key: it.Spec.ParamKey(p.Name),
			})
		}
		out.Strategies = append(out.Strategies, info)
	}
	out.Defaults, err = toStruct(backtest.DefaultConfig())
	return out, err
}

func (s *BacktestService) CreateTask(ctx context.Context, req *v1.CreateTaskRequest) (*v1.Task, error) {
	t, err := s.uc.CreateTask(ctx, biz.CreateRequest{
		Kind: req.GetKind(), Name: req.GetName(),
		Config: rawOf(req.GetConfig()), Search: rawOf(req.GetSearch()), CreatedBy: operator(ctx),
	})
	if err != nil {
		return nil, mapErr(err)
	}
	return toTask(t)
}

func (s *BacktestService) ListTasks(ctx context.Context, req *v1.ListTasksRequest) (*v1.ListTasksReply, error) {
	rows, total, err := s.uc.ListTasks(ctx, biz.TaskFilter{
		Status: req.GetStatus(), Strategy: req.GetStrategy(), Kind: req.GetKind(),
		Limit: int(req.GetLimit()), Offset: int(req.GetOffset()),
	})
	if err != nil {
		return nil, mapErr(err)
	}
	out := &v1.ListTasksReply{Total: int32(total)}
	for _, row := range rows {
		t, err := toTask(row)
		if err != nil {
			return nil, err
		}
		out.Tasks = append(out.Tasks, t)
	}
	return out, nil
}

func (s *BacktestService) GetTask(ctx context.Context, req *v1.GetTaskRequest) (*v1.Task, error) {
	t, err := s.uc.GetTask(ctx, int(req.GetId()))
	if err != nil {
		return nil, mapErr(err)
	}
	return toTask(t)
}

func (s *BacktestService) CancelTask(ctx context.Context, req *v1.GetTaskRequest) (*v1.Task, error) {
	t, err := s.uc.CancelTask(ctx, int(req.GetId()))
	if err != nil {
		return nil, mapErr(err)
	}
	return toTask(t)
}

func (s *BacktestService) RerunTask(ctx context.Context, req *v1.GetTaskRequest) (*v1.Task, error) {
	t, err := s.uc.RerunTask(ctx, int(req.GetId()), operator(ctx))
	if err != nil {
		return nil, mapErr(err)
	}
	return toTask(t)
}

func toTask(t *biz.Task) (*v1.Task, error) {
	cfg, err := toStruct(t.Spec)
	if err != nil {
		return nil, err
	}
	out := &v1.Task{
		Id: int64(t.ID), Kind: t.Kind, Name: t.Name, Strategy: t.Strategy, Freq: t.Freq,
		StartDate: fmtDay(t.Start), EndDate: fmtDay(t.End), Status: t.Status, Progress: t.Progress,
		Message: t.Message, Error: t.Error, ParamsHash: t.ParamsHash, EngineVersion: t.EngineVersion,
		BuildVersion: t.BuildVersion, Seed: t.Seed, DataHash: t.DataHash, DataCutoff: fmtTimePtr(t.DataCutoff),
		CreatedBy: t.CreatedBy, CreatedAt: fmtTime(t.CreatedAt), StartedAt: fmtTimePtr(t.StartedAt),
		FinishedAt: fmtTimePtr(t.FinishedAt), Config: cfg,
	}
	if t.RerunOf != nil {
		out.RerunOf = int64(*t.RerunOf)
	}
	if s := t.Summary; s != nil {
		out.Summary = &v1.TaskSummary{
			TotalReturn: s.TotalReturn, AnnualReturn: s.AnnualReturn, MaxDrawdown: s.MaxDrawdown,
			Sharpe: s.Sharpe, WinRate: s.WinRate, TradeCount: int32(s.Rounds), HasReport: true,
		}
	}
	return out, nil
}

func (s *BacktestService) GetReport(ctx context.Context, req *v1.GetTaskRequest) (*v1.ReportReply, error) {
	raw, err := s.uc.Report(ctx, int(req.GetId()))
	if err != nil {
		return nil, mapErr(err)
	}
	st, err := toStruct(json.RawMessage(raw))
	if err != nil {
		return nil, err
	}
	return &v1.ReportReply{TaskId: req.GetId(), Report: st}, nil
}

func (s *BacktestService) ListTrades(ctx context.Context, req *v1.ListTradesRequest) (*v1.ListTradesReply, error) {
	rows, total, err := s.uc.Trades(ctx, biz.TradeFilter{
		TaskID: int(req.GetId()), Symbol: req.GetSymbol(), Limit: int(req.GetLimit()), Offset: int(req.GetOffset()),
	})
	if err != nil {
		return nil, mapErr(err)
	}
	out := &v1.ListTradesReply{Total: int32(total)}
	for _, t := range rows {
		out.Trades = append(out.Trades, &v1.Trade{
			Seq: int32(t.Seq), Symbol: t.Symbol, Side: t.Side, Time: fmtTime(t.Time),
			Price: t.Price, Quantity: int32(t.Qty), Amount: t.Amount, Fee: t.Fee,
			Reason: t.Reason, Pnl: t.PnL, RoundId: int32(t.RoundID),
		})
	}
	return out, nil
}

func (s *BacktestService) ShadowStats(ctx context.Context, req *v1.ShadowStatsRequest) (*v1.ShadowStatsReply, error) {
	from, err := day(req.GetStart())
	if err != nil {
		return nil, kerrors.BadRequest("BAD_REQUEST", err.Error())
	}
	to, err := day(req.GetEnd())
	if err != nil {
		return nil, kerrors.BadRequest("BAD_REQUEST", err.Error())
	}
	if !to.IsZero() {
		to = to.AddDate(0, 0, 1)
	}
	st, err := s.uc.ShadowStats(ctx, biz.ShadowFilter{Model: req.GetModel(), PromptVersion: req.GetPromptVersion(), From: from, To: to})
	if err != nil {
		return nil, mapErr(err)
	}
	out := &v1.ShadowStatsReply{Total: int32(st.Total), Pending: int32(st.Pending), Monotonic: st.Monotonic}
	for _, h := range st.Horizons {
		out.Horizons = append(out.Horizons, &v1.HorizonStat{
			Horizon: int32(h.Horizon), Count: int32(h.Count), AvgReturn: h.AvgReturn, WinRate: h.WinRate,
		})
	}
	for _, b := range st.Buckets {
		out.Buckets = append(out.Buckets, &v1.BucketStat{
			Bucket: b.Bucket, Count: int32(b.Count), AvgT1: b.Avg[1], AvgT3: b.Avg[3], AvgT5: b.Avg[5],
		})
	}
	return out, nil
}

func (s *BacktestService) ListIntrospections(ctx context.Context, req *v1.ListIntrospectionsRequest) (*v1.ListIntrospectionsReply, error) {
	rows, err := s.uc.Introspections(ctx, req.GetBook(), int(req.GetLimit()))
	if err != nil {
		return nil, mapErr(err)
	}
	out := &v1.ListIntrospectionsReply{}
	for _, row := range rows {
		out.Items = append(out.Items, toIntro(row))
	}
	return out, nil
}

func (s *BacktestService) RunIntrospection(ctx context.Context, req *v1.RunIntrospectionRequest) (*v1.Introspection, error) {
	d, err := day(req.GetDate())
	if err != nil {
		return nil, kerrors.BadRequest("BAD_REQUEST", err.Error())
	}
	if d.IsZero() {
		if d, err = s.uc.LastClosed(); err != nil {
			return nil, mapErr(err)
		}
	}
	row, err := s.uc.Introspect(ctx, d, req.GetBook())
	if err != nil {
		return nil, mapErr(err)
	}
	return toIntro(row), nil
}

func toIntro(in *biz.Introspection) *v1.Introspection {
	out := &v1.Introspection{
		Id: int64(in.ID), TradeDate: fmtDay(in.TradeDate), Book: in.Book, WindowDays: int32(in.WindowDays),
		SampleCount: int32(in.SampleCount), WinRate: in.WinRate, AvgReturn: in.AvgReturn, MaxDrawdown: in.MaxDrawdown,
		MissedCount: int32(in.MissedCount), Attribution: map[string]int32{}, Triggered: in.Triggered,
		TriggerReason: in.TriggerReason, Note: in.Note, CreatedAt: fmtTime(in.CreatedAt),
	}
	for k, v := range in.Attribution {
		out.Attribution[k] = int32(v)
	}
	if in.TaskID != nil {
		out.TaskId = int64(*in.TaskID)
	}
	return out
}

func (s *BacktestService) ListProposals(ctx context.Context, req *v1.ListProposalsRequest) (*v1.ListProposalsReply, error) {
	rows, err := s.uc.Proposals(ctx, biz.ProposalFilter{Status: req.GetStatus(), Strategy: req.GetStrategy(), Limit: int(req.GetLimit())})
	if err != nil {
		return nil, mapErr(err)
	}
	out := &v1.ListProposalsReply{}
	for _, row := range rows {
		p, err := toProposal(row)
		if err != nil {
			return nil, err
		}
		out.Proposals = append(out.Proposals, p)
	}
	return out, nil
}

func (s *BacktestService) ApproveProposal(ctx context.Context, req *v1.DecideProposalRequest) (*v1.Proposal, error) {
	p, err := s.uc.Approve(ctx, int(req.GetId()), operator(ctx))
	if err != nil {
		return nil, mapErr(err)
	}
	return toProposal(p)
}

func (s *BacktestService) RejectProposal(ctx context.Context, req *v1.DecideProposalRequest) (*v1.Proposal, error) {
	p, err := s.uc.Reject(ctx, int(req.GetId()), operator(ctx), req.GetNote())
	if err != nil {
		return nil, mapErr(err)
	}
	return toProposal(p)
}

func (s *BacktestService) RollbackStrategy(ctx context.Context, req *v1.RollbackStrategyRequest) (*v1.Proposal, error) {
	p, err := s.uc.Rollback(ctx, req.GetName(), operator(ctx))
	if err != nil {
		return nil, mapErr(err)
	}
	return toProposal(p)
}

func toProposal(p *biz.Proposal) (*v1.Proposal, error) {
	base, err := toStruct(json.RawMessage(p.BaseMetrics))
	if err != nil {
		return nil, err
	}
	next, err := toStruct(json.RawMessage(p.NewMetrics))
	if err != nil {
		return nil, err
	}
	out := &v1.Proposal{
		Id: int64(p.ID), Strategy: p.Strategy, BaseParams: p.BaseParams, Params: p.Params, Reason: p.Reason,
		BaseMetrics: base, NewMetrics: next, Status: p.Status, DecidedBy: p.DecidedBy,
		CreatedAt: fmtTime(p.CreatedAt), DecidedAt: fmtTimePtr(p.DecidedAt),
	}
	if p.TaskID != nil {
		out.TaskId = int64(*p.TaskID)
	}
	if p.VersionID != nil {
		out.VersionId = int64(*p.VersionID)
	}
	if p.PrevVersionID != nil {
		out.PrevVersionId = int64(*p.PrevVersionID)
	}
	return out, nil
}

func (s *BacktestService) PeriodReport(ctx context.Context, req *v1.PeriodReportRequest) (*v1.PeriodReportReply, error) {
	d, err := day(req.GetDate())
	if err != nil {
		return nil, kerrors.BadRequest("BAD_REQUEST", err.Error())
	}
	if d.IsZero() {
		d = backtest.Day(time.Now())
	}
	start, end, rep, err := s.uc.PeriodReport(ctx, req.GetPeriod(), d)
	if err != nil {
		return nil, mapErr(err)
	}
	st, err := toStruct(rep)
	if err != nil {
		return nil, err
	}
	return &v1.PeriodReportReply{Period: req.GetPeriod(), Start: fmtDay(start), End: fmtDay(end), Report: st}, nil
}

func (s *BacktestService) Admission(ctx context.Context, req *v1.AdmissionRequest) (*v1.AdmissionReply, error) {
	items, ready, err := s.uc.Admission(ctx, req.GetStrategy())
	if err != nil {
		return nil, mapErr(err)
	}
	out := &v1.AdmissionReply{Strategy: req.GetStrategy(), Ready: ready}
	for _, it := range items {
		out.Items = append(out.Items, &v1.AdmissionItem{Key: it.Key, Title: it.Title, Status: it.Status, Detail: it.Detail})
	}
	return out, nil
}
