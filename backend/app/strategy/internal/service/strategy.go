// Package service 是 strategy 的协议适配：校验参数、转调 biz、转换结构。
package service

import (
	"context"
	"errors"
	"time"

	v1 "server/api/strategy/v1"
	"server/app/strategy/internal/biz"
	"server/pkg/tradecal"

	kerrors "github.com/go-kratos/kratos/v2/errors"
	"github.com/google/wire"
)

var ProviderSet = wire.NewSet(NewStrategyService)

type StrategyService struct {
	v1.UnimplementedStrategyServer
	uc *biz.Usecase
}

func NewStrategyService(uc *biz.Usecase) *StrategyService { return &StrategyService{uc: uc} }

func toErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, biz.ErrBadRequest), errors.Is(err, biz.ErrBadTransition):
		return kerrors.BadRequest("BAD_REQUEST", err.Error())
	case errors.Is(err, biz.ErrNotFound):
		return kerrors.NotFound("NOT_FOUND", err.Error())
	case errors.Is(err, biz.ErrConflict):
		return kerrors.Conflict("CONFLICT", err.Error())
	}
	return kerrors.InternalServer("INTERNAL", err.Error())
}

func parseDay(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.ParseInLocation("2006-01-02", s, tradecal.Shanghai())
	if err != nil {
		return time.Time{}, kerrors.BadRequest("BAD_REQUEST", "trade_date 应为 YYYY-MM-DD")
	}
	return t, nil
}

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.In(tradecal.Shanghai()).Format(time.RFC3339)
}

func fmtDay(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.In(tradecal.Shanghai()).Format("2006-01-02")
}

func optInt(p *int) int64 {
	if p == nil {
		return 0
	}
	return int64(*p)
}

func toPB(s *biz.Signal) *v1.Signal {
	out := &v1.Signal{
		Id: int64(s.ID), Book: s.Book, SignalTime: fmtTime(s.Time), TradeDate: fmtDay(s.TradeDate),
		Symbol: s.Symbol, Name: s.Name, Side: s.Side, Strategy: s.Strategy, StrategyVersionId: optInt(s.VersionID),
		Entry: s.Entry, EntryLow: s.EntryLow, EntryHigh: s.EntryHigh, StopLoss: s.StopLoss, TakeProfit: s.TakeProfit,
		Atr: s.ATR, PositionPct: s.PositionPct, ValidUntil: fmtTime(s.ValidUntil), HoldDaysMax: int32(s.HoldDaysMax),
		RuleScore: s.RuleScore, FinalScore: s.FinalScore, Dims: s.Dims, Evidence: s.Evidence,
		AiDegraded: s.AIDegraded, HighValue: s.HighValue, ExitKind: s.ExitKind, Reason: s.Reason,
		Status: string(s.Status), TraceId: s.TraceID, CreatedAt: fmtTime(s.CreatedAt), UpdatedAt: fmtTime(s.UpdatedAt),
		ClosedAt: fmtTime(s.ClosedAt),
	}
	if s.AIScore != nil {
		out.AiScore = *s.AIScore
	}
	if s.ExitPrice != nil {
		out.ExitPrice = *s.ExitPrice
	}
	if s.PnlPct != nil {
		out.PnlPct = *s.PnlPct
		out.HasPnl = true
	}
	if s.HoldingDays != nil {
		out.HoldingDays = int32(*s.HoldingDays)
	}
	return out
}

func (s *StrategyService) ListSignals(ctx context.Context, req *v1.ListSignalsRequest) (*v1.ListSignalsReply, error) {
	day, err := parseDay(req.GetTradeDate())
	if err != nil {
		return nil, err
	}
	list, err := s.uc.ListSignals(ctx, biz.SignalFilter{
		Day: day, Status: biz.Status(req.GetStatus()), Side: req.GetSide(), Limit: int(req.GetLimit()),
	})
	if err != nil {
		return nil, toErr(err)
	}
	reply := &v1.ListSignalsReply{Items: make([]*v1.Signal, len(list))}
	for i, sig := range list {
		reply.Items[i] = toPB(sig)
	}
	return reply, nil
}

func (s *StrategyService) GetSignal(ctx context.Context, req *v1.GetSignalRequest) (*v1.Signal, error) {
	sig, err := s.uc.GetSignal(ctx, int(req.GetId()))
	if err != nil {
		return nil, toErr(err)
	}
	return toPB(sig), nil
}

