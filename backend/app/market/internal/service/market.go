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

// parseSymbol 是用户请求边界的归一（P0-1 裸码修复）：助记符直达（GP/QB 600519）
// 与手输都可能带裸 6 位代码，这里按 ParseAShare 放宽 A 股代码段；指数/基金段
// 不归此函数管，认不出仍 400，ParseAShare 是唯一判错闸门。
func parseSymbol(raw string) (string, error) {
	s, err := symbol.ParseAShare(raw)
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

// GetBarsBatch 一次拉多码日线（M13 W2 留账：strategy/datahub 读放大收敛的读端）。
// 缺码在 results 中缺席（缺席即缺数）；任一码解析失败整批 400，不做部分静默放行。
func (s *MarketService) GetBarsBatch(ctx context.Context, req *v1.GetBarsBatchRequest) (*v1.GetBarsBatchReply, error) {
	syms, err := parseSymbols(req.GetSymbols())
	if err != nil {
		return nil, err
	}
	if len(syms) == 0 {
		return nil, badRequest("symbols required")
	}
	start, err := parseTime(req.GetStart())
	if err != nil {
		return nil, err
	}
	end, err := parseEnd(req.GetEnd())
	if err != nil {
		return nil, err
	}
	res, err := s.uc.BarsBatch(ctx, syms, start, end, adjustOf(req.GetAdjust()), int(req.GetLimit()))
	if err != nil {
		return nil, internal(err)
	}
	reply := &v1.GetBarsBatchReply{Freq: "1d", Results: make([]*v1.SymbolBars, 0, len(res))}
	for _, sym := range syms { // 结果按请求序稳定输出
		rows, ok := res[sym]
		if !ok || len(rows) == 0 {
			continue // 缺席即缺数
		}
		sb := &v1.SymbolBars{Symbol: sym, Bars: make([]*v1.Bar, 0, len(rows))}
		for _, r := range rows {
			sb.Bars = append(sb.Bars, &v1.Bar{
				Time: fmtDay(r.Time), Open: r.Open, High: r.High, Low: r.Low, Close: r.Close,
				Volume: r.Volume, Amount: r.Amount, AdjFactor: r.Adj,
			})
		}
		reply.Results = append(reply.Results, sb)
	}
	return reply, nil
}

func (s *MarketService) GetQuote(ctx context.Context, req *v1.GetQuoteRequest) (*v1.Quote, error) {
	sym, err := parseSymbol(req.GetSymbol())
	if err != nil {
		return nil, err
	}
	q, err := s.uc.Quote(ctx, sym)
	if err != nil {
		return nil, internal(err)
	}
	out := &v1.Quote{
		Symbol: q.Symbol, Name: q.Name, Market: q.Market, TradeStatus: q.Status,
		Last: q.Last, Chg: q.Chg, PctChg: q.PctChg,
		Open: q.Open, High: q.High, Low: q.Low, PrevClose: q.PreClose,
		Volume: q.Volume, Amount: q.Amount,
		TurnoverRate: q.TurnoverRate, Amplitude: q.Amplitude,
		PeTtm: q.PeTtm, Pb: q.Pb, CircMv: q.CircMv, TotalMv: q.TotalMv,
		LimitUpPrice: q.LimitUp, LimitDownPrice: q.LimitDown, NoLimit: q.NoLimit,
		High_52W: q.High52, Low_52W: q.Low52, // proto high_52w/low_52w 的 Go 生成名（*float64 缺省直传）
		Source: q.Source, AsOf: fmtTime(q.AsOf), Stale: q.Stale, Found: q.Found,
	}
	for _, l := range q.Bid {
		out.Bid = append(out.Bid, &v1.QuoteLevel{Price: l[0], Volume: int64(l[1])})
	}
	for _, l := range q.Ask {
		out.Ask = append(out.Ask, &v1.QuoteLevel{Price: l[0], Volume: int64(l[1])})
	}
	return out, nil
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
	st, riskLevel, ok, err := s.uc.Sentiment(ctx, day, asOf)
	if err != nil {
		return nil, internal(err)
	}
	if !ok {
		return nil, kerrors.NotFound("MARKET_NOT_FOUND", "no sentiment for "+fmtDay(day))
	}
	reply := &v1.Sentiment{
		TradeDate: fmtDay(st.TradeDate), AsOf: fmtTime(st.AsOf),
		UpCount: int32(st.UpCount), DownCount: int32(st.DownCount), BrokenCount: int32(st.BrokenCount),
		BrokenRate: st.BrokenRate, MaxHeight: int32(st.MaxHeight), ProfitEffect: st.ProfitEffect,
		Advance: int32(st.Advance), Decline: int32(st.Decline), Flat: int32(st.Flat), Amount: st.Amount,
		Phase: string(st.Phase), ScoreCoef: st.ScoreCoef, PositionScale: st.PositionScale, Stale: st.Stale,
	}
	if riskLevel != nil {
		v := int32(*riskLevel)
		reply.RiskLevel = &v
	}
	return reply, nil
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

// GetWatchlist 读取候选池租约（只读）。owner 为空返回全部未到期 owner 租约——登录即可读：本系统为单机私有部署，各桌面互见彼此与 trade.* 进程的池以便排查推送中断（WS 不广播租约，这是本读口独有的有意暴露面）。
func (s *MarketService) GetWatchlist(ctx context.Context, req *v1.GetWatchlistRequest) (*v1.GetWatchlistReply, error) {
	owner := strings.TrimSpace(req.GetOwner())
	leases := s.uc.GetWatchlist(owner)
	reply := &v1.GetWatchlistReply{Leases: make([]*v1.WatchLease, 0, len(leases))}
	for _, l := range leases {
		reply.Leases = append(reply.Leases, &v1.WatchLease{
			Owner:     l.Owner,
			Symbols:   l.Symbols,
			ExpiresAt: fmtTime(l.Expires),
		})
	}
	return reply, nil
}

// GetAuction 返回集合竞价截面汇总（监控驾驶舱 P1，只读）。
func (s *MarketService) GetAuction(ctx context.Context, req *v1.GetAuctionRequest) (*v1.GetAuctionReply, error) {
	day, err := parseDay(req.GetTradeDate())
	if err != nil {
		return nil, err
	}
	summary, err := s.uc.AuctionCross(ctx, day, int(req.GetTop()))
	if err != nil {
		return nil, internal(err)
	}
	reply := &v1.GetAuctionReply{
		TradeDate: fmtDay(summary.Day), Found: summary.Found, TotalAmount: summary.TotalAmount,
		LimitUpCount: int32(summary.LimitUpCount), AsOf: fmtTime(summary.AsOf),
	}
	toRow := func(r biz.AuctionRow) *v1.AuctionRow {
		return &v1.AuctionRow{
			Symbol: r.Symbol, AuctionPct: r.Pct, AuctionAmount: r.Amount, AuctionVolRatio: r.VolRatio,
			AuctionTurnover: r.Turnover, AuctionLimitUp: r.LimitUp, AuctionUnmatched: r.Unmatched,
		}
	}
	for _, r := range summary.LimitUps {
		reply.LimitUps = append(reply.LimitUps, toRow(r))
	}
	for _, r := range summary.TopRush {
		reply.TopRush = append(reply.TopRush, toRow(r))
	}
	return reply, nil
}

// GetLadder 返回连板梯队晋级率（监控驾驶舱 P1，只读）。to 为空时取最近已收盘交易日。
func (s *MarketService) GetLadder(ctx context.Context, req *v1.GetLadderRequest) (*v1.GetLadderReply, error) {
	var to time.Time
	if raw := strings.TrimSpace(req.GetTo()); raw != "" {
		var err error
		to, err = parseDay(raw)
		if err != nil {
			return nil, err
		}
	} else {
		// 默认最近一个已收盘交易日：今天 15:30 前看昨天
		last, err := tradecal.Default.LastClosedDay(time.Now())
		if err != nil {
			return nil, internal(err)
		}
		to = last
	}
	days, totals, err := s.uc.Ladder(ctx, to, int(req.GetDays()))
	if err != nil {
		return nil, internal(err)
	}
	toStep := func(st biz.LadderStep) *v1.LadderStep {
		return &v1.LadderStep{FromHeight: int32(st.From), Sample: int32(st.Sample), Succeeded: int32(st.Succeeded), Rate: st.Rate}
	}
	reply := &v1.GetLadderReply{}
	for _, d := range days {
		item := &v1.LadderDay{TradeDate: fmtDay(d.Day)}
		for _, st := range d.Steps {
			item.Steps = append(item.Steps, toStep(st))
		}
		reply.Days = append(reply.Days, item)
	}
	for _, st := range totals {
		reply.Total = append(reply.Total, toStep(st))
	}
	return reply, nil
}

// resolveP2Day 处理 P2 接口的默认交易日：空 = 最近已收盘交易日。
func resolveP2Day(raw string) (time.Time, error) {
	if strings.TrimSpace(raw) != "" {
		return parseDay(raw)
	}
	return tradecal.Default.LastClosedDay(time.Now())
}

// GetLimitPremium 返回昨日涨停今日溢价（监控驾驶舱 P2，只读）。
func (s *MarketService) GetLimitPremium(ctx context.Context, req *v1.GetLimitPremiumRequest) (*v1.GetLimitPremiumReply, error) {
	day, err := resolveP2Day(req.GetTradeDate())
	if err != nil {
		return nil, err
	}
	report, err := s.uc.PremiumSnapshot(ctx, day, int(req.GetDays()), int(req.GetTop()))
	if err != nil {
		return nil, internal(err)
	}
	toDay := func(p biz.PremiumDay) *v1.LimitPremiumDay {
		return &v1.LimitPremiumDay{
			TradeDate: fmtDay(p.TradeDate), BaseDate: fmtDay(p.BaseDate),
			Sample: int32(p.Sample), LimitAgain: int32(p.LimitAgain),
			OpenAvg: p.OpenAvg, CloseAvg: p.CloseAvg, CloseWin: p.CloseWin, HighAvg: p.HighAvg,
			HasClose: p.HasClose,
		}
	}
	reply := &v1.GetLimitPremiumReply{
		TradeDate: fmtDay(report.Day), Found: report.Found, Live: report.Live,
		AsOf: fmtTime(report.AsOf),
	}
	if report.Found {
		reply.Summary = toDay(report.Summary)
	}
	for _, p := range report.Days {
		reply.Days = append(reply.Days, toDay(p))
	}
	for _, r := range report.Rows {
		reply.Rows = append(reply.Rows, &v1.LimitPremiumRow{
			Symbol: r.Symbol, Name: r.Name,
			TradeDate: fmtDay(r.TradeDate), BaseDate: fmtDay(r.BaseDate),
			Consecutive: int32(r.Consecutive), BaseClose: r.BaseClose,
			Open: r.Open, Close: r.Close,
			OpenPremium: r.OpenPremium, ClosePremium: r.ClosePremium, HighPremium: r.HighPremium,
			LimitAgain: r.LimitAgain, HasClose: r.HasClose, Source: r.Source,
		})
	}
	return reply, nil
}

// GetSeatStats 返回龙虎榜席位胜率时序（目录册增补 B2，只读）。
// trade_date 空时按最近已收盘交易日；found=false 表示该日 seat_stats 无行（龙虎榜空或任务未跑）。
func (s *MarketService) GetSeatStats(ctx context.Context, req *v1.GetSeatStatsRequest) (*v1.GetSeatStatsReply, error) {
	day, err := resolveP2Day(req.GetTradeDate())
	if err != nil {
		return nil, err
	}
	report, err := s.uc.SeatStatsSnapshot(ctx, day, int(req.GetTop()))
	if err != nil {
		return nil, internal(err)
	}
	reply := &v1.GetSeatStatsReply{
		TradeDate: fmtDay(report.Day), Found: report.Found, AsOf: fmtTime(report.AsOf),
	}
	for _, r := range report.Rows {
		reply.Rows = append(reply.Rows, &v1.SeatStatRow{
			Seat: r.Seat, AsOf: fmtDay(r.AsOf),
			Lookback: int32(r.Lookback), Sample: int32(r.Sample), SampleT5: int32(r.SampleT5),
			WinRateT1: r.WinRateT1, AvgRetT1: r.AvgRetT1, MedRetT1: r.MedRetT1,
			WinRateT5: r.WinRateT5, AvgRetT5: r.AvgRetT5, MedRetT5: r.MedRetT5,
			Source: r.Source,
		})
	}
	return reply, nil
}

// GetChipDistribution 返回筹码分布（目录册增补 B3，只读）。
// as_of 空时按最近已收盘交易日；status 非 ok 时衍生列缺失（proto optional，「没取到≠0」），
// window 事实列在无行/校验失败时保持空串而非伪造日期。
func (s *MarketService) GetChipDistribution(ctx context.Context, req *v1.GetChipDistributionRequest) (*v1.GetChipDistributionReply, error) {
	sym, err := parseSymbol(req.GetSymbol())
	if err != nil {
		return nil, err
	}
	grid := int(req.GetGridSize())
	if grid != 0 && (grid < 50 || grid > 2000) {
		return nil, badRequest("grid_size out of range [50,2000]")
	}
	day, err := resolveP2Day(req.GetAsOf())
	if err != nil {
		return nil, err
	}
	rep, err := s.uc.ChipDistribution(ctx, sym, day, int(req.GetDays()), grid)
	if err != nil {
		return nil, internal(err)
	}
	reply := &v1.GetChipDistributionReply{
		Symbol: rep.Symbol, Status: rep.Status, Reason: rep.Reason,
		Profit: rep.Profit, AvgCost: rep.AvgCost,
		Cost90Lo: rep.Cost90Lo, Cost90Hi: rep.Cost90Hi,
		Cost70Lo: rep.Cost70Lo, Cost70Hi: rep.Cost70Hi,
		Concentration90: rep.Concentration90, Concentration70: rep.Concentration70,
		PeakPrice: rep.PeakPrice, Price: rep.Price,
		DaysUsed: int32(rep.DaysUsed), CumTurnoverPct: rep.CumTurnoverPct,
	}
	if !rep.WindowStart.IsZero() {
		reply.WindowStart = fmtDay(rep.WindowStart)
		reply.WindowEnd = fmtDay(rep.WindowEnd)
	}
	return reply, nil
}

// GetReportEstimates 返回券商研报盈利预测（目录册增补 B4，只读）。
// as_of 空时按最近已收盘交易日；found=false 表示发布日窗口内无披露行——显式缺失不编数。
// 披露列 Nillable 指针直传 proto optional，缺披露省略发送不产假数。
func (s *MarketService) GetReportEstimates(ctx context.Context, req *v1.GetReportEstimatesRequest) (*v1.GetReportEstimatesReply, error) {
	sym, err := parseSymbol(req.GetSymbol())
	if err != nil {
		return nil, err
	}
	day, err := resolveP2Day(req.GetAsOf())
	if err != nil {
		return nil, err
	}
	rep, err := s.uc.ReportEstimates(ctx, sym, day, int(req.GetDays()))
	if err != nil {
		return nil, internal(err)
	}
	reply := &v1.GetReportEstimatesReply{
		Symbol: rep.Symbol, Found: rep.Found, Days: int32(rep.Days),
		WindowStart: fmtDay(rep.WindowStart), AsOf: fmtDay(rep.AsOf),
	}
	for _, r := range rep.Rows {
		row := &v1.ReportEstimateRow{
			PublishDate: fmtDay(r.PublishDate), OrgSname: r.OrgSname, OrgCode: r.OrgCode,
			EstYear: int32(r.EstYear),
			Eps:     r.Eps, Pe: r.Pe, AimPriceLow: r.AimPriceLow, AimPriceHigh: r.AimPriceHigh,
			Rating: r.Rating, LastRating: r.LastRating,
			Researcher: r.Researcher, Industry: r.Industry,
		}
		if r.RatingChange != nil {
			v := int32(*r.RatingChange)
			row.RatingChange = &v
		}
		if r.ReportType != nil {
			v := int32(*r.ReportType)
			row.ReportType = &v
		}
		reply.Rows = append(reply.Rows, row)
	}
	return reply, nil
}

// GetFinanceBrief 返回财务排雷摘要（附录 B-3，只读 finance_report）。
// found=false = 该券无披露行——合法业务态，前端显「无」不告警；披露列指针直传，
// DB NULL ↔ proto 缺省，不落 0。
func (s *MarketService) GetFinanceBrief(ctx context.Context, req *v1.GetFinanceBriefRequest) (*v1.GetFinanceBriefReply, error) {
	sym, err := parseSymbol(req.GetSymbol())
	if err != nil {
		return nil, err
	}
	rows, err := s.uc.FinanceBrief(ctx, sym, int(req.GetPeriods()))
	if err != nil {
		return nil, internal(err)
	}
	reply := &v1.GetFinanceBriefReply{Symbol: sym, Found: len(rows) > 0, Rows: make([]*v1.FinanceBriefRow, 0, len(rows))}
	for _, r := range rows {
		reply.Rows = append(reply.Rows, &v1.FinanceBriefRow{
			EndDate: fmtDay(r.EndDate), Kind: r.Kind, AnnDate: fmtDay(r.AnnDate),
			ForecastType: r.ForecastType, NetProfit: r.NetProfit, NetProfitYoy: r.NetProfitYoY,
			Summary: r.Summary, Source: r.Source,
		})
	}
	return reply, nil
}

// GetAHPremium 返回 A/H 配对与溢价（目录册增补 B5，查询时直抓，不落库）。
// status 五态归属与衍生列缺省在 biz 收口；这里只做指针直传与时刻格式化。
func (s *MarketService) GetAHPremium(ctx context.Context, req *v1.GetAHPremiumRequest) (*v1.GetAHPremiumReply, error) {
	sym, err := parseSymbol(req.GetSymbol())
	if err != nil {
		return nil, err
	}
	rep, err := s.uc.AHPremium(ctx, sym)
	if err != nil {
		return nil, internal(err)
	}
	reply := &v1.GetAHPremiumReply{
		Status:  rep.Status,
		Reason:  rep.Reason,
		ACode:   rep.Pairing.ACode,
		AMarket: rep.Pairing.AMarket,
		AName:   rep.Pairing.AName,
		HCode:   rep.Pairing.HCode,
		HName:   rep.Pairing.HName,
		ATraded: rep.ATraded,
		ATime:   fmtTime(rep.A.Time),
		HTime:   fmtTime(rep.H.Time),
		FxTime:  fmtTime(rep.FX.Time),
		// 常量标签恒有：源族留痕给面板/钉样（IV 格式行 40）。
		PairSource:  "eastmoney_f10",
		QuoteSource: "tencent",
		AsOf:        fmtTime(rep.AsOf),
	}
	if rep.A.Price != nil {
		reply.APrice = rep.A.Price
	}
	if rep.H.Price != nil {
		reply.HPrice = rep.H.Price
	}
	if rep.FX.Price != nil {
		reply.Hkdcny = rep.FX.Price
	}
	if rep.Ratio != nil {
		reply.AhRatio = rep.Ratio
	}
	if rep.Premium != nil {
		reply.PremiumPct = rep.Premium
	}
	for _, m := range rep.Missing {
		reply.Missing = append(reply.Missing, &v1.AHMissingLeg{Leg: m.Leg, Why: m.Why})
	}
	return reply, nil
}

func (s *MarketService) GetBreadth(ctx context.Context, req *v1.GetBreadthRequest) (*v1.GetBreadthReply, error) {
	day, err := resolveP2Day(req.GetTradeDate())
	if err != nil {
		return nil, err
	}
	report, err := s.uc.BreadthSnapshot(ctx, day, int(req.GetDays()))
	if err != nil {
		return nil, internal(err)
	}
	toDay := func(d biz.BreadthDay) *v1.BreadthDay {
		return &v1.BreadthDay{
			TradeDate: fmtDay(d.Day), Window: int32(d.Window),
			Above: int32(d.Above), Below: int32(d.Below), Flat: int32(d.Flat), Total: int32(d.Total),
			Ratio: d.Ratio, Live: d.Live, AsOf: fmtTime(d.AsOf),
		}
	}
	reply := &v1.GetBreadthReply{TradeDate: fmtDay(report.Day), Found: report.Found}
	if report.Found {
		reply.Latest = toDay(report.Latest)
	}
	for _, d := range report.Days {
		reply.Days = append(reply.Days, toDay(d))
	}
	return reply, nil
}

// GetKlineGaps 返回 K 线连续性检查（监控驾驶舱 P2，只读）。to 为空时取最近已收盘交易日。
func (s *MarketService) GetKlineGaps(ctx context.Context, req *v1.GetKlineGapsRequest) (*v1.GetKlineGapsReply, error) {
	day, err := resolveP2Day(req.GetTradeDate())
	if err != nil {
		return nil, err
	}
	report, err := s.uc.KlineGaps(ctx, day, int(req.GetDays()))
	if err != nil {
		return nil, internal(err)
	}
	reply := &v1.GetKlineGapsReply{
		TradeDate: fmtDay(report.Day), Found: report.Found,
		Universe: int32(report.Universe), MissingTotal: int32(report.MissingTotal),
		AsOf: fmtTime(report.AsOf),
	}
	for _, d := range report.Days {
		reply.Days = append(reply.Days, &v1.KlineGapDay{
			TradeDate: fmtDay(d.Day), Universe: int32(d.Universe),
			Missing: int32(d.Missing), Present: int32(d.Present),
			Sample: d.Sample, AsOf: fmtTime(d.AsOf),
		})
	}
	return reply, nil
}

// GetEmotionPath 返回盘中情绪路径（复盘体系 P0-3，只读）。没存的时点不出现在 points。
func (s *MarketService) GetEmotionPath(ctx context.Context, req *v1.GetEmotionPathRequest) (*v1.GetEmotionPathReply, error) {
	day, err := parseDay(req.GetDate())
	if err != nil {
		return nil, err
	}
	p, err := s.uc.EmotionPath(ctx, day)
	if err != nil {
		return nil, internal(err)
	}
	reply := &v1.GetEmotionPathReply{
		Date: fmtDay(p.Day), AuctionFound: p.AuctionFound,
		AuctionTotalAmount: p.AuctionTotalAmount, AuctionLimitUpCount: int32(p.AuctionLimitUpCount),
		PrevTierAuctionSample: int32(p.PrevTierSample),
		Partial:               p.Partial, Missing: p.Missing,
	}
	if p.PrevTierAuctionPct != nil {
		reply.PrevTierAuctionPct = p.PrevTierAuctionPct
	}
	for _, pt := range p.Points {
		reply.Points = append(reply.Points, &v1.EmotionPathPoint{
			Time: pt.Time, Auction: pt.Auction,
			Up: int32(pt.Up), Down: int32(pt.Down), Broken: int32(pt.Broken),
			BrokenRate: pt.BrokenRate, MaxHeight: int32(pt.MaxHeight), ProfitEffect: pt.ProfitEffect,
			Advance: int32(pt.Advance), Decline: int32(pt.Decline), Amount: pt.Amount,
			Phase: pt.Phase, ScoreCoef: pt.ScoreCoef, Stale: pt.Stale,
		})
	}
	return reply, nil
}

// GetSecurityBasics 返回名称字典（UI 显示方案 §6.2 / 附录 B-1，只读）。
// codes 为空返回全量；非空时每个代码必须可解析（600519.SH / sh600519 / SHSE.600519），规整为 600519.SH，
// 去重交给 biz。缺字段按 proto 零值透传，前端判 hasName 显「名称未返回」，后端不伪造。
func (s *MarketService) GetSecurityBasics(ctx context.Context, req *v1.GetSecurityBasicsRequest) (*v1.GetSecurityBasicsReply, error) {
	codes, err := parseSymbols(req.GetCodes())
	if err != nil {
		return nil, err
	}
	rows, maxUpdate, err := s.uc.SecurityBasics(ctx, codes)
	if err != nil {
		return nil, internal(err)
	}
	reply := &v1.GetSecurityBasicsReply{
		List:          make([]*v1.SecurityBasic, 0, len(rows)),
		MaxUpdateTime: fmtTime(maxUpdate),
	}
	for _, r := range rows {
		item := &v1.SecurityBasic{
			Code: r.Code, Name: r.Name, Market: r.Market, Industry: r.Industry,
			StFlag: r.ST, SuspendFlag: r.Suspended,
			PeTtm: r.PeTtm, Pb: r.Pb, CircMv: r.CircMv,
			UpdateTime: fmtTime(r.UpdateTime), Concepts: r.Concepts,
		}
		// total_mv 未采集=0 → optional 不置位：「没取到≠0」，前端拿 undefined 显「—」，禁止以 0 充数。
		if r.TotalMv > 0 {
			item.TotalMv = &r.TotalMv
		}
		reply.List = append(reply.List, item)
	}
	return reply, nil
}

// SearchSecurities 代码/名称/拼音联想（G2 后端先落，四通道归组见 biz）。
// query 为空返回空 matches，不报错——前端联想框清空即停。
func (s *MarketService) SearchSecurities(ctx context.Context, req *v1.SearchSecuritiesRequest) (*v1.SearchSecuritiesReply, error) {
	ms, err := s.uc.SearchSecurities(ctx, req.GetQuery(), int(req.GetLimit()))
	if err != nil {
		return nil, internal(err)
	}
	reply := &v1.SearchSecuritiesReply{Matches: make([]*v1.SecurityMatch, 0, len(ms))}
	for _, m := range ms {
		reply.Matches = append(reply.Matches, &v1.SecurityMatch{
			Code: m.Code, Name: m.Name, Market: m.Market, St: m.ST, Suspended: m.Suspended,
			MatchKind: m.MatchKind, Pinyin: m.Pinyin,
		})
	}
	return reply, nil
}

func (s *MarketService) GetIntradayAlerts(ctx context.Context, req *v1.GetIntradayAlertsRequest) (*v1.GetIntradayAlertsReply, error) {
	day, err := parseDay(req.GetDate())
	if err != nil {
		return nil, err
	}
	var symbols []string
	for _, sym := range strings.Split(req.GetSymbols(), ",") {
		if sym = strings.TrimSpace(sym); sym != "" {
			symbols = append(symbols, sym)
		}
	}
	rows, err := s.uc.IntradayAlerts(ctx, day, req.GetMinPct(), symbols, req.GetWatchOnly(), time.Now())
	if err != nil {
		return nil, internal(err)
	}
	reply := &v1.GetIntradayAlertsReply{Date: fmtDay(day)}
	for _, r := range rows {
		row := &v1.IntradayAlertRow{Symbol: r.Symbol, Name: r.Name, Kind: r.Kind, Time: r.Time.Format(time.RFC3339)}
		if r.Pct != nil {
			row.Pct = r.Pct
		}
		if r.Price != nil {
			row.Price = r.Price
		}
		reply.Alerts = append(reply.Alerts, row)
	}
	return reply, nil
}
