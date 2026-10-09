package data

import (
	"context"
	"time"

	"server/app/trade/internal/biz"
	"server/ent"
	"server/ent/position"
	"server/ent/tradecash"
	"server/ent/tradefill"
	"server/ent/tradeorder"
	"server/pkg/events"
	"server/pkg/outbox"

	"github.com/go-kratos/kratos/v2/log"
)

type Repo struct {
	client *ent.Client
	log    *log.Helper
}

func NewRepo(client *ent.Client, logger log.Logger) *Repo {
	return &Repo{client: client, log: log.NewHelper(logger)}
}

func (r *Repo) LoadOrder(ctx context.Context, clientID string) (biz.Order, bool, error) {
	row, err := r.client.TradeOrder.Query().Where(tradeorder.ClientOrderIDEQ(clientID)).Only(ctx)
	if ent.IsNotFound(err) {
		return biz.Order{}, false, nil
	}
	if err != nil {
		return biz.Order{}, false, err
	}
	return orderFrom(row), true, nil
}

func (r *Repo) LoadCash(ctx context.Context, book string) (biz.Cash, bool, error) {
	row, err := r.client.TradeCash.Query().Where(tradecash.BookEQ(book)).Only(ctx)
	if ent.IsNotFound(err) {
		return biz.Cash{}, false, nil
	}
	if err != nil {
		return biz.Cash{}, false, err
	}
	return biz.Cash{
		Book: row.Book, Cash: row.Cash, RealizedPnL: row.RealizedPnl,
		PaperDays: row.PaperDays, OpenHalted: row.OpenHalted, HaltReason: row.HaltReason,
	}, true, nil
}

func (r *Repo) LoadPosition(ctx context.Context, book, symbol string) (biz.Position, bool, error) {
	row, err := r.client.Position.Query().Where(position.BookEQ(book), position.SymbolEQ(symbol)).Only(ctx)
	if ent.IsNotFound(err) {
		return biz.Position{}, false, nil
	}
	if err != nil {
		return biz.Position{}, false, err
	}
	return posFrom(row), true, nil
}

func (r *Repo) ListPositions(ctx context.Context, book string) ([]biz.Position, error) {
	rows, err := r.client.Position.Query().Where(position.BookEQ(book)).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]biz.Position, 0, len(rows))
	for _, row := range rows {
		out = append(out, posFrom(row))
	}
	return out, nil
}

func (r *Repo) ListOpen(ctx context.Context, book, symbol string) ([]biz.Order, error) {
	q := r.client.TradeOrder.Query().Where(
		tradeorder.BookEQ(book),
		tradeorder.StatusIn(biz.StatusSubmitted, biz.StatusPartial),
	)
	if symbol != "" {
		q = q.Where(tradeorder.SymbolEQ(symbol))
	}
	rows, err := q.Order(ent.Asc(tradeorder.FieldCreatedAt)).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]biz.Order, 0, len(rows))
	for _, row := range rows {
		out = append(out, orderFrom(row))
	}
	return out, nil
}

func (r *Repo) ListOrders(ctx context.Context, book string, limit int) ([]biz.Order, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.client.TradeOrder.Query().Where(tradeorder.BookEQ(book)).
		Order(ent.Desc(tradeorder.FieldCreatedAt)).Limit(limit).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]biz.Order, 0, len(rows))
	for _, row := range rows {
		out = append(out, orderFrom(row))
	}
	return out, nil
}

func (r *Repo) ListFills(ctx context.Context, book string, limit int) ([]biz.Fill, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.client.TradeFill.Query().Where(tradefill.BookEQ(book)).
		Order(ent.Desc(tradefill.FieldCreatedAt)).Limit(limit).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]biz.Fill, 0, len(rows))
	for _, row := range rows {
		out = append(out, fillFrom(row))
	}
	return out, nil
}

func (r *Repo) ListByStatus(ctx context.Context, status string) ([]biz.Order, error) {
	rows, err := r.client.TradeOrder.Query().Where(tradeorder.StatusEQ(status)).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]biz.Order, 0, len(rows))
	for _, row := range rows {
		out = append(out, orderFrom(row))
	}
	return out, nil
}