func (s *StrategyService) TransitionSignal(ctx context.Context, req *v1.TransitionSignalRequest) (*v1.Signal, error) {
	if req.GetId() <= 0 || req.GetTo() == "" || req.GetActor() == "" {
		return nil, kerrors.BadRequest("BAD_REQUEST", "id、to、actor 必填")
	}
	var close *biz.Close
	if req.ExitPrice != nil || req.PnlPct != nil || req.GetClosedAt() != "" {
		close = &biz.Close{ExitPrice: req.GetExitPrice(), PnlPct: req.GetPnlPct()}
		if v := req.GetClosedAt(); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				return nil, kerrors.BadRequest("BAD_REQUEST", "closed_at 应为 RFC3339 时间")
			}
			close.At = t
		}
	}
	sig, err := s.uc.Transition(ctx, int(req.GetId()), biz.Status(req.GetTo()), req.GetReason(), req.GetActor(), close)
	if err != nil {
		return nil, toErr(err)
	}
	return toPB(sig), nil
}

func (s *StrategyService) Scan(ctx context.Context, req *v1.ScanRequest) (*v1.ScanReply, error) {
	pool := req.GetPool()
	switch pool {
	case "":
		pool = biz.PoolIntraday
	case biz.PoolPre, biz.PoolIntraday:
	default:
		return nil, kerrors.BadRequest("BAD_REQUEST", "pool 只能是 pre 或 intraday")
	}
	now := time.Now()
	rep, err := s.uc.Scan(ctx, biz.ScanRequest{Pool: pool, Full: req.GetFull(), Symbols: req.GetSymbols(), AsOf: now})
	if err != nil {
		return nil, toErr(err)
	}
	reply := &v1.ScanReply{
		TraceId: rep.TraceID, Universe: int32(rep.Universe), Matched: int32(rep.Matched), Ranked: int32(rep.Ranked),
		AiCalls: int32(rep.AICalls), Degraded: int32(rep.Degraded), KillSwitch: rep.KillState,
		ElapsedMs: rep.Elapsed.Milliseconds(), Dropped: counts(rep.Dropped), Misses: counts(rep.Misses),
	}
	for _, id := range rep.Signals {
		reply.SignalIds = append(reply.SignalIds, int64(id))
	}
	if req.GetExits() {
		ids, err := s.uc.ScanExits(ctx, now)
		if err != nil {
			return nil, toErr(err)
		}
		for _, id := range ids {
			reply.SellSignalIds = append(reply.SellSignalIds, int64(id))
		}
	}
	return reply, nil
}

func counts(m map[string]int) map[string]int32 {
	out := make(map[string]int32, len(m))
	for k, v := range m {
		out[k] = int32(v)
	}
	return out
}

func optFloat(p *float64) (float64, bool) {
	if p == nil {
		return 0, false
	}
	return *p, true
}

func (s *StrategyService) ListCandidates(ctx context.Context, req *v1.ListCandidatesRequest) (*v1.ListCandidatesReply, error) {
	day, err := parseDay(req.GetTradeDate())
	if err != nil {
		return nil, err
	}
	list, err := s.uc.ListCandidates(ctx, biz.CandidateFilter{
		Day: day, Pool: req.GetPool(), Stage: req.GetStage(), Limit: int(req.GetLimit()),
	})
	if err != nil {
		return nil, toErr(err)
	}
	reply := &v1.ListCandidatesReply{Items: make([]*v1.Candidate, len(list))}
	for i, c := range list {
		item := &v1.Candidate{
			Id: int64(c.ID), TradeDate: fmtDay(c.TradeDate), Strategy: c.Strategy, Symbol: c.Symbol, Name: c.Name,
			Pool: c.Pool, Stage: c.Stage, MissReason: c.MissReason, RuleScore: c.RuleScore,
			SignalId: optInt(c.SignalID), RefPrice: c.RefPrice,
		}
		item.AiScore, _ = optFloat(c.AIScore)
		item.FinalScore, _ = optFloat(c.FinalScore)
		item.RetT1, item.HasT1 = optFloat(c.RetT1)
		item.RetT3, item.HasT3 = optFloat(c.RetT3)
		item.RetT5, item.HasT5 = optFloat(c.RetT5)
		reply.Items[i] = item
	}
	return reply, nil
}

func (s *StrategyService) AddBlacklist(ctx context.Context, req *v1.AddBlacklistRequest) (*v1.BlacklistReply, error) {
	var expires *time.Time
	if v := req.GetExpiresAt(); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return nil, kerrors.BadRequest("BAD_REQUEST", "expires_at 应为 RFC3339 时间")
		}
		expires = &t
	}
	code, err := s.uc.AddBlacklist(ctx, req.GetSymbol(), req.GetReason(), req.GetOperator(), expires)
	if err != nil {
		return nil, toErr(err)
	}
	return &v1.BlacklistReply{Symbol: code}, nil
}

func (s *StrategyService) RemoveBlacklist(ctx context.Context, req *v1.RemoveBlacklistRequest) (*v1.BlacklistReply, error) {
	code, err := s.uc.RemoveBlacklist(ctx, req.GetSymbol())
	if err != nil {
		return nil, toErr(err)
	}
	return &v1.BlacklistReply{Symbol: code}, nil
}
