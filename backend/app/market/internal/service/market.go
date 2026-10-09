// Package service 把 Market 接口转成 biz 调用，只做参数校验和结构转换。
package service

import (
	"context"
	"strings"
	"time"

	v1 "server/api/market/v1"
	"server/app/market/internal/biz"
	"server/pkg/market"
	"server/pkg/symbol"
	"server/pkg/tradecal"

	kerrors "github.com/go-kratos/kratos/v2/errors"
	"github.com/google/wire"
)

var ProviderSet = wire.NewSet(NewMarketService)

const (
	maxWatchSymbols = 500
	maxWatchTTL     = 24 * time.Hour
)

type MarketService struct {
	v1.UnimplementedMarketServer
	uc *biz.Usecase
}

func NewMarketService(uc *biz.Usecase) *MarketService { return &MarketService{uc: uc} }

func badRequest(msg string) error { return kerrors.BadRequest("MARKET_INVALID", msg) }

func internal(err error) error { return kerrors.InternalServer("MARKET_INTERNAL", err.Error()) }

// parseTime 接受 RFC3339 或上海时区的 2006-01-02 / 2006-01-02 15:04:05。
func parseTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, tradecal.Shanghai()); err == nil {
			return t, nil
		}
	}
	return time.Time{}, badRequest("invalid time " + s)
}

// parseEnd 把纯日期的结束时间扩到当天最后一刻，使区间包含 end 当天。
func parseEnd(s string) (time.Time, error) {
	t, err := parseTime(s)
	if err != nil || t.IsZero() {
		return t, err
	}
	if len(strings.TrimSpace(s)) == len("2006-01-02") {
		t = t.Add(24*time.Hour - time.Nanosecond)
	}
	return t, nil
}

func parseDay(s string) (time.Time, error) {
	if strings.TrimSpace(s) == "" {
		return market.DateOf(time.Now()), nil
	}
	t, err := parseTime(s)
	if err != nil {
		return t, err
	}
	return market.DateOf(t), nil
}

func parseSymbol(raw string) (string, error) {
	s, err := symbol.Parse(raw)
	if err != nil {
		return "", badRequest(err.Error())
	}
	return s.Tongdaxin(), nil
}