func (r *Repo) Commit(ctx context.Context, batch biz.Batch) error {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now()
	for _, o := range batch.Orders {
		if err := tx.TradeOrder.Create().
			SetClientOrderID(o.ClientOrderID).
			SetBook(o.Book).
			SetSymbol(o.Symbol).
			SetSide(o.Side).
			SetStatus(o.Status).
			SetPrice(o.Price).
			SetVolume(o.Volume).
			SetFilled(o.Filled).
			SetSource(o.Source).
			SetOperator(o.Operator).
			SetSignalID(o.SignalID).
			SetStrategyVersion(o.StrategyVersion).
			SetReason(o.Reason).
			SetChannel(o.Channel).
			SetCreatedAt(o.CreatedAt).
			SetUpdatedAt(now).
			OnConflictColumns(tradeorder.FieldClientOrderID).
			UpdateNewValues().
			Exec(ctx); err != nil {
			return err
		}
	}
	for _, f := range batch.Fills {
		if err := tx.TradeFill.Create().
			SetClientOrderID(f.ClientOrderID).
			SetBook(f.Book).
			SetSymbol(f.Symbol).
			SetSide(f.Side).
			SetQty(f.Qty).
			SetPrice(f.Price).
			SetAmount(f.Amount).
			SetFee(f.Fee).
			SetSignalID(f.SignalID).
			SetStrategyVersion(f.StrategyVersion).
			Exec(ctx); err != nil {
			return err
		}
	}
	for _, t := range batch.Trails {
		if err := tx.OrderTrail.Create().
			SetClientOrderID(t.ClientOrderID).
			SetStatus(t.Status).
			SetNote(t.Note).
			Exec(ctx); err != nil {
			return err
		}
	}
	if c := batch.Cash; c != nil {
		if err := tx.TradeCash.Create().
			SetBook(c.Book).
			SetCash(c.Cash).
			SetRealizedPnl(c.RealizedPnL).
			SetPaperDays(c.PaperDays).
			SetOpenHalted(c.OpenHalted).
			SetHaltReason(c.HaltReason).
			SetUpdatedAt(now).
			OnConflictColumns(tradecash.FieldBook).
			UpdateNewValues().
			Exec(ctx); err != nil {
			return err
		}
	}
	for _, p := range batch.Positions {
		b := tx.Position.Create().
			SetBook(p.Book).
			SetSymbol(p.Symbol).
			SetQuantity(p.Quantity).
			SetAvailable(p.Available).
			SetUpdatedAt(now)
		if p.AvgCost > 0 {
			b.SetAvgCost(p.AvgCost)
		}
		if p.LastPrice > 0 {
			b.SetCurrentPrice(p.LastPrice)
		}
		pnl := (p.LastPrice - p.AvgCost) * float64(p.Quantity)
		if p.LastPrice > 0 {
			b.SetPnl(pnl)
		}
		if err := b.OnConflictColumns(position.FieldBook, position.FieldSymbol).UpdateNewValues().Exec(ctx); err != nil {
			return err
		}
	}
	for _, ev := range batch.Events {
		env, err := events.New("trade", ev.Subject, events.TraceID(ctx), ev.Payload)
		if err != nil {
			return err
		}
		if err := outbox.Insert(ctx, tx, env); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *Repo) SaveSnapshot(ctx context.Context, book string, view biz.AccountView) error {
	ratio := 0.0
	if view.Equity > 0 {
		ratio = (view.Equity - view.Cash) / view.Equity
	}
	return r.client.AccountSnapshot.Create().
		SetBook(book).
		SetSnapshotTime(time.Now()).
		SetTotalEquity(view.Equity).
		SetAvailableFunds(view.Cash).
		SetUnrealizedPnl(view.UnrealizedPnL).
		SetRealizedPnl(view.RealizedPnL).
		SetMarginUsed(0).
		SetRiskRatio(ratio).
		SetPositionCount(view.PositionCount).
		Exec(ctx)
}

func orderFrom(row *ent.TradeOrder) biz.Order {
	return biz.Order{
		ClientOrderID: row.ClientOrderID, Book: row.Book, Symbol: row.Symbol, Side: row.Side,
		Status: row.Status, Price: row.Price, Volume: row.Volume, Filled: row.Filled,
		Source: row.Source, Operator: row.Operator, SignalID: row.SignalID,
		StrategyVersion: row.StrategyVersion, Reason: row.Reason, Channel: row.Channel,
		CreatedAt: row.CreatedAt,
	}
}

func fillFrom(row *ent.TradeFill) biz.Fill {
	return biz.Fill{
		ClientOrderID: row.ClientOrderID, Book: row.Book, Symbol: row.Symbol, Side: row.Side,
		Qty: row.Qty, Price: row.Price, Amount: row.Amount, Fee: row.Fee,
		SignalID: row.SignalID, StrategyVersion: row.StrategyVersion, CreatedAt: row.CreatedAt,
	}
}

func posFrom(row *ent.Position) biz.Position {
	p := biz.Position{Book: row.Book, Symbol: row.Symbol, Quantity: row.Quantity, Available: row.Available}
	if row.AvgCost != nil {
		p.AvgCost = *row.AvgCost
	}
	if row.CurrentPrice != nil {
		p.LastPrice = *row.CurrentPrice
	}
	return p
}
