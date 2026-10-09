package service

import (
	"context"
	"errors"

	v1 "server/api/trade/v1"
	"server/app/trade/internal/biz"

	kerrors "github.com/go-kratos/kratos/v2/errors"
	"github.com/google/wire"
)

var ProviderSet = wire.NewSet(NewTradeService)

type TradeService struct {
	v1.UnimplementedTradeServer
	e *biz.Engine
}

func NewTradeService(e *biz.Engine) *TradeService { return &TradeService{e: e} }

func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, biz.ErrInvalid) {
		return kerrors.BadRequest("TRADE_INVALID", err.Error())
	}
	if errors.Is(err, biz.ErrNotFound) {
		return kerrors.NotFound("TRADE_NOT_FOUND", err.Error())
	}
	return err
}

func (s *TradeService) PlaceOrder(ctx context.Context, req *v1.PlaceOrderRequest) (*v1.Order, error) {
	in := biz.PlaceRequest{
		ClientOrderID: req.GetClientOrderId(), Account: req.GetAccount(), Symbol: req.GetSymbol(),
		Side: req.GetSide(), Price: req.GetPrice(), Volume: int(req.GetVolume()),
		Source: req.GetSource(), Operator: req.GetOperator(),
		SignalID: req.GetSignalId(), StrategyVersion: req.GetStrategyVersion(),
	}
	if b := req.GetBar(); b != nil && (b.GetOpen() > 0 || b.GetVolume() > 0) {
		in.Quote = quoteOf(b)
	}
	o, err := s.e.Place(ctx, in)
	if err != nil {
		return nil, mapErr(err)
	}
	return toOrder(o), nil
}

func (s *TradeService) CancelOrder(ctx context.Context, req *v1.CancelOrderRequest) (*v1.Order, error) {
	o, err := s.e.Cancel(ctx, req.GetClientOrderId())
	if err != nil {
		return nil, mapErr(err)
	}
	return toOrder(o), nil
}

func (s *TradeService) GetOrder(ctx context.Context, req *v1.GetOrderRequest) (*v1.Order, error) {
	o, err := s.e.Get(ctx, req.GetClientOrderId())
	if err != nil {
		return nil, mapErr(err)
	}
	return toOrder(o), nil
}

func (s *TradeService) ListPositions(ctx context.Context, req *v1.ListPositionsRequest) (*v1.ListPositionsReply, error) {
	rows, err := s.e.Positions(ctx, req.GetAccount())
	if err != nil {
		return nil, mapErr(err)
	}
	out := &v1.ListPositionsReply{}
	for _, p := range rows {
		out.Positions = append(out.Positions, &v1.Position{
			Symbol: p.Symbol, Quantity: int32(p.Quantity), Available: int32(p.Available),
			AvgCost: p.AvgCost, LastPrice: p.LastPrice,
		})
	}
	return out, nil
}

func (s *TradeService) GetAccount(ctx context.Context, req *v1.GetAccountRequest) (*v1.Account, error) {
	view, err := s.e.Account(ctx, req.GetAccount())
	if err != nil {
		return nil, mapErr(err)
	}
	return toAccount(view), nil
}

func (s *TradeService) ApplyBar(ctx context.Context, req *v1.ApplyBarRequest) (*v1.ApplyBarReply, error) {
	q := biz.Quote{}
	if req.GetBar() != nil {
		q = *quoteOf(req.GetBar())
	}
	orders, err := s.e.ApplyBar(ctx, req.GetAccount(), req.GetSymbol(), q)
	if err != nil {
		return nil, mapErr(err)
	}
	out := &v1.ApplyBarReply{}
	for _, o := range orders {
		out.Orders = append(out.Orders, toOrder(o))
	}
	return out, nil
}

func (s *TradeService) RollDay(ctx context.Context, req *v1.RollDayRequest) (*v1.Account, error) {
	view, err := s.e.RollDay(ctx, req.GetAccount())
	if err != nil {
		return nil, mapErr(err)
	}
	return toAccount(view), nil
}

func (s *TradeService) Orders(ctx context.Context, account string) ([]biz.Order, error) {
	return s.e.Orders(ctx, account)
}

func (s *TradeService) Fills(ctx context.Context, account string) ([]biz.Fill, error) {
	return s.e.Fills(ctx, account)
}

func (s *TradeService) Confirm(ctx context.Context, clientID string) (biz.Order, error) {
	return s.e.Confirm(ctx, clientID)
}

func (s *TradeService) Reconcile(ctx context.Context, req *v1.ReconcileRequest) (*v1.ReconcileReply, error) {
	var remote []biz.RemotePosition
	for _, p := range req.GetPositions() {
		remote = append(remote, biz.RemotePosition{Symbol: p.GetSymbol(), Quantity: int(p.GetQuantity()), Available: int(p.GetAvailable())})
	}
	ok, diffs, err := s.e.Reconcile(ctx, req.GetAccount(), remote)
	if err != nil {
		return nil, mapErr(err)
	}
	return &v1.ReconcileReply{Matched: ok, OpenHalted: !ok, Diffs: diffs}, nil
}

func quoteOf(b *v1.Bar) *biz.Quote {
	if b == nil {
		return &biz.Quote{}
	}
	return &biz.Quote{
		Open: b.GetOpen(), High: b.GetHigh(), Low: b.GetLow(), Close: b.GetClose(),
		Volume: b.GetVolume(), LimitUp: b.GetLimitUp(), LimitDown: b.GetLimitDown(),
	}
}

func toOrder(o biz.Order) *v1.Order {
	account := "SIM"
	if o.Book == biz.BookLive {
		account = "LIVE"
	}
	return &v1.Order{
		ClientOrderId: o.ClientOrderID, Account: account, Symbol: o.Symbol, Side: o.Side,
		Status: o.Status, Price: o.Price, Volume: int32(o.Volume), Filled: int32(o.Filled),
		Reason: o.Reason, SignalId: o.SignalID, StrategyVersion: o.StrategyVersion,
	}
}

func toAccount(v biz.AccountView) *v1.Account {
	account := "SIM"
	if v.Book == biz.BookLive {
		account = "LIVE"
	}
	return &v1.Account{
		Account: account, Cash: v.Cash, Equity: v.Equity, RealizedPnl: v.RealizedPnL,
		UnrealizedPnl: v.UnrealizedPnL, PositionCount: int32(v.PositionCount),
		PaperDays: int32(v.PaperDays), OpenHalted: v.OpenHalted, HaltReason: v.HaltReason,
	}
}