func parseSymbols(raw []string) ([]string, error) {
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		s, err := parseSymbol(r)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

func adjustOf(a v1.Adjust) market.Adjust {
	switch a {
	case v1.Adjust_QFQ:
		return market.AdjustForward
	case v1.Adjust_HFQ:
		return market.AdjustBackward
	default:
		return market.AdjustNone
	}
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

func (s *MarketService) GetBars(ctx context.Context, req *v1.GetBarsRequest) (*v1.GetBarsReply, error) {
	sym, err := parseSymbol(req.GetSymbol())
	if err != nil {
		return nil, err
	}
	freq := req.GetFreq()
	if freq == "" {
		freq = "1d"
	}
	if freq != "1d" && freq != "1m" {
		return nil, badRequest("freq must be 1m or 1d")
	}
	start, err := parseTime(req.GetStart())
	if err != nil {
		return nil, err
	}
	end, err := parseEnd(req.GetEnd())
	if err != nil {
		return nil, err
	}
	rows, err := s.uc.Bars(ctx, sym, freq, start, end, adjustOf(req.GetAdjust()), int(req.GetLimit()))
	if err != nil {
		return nil, internal(err)
	}
	reply := &v1.GetBarsReply{Symbol: sym, Freq: freq, Bars: make([]*v1.Bar, 0, len(rows))}
	for _, r := range rows {
		ts := fmtTime(r.Time)
		if freq == "1d" {
			ts = fmtDay(r.Time)
		}
		reply.Bars = append(reply.Bars, &v1.Bar{
			Time: ts, Open: r.Open, High: r.High, Low: r.Low, Close: r.Close,
			Volume: r.Volume, Amount: r.Amount, AdjFactor: r.Adj,
		})
	}
	return reply, nil
}

func (s *MarketService) GetIndicators(ctx context.Context, req *v1.GetIndicatorsRequest) (*v1.GetIndicatorsReply, error) {
	sym, err := parseSymbol(req.GetSymbol())
	if err != nil {
		return nil, err
	}
	start, err := parseTime(req.GetStart())
	if err != nil {
		return nil, err
	}
	end, err := parseEnd(req.GetEnd())
	if err != nil {
		return nil, err
	}
	pts, err := s.uc.Indicators(ctx, sym, start, end, adjustOf(req.GetAdjust()), req.GetNames())
	if err != nil {
		return nil, internal(err)
	}
	reply := &v1.GetIndicatorsReply{Symbol: sym, Points: make([]*v1.IndicatorPoint, 0, len(pts))}
	for _, p := range pts {
		reply.Points = append(reply.Points, &v1.IndicatorPoint{Time: fmtDay(p.Time), Values: p.Values})
	}
	return reply, nil
}

func (s *MarketService) GetFactorsCross(ctx context.Context, req *v1.GetFactorsCrossRequest) (*v1.GetFactorsCrossReply, error) {
	day, err := parseDay(req.GetTradeDate())
	if err != nil {
		return nil, err
	}
	asOf, err := parseTime(req.GetAsOf())
	if err != nil {
		return nil, err
	}
	switch req.GetKind() {
	case "", biz.KindClose, biz.KindIntraday, biz.KindAuction, biz.KindCapital:
	default:
		return nil, badRequest("kind must be close, intraday, auction or capital")
	}
	syms, err := parseSymbols(req.GetSymbols())
	if err != nil {
		return nil, err
	}
	rows, err := s.uc.FactorsCross(ctx, day, asOf, req.GetKind(), syms, req.GetNames())
	if err != nil {
		return nil, internal(err)
	}
	reply := &v1.GetFactorsCrossReply{TradeDate: fmtDay(day), Rows: make([]*v1.FactorRow, 0, len(rows))}
	for _, r := range rows {
		reply.Rows = append(reply.Rows, &v1.FactorRow{
			Symbol: r.Symbol, AsOf: fmtTime(r.AsOf), Kind: r.Kind, Values: r.Values, Stale: r.Stale,
		})
	}
	return reply, nil
}

func (s *MarketService) GetSentiment(ctx context.Context, req *v1.GetSentimentRequest) (*v1.Sentiment, error) {
	day, err := parseDay(req.GetTradeDate())
	if err != nil {
		return nil, err
	}
	asOf, err := parseTime(req.GetAsOf())
	if err != nil {
		return nil, err
	}
	st, ok, err := s.uc.Sentiment(ctx, day, asOf)
	if err != nil {
		return nil, internal(err)
	}
	if !ok {
		return nil, kerrors.NotFound("MARKET_NOT_FOUND", "no sentiment for "+fmtDay(day))
	}
	return &v1.Sentiment{
		TradeDate: fmtDay(st.TradeDate), AsOf: fmtTime(st.AsOf),
		UpCount: int32(st.UpCount), DownCount: int32(st.DownCount), BrokenCount: int32(st.BrokenCount),
		BrokenRate: st.BrokenRate, MaxHeight: int32(st.MaxHeight), ProfitEffect: st.ProfitEffect,
		Advance: int32(st.Advance), Decline: int32(st.Decline), Flat: int32(st.Flat), Amount: st.Amount,
		Phase: string(st.Phase), ScoreCoef: st.ScoreCoef, PositionScale: st.PositionScale, Stale: st.Stale,
	}, nil
}

func (s *MarketService) GetLimitBoards(ctx context.Context, req *v1.GetLimitBoardsRequest) (*v1.GetLimitBoardsReply, error) {
	day, err := parseDay(req.GetTradeDate())
	if err != nil {
		return nil, err
	}
	dir := strings.ToUpper(req.GetDirection())
	if dir != "" && dir != market.DirUp && dir != market.DirDown {
		return nil, badRequest("direction must be UP or DOWN")
	}
	status := strings.ToUpper(req.GetStatus())
	if status != "" && status != market.StatusSealed && status != market.StatusBroken {
		return nil, badRequest("status must be SEALED or BROKEN")
	}
	boards, err := s.uc.LimitBoards(ctx, day, dir, status)
	if err != nil {
		return nil, internal(err)
	}
	reply := &v1.GetLimitBoardsReply{Boards: make([]*v1.LimitBoard, 0, len(boards))}
	for _, b := range boards {
		reply.Boards = append(reply.Boards, &v1.LimitBoard{
			Symbol: b.Symbol, TradeDate: fmtDay(b.TradeDate), Direction: b.Direction, Status: b.Status,
			LimitPrice: b.LimitPrice, FirstSealAt: fmtTime(b.FirstSealAt), LastSealAt: fmtTime(b.LastSealAt),
			OpenCount: int32(b.OpenCount), SealAmount: b.SealAmount, Consecutive: int32(b.Consecutive),
			AsOf: fmtTime(b.AsOf),
		})
	}
	return reply, nil
}

func (s *MarketService) GetSectorHeat(ctx context.Context, req *v1.GetSectorHeatRequest) (*v1.GetSectorHeatReply, error) {
	day, err := parseDay(req.GetTradeDate())
	if err != nil {
		return nil, err
	}
	asOf, err := parseTime(req.GetAsOf())
	if err != nil {
		return nil, err
	}
	rows, at, err := s.uc.SectorHeat(ctx, day, asOf, int(req.GetTop()))
	if err != nil {
		return nil, internal(err)
	}
	reply := &v1.GetSectorHeatReply{Sectors: make([]*v1.SectorHeat, 0, len(rows))}
	for _, h := range rows {
		reply.Sectors = append(reply.Sectors, &v1.SectorHeat{
			SectorCode: h.SectorCode, Name: s.uc.SectorName(h.SectorCode), Rank: int32(h.Rank), Heat: h.Heat,
			AvgPct: h.AvgPct, UpLimitCount: int32(h.UpLimitCount), AdvanceRatio: h.AdvanceRatio,
			Amount: h.Amount, MemberCount: int32(h.MemberCount), LeaderSymbol: h.LeaderSymbol,
			LeaderReason: h.LeaderReason, OverseasImpulse: h.OverseasImpulse, AsOf: fmtTime(at),
		})
	}
	return reply, nil
}

func (s *MarketService) SetWatchlist(ctx context.Context, req *v1.SetWatchlistRequest) (*v1.SetWatchlistReply, error) {
	owner := strings.TrimSpace(req.GetOwner())
	if owner == "" {
		return nil, badRequest("owner is required")
	}
	if len(req.GetSymbols()) > maxWatchSymbols {
		return nil, badRequest("too many symbols")
	}
	syms, err := parseSymbols(req.GetSymbols())
	if err != nil {
		return nil, err
	}
	ttl := time.Duration(req.GetTtlSeconds()) * time.Second
	if ttl < 0 || ttl > maxWatchTTL {
		return nil, badRequest("ttl_seconds out of range")
	}
	exp := s.uc.SetWatchlist(owner, syms, ttl)
	return &v1.SetWatchlistReply{Count: int32(len(syms)), ExpiresAt: fmtTime(exp)}, nil
}
