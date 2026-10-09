package data

import (
	"context"
	"time"

	"server/app/strategy/internal/biz"
	"server/ent"
	"server/ent/position"
	"server/ent/stockblacklist"
	"server/ent/strategyversion"

	"entgo.io/ent/dialect/sql"
)

type paramRepo struct{ d *Data }

func NewParamRepo(d *Data) biz.ParamRepo { return &paramRepo{d: d} }

func (r *paramRepo) Values(ctx context.Context) (map[string]string, error) {
	rows, err := r.d.client.StrategyConfig.Query().All(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		out[row.ConfigKey] = row.ConfigValue
	}
	return out, nil
}

func (r *paramRepo) ActiveVersion(ctx context.Context) (*int, error) {
	row, err := r.d.client.StrategyVersion.Query().Where(strategyversion.IsActive(true)).
		Order(strategyversion.ByID(sql.OrderDesc())).First(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row.ID, nil
}

type blacklistRepo struct{ d *Data }

func NewBlacklistRepo(d *Data) biz.BlacklistRepo { return &blacklistRepo{d: d} }

func (r *blacklistRepo) Active(ctx context.Context, now time.Time) (map[string]bool, error) {
	codes, err := r.d.client.StockBlacklist.Query().
		Where(stockblacklist.Or(stockblacklist.ExpiresAtIsNil(), stockblacklist.ExpiresAtGT(now))).
		Select(stockblacklist.FieldStockCode).
		Strings(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(codes))
	for _, c := range codes {
		out[c] = true
	}
	return out, nil
}

// Add 已存在时更新原因、操作人和到期时间。
func (r *blacklistRepo) Add(ctx context.Context, symbol, reason, by string, expires *time.Time) error {
	old, err := r.d.client.StockBlacklist.Query().Where(stockblacklist.StockCode(symbol)).Only(ctx)
	if ent.IsNotFound(err) {
		return r.d.client.StockBlacklist.Create().
			SetStockCode(symbol).SetReason(reason).SetCreatedBy(by).SetNillableExpiresAt(expires).
			Exec(ctx)
	}
	if err != nil {
		return err
	}
	u := old.Update().SetReason(reason).SetCreatedBy(by)
	if expires == nil {
		u.ClearExpiresAt()
	} else {
		u.SetExpiresAt(*expires)
	}
	return u.Exec(ctx)
}

func (r *blacklistRepo) Remove(ctx context.Context, symbol string) error {
	_, err := r.d.client.StockBlacklist.Delete().Where(stockblacklist.StockCode(symbol)).Exec(ctx)
	return err
}

type positionRepo struct{ d *Data }

func NewPositionRepo(d *Data) biz.PositionRepo { return &positionRepo{d: d} }

func (r *positionRepo) Holdings(ctx context.Context, book string) ([]biz.Holding, error) {
	rows, err := r.d.client.Position.Query().Where(position.Book(book), position.QuantityGT(0)).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]biz.Holding, len(rows))
	for i, row := range rows {
		out[i] = biz.Holding{Symbol: row.Symbol, Quantity: row.Quantity, Available: row.Available}
		if row.AvgCost != nil {
			out[i].AvgCost = *row.AvgCost
		}
	}
	return out, nil
}
