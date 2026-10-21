package data

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"server/app/trade/internal/biz"
	"server/ent"
	"server/ent/executionintent"
	"server/ent/tradeledgerlock"
	"server/ent/tradeorder"

	"github.com/jackc/pgx/v5/pgconn"
)

type ledgerTxKey struct{}

func (r *Repo) InLedger(ctx context.Context) bool { return ctx.Value(ledgerTxKey{}) != nil }
func (r *Repo) FinishIntentDetached(ctx context.Context, id, state, reason string) error {
	detached, cancel := context.WithTimeout(context.WithValue(context.WithoutCancel(ctx), ledgerTxKey{}, nil), 5*time.Second)
	defer cancel()
	if state == "unknown" {
		row, err := r.client.TradeOrder.Query().Where(tradeorder.ClientOrderID(id)).Only(detached)
		if err == nil {
			state, reason = row.Status, row.Reason
		} else if !ent.IsNotFound(err) {
			return err
		}
	}
	return r.FinishIntent(detached, id, state, reason)
}

func (r *Repo) clientFor(ctx context.Context) *ent.Client {
	if tx, ok := ctx.Value(ledgerTxKey{}).(*ent.Tx); ok {
		return tx.Client()
	}
	return r.client
}
func (r *Repo) transaction(ctx context.Context) (*ent.Tx, func() error, func(), error) {
	if tx, ok := ctx.Value(ledgerTxKey{}).(*ent.Tx); ok {
		return tx, func() error { return nil }, func() {}, nil
	}
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	return tx, tx.Commit, func() { _ = tx.Rollback() }, nil
}
func (r *Repo) WithLedger(ctx context.Context, book string, fn func(context.Context) error) error {
	if _, ok := ctx.Value(ledgerTxKey{}).(*ent.Tx); ok {
		return fn(ctx)
	}
	// The lock row is infrastructure, not an initialized cash account.
	if err := r.client.TradeLedgerLock.Create().SetBook(book).OnConflictColumns(tradeledgerlock.FieldBook).DoNothing().Exec(ctx); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.TradeLedgerLock.Query().Where(tradeledgerlock.Book(book)).ForUpdate().Only(ctx); err != nil {
		return err
	}
	if err = fn(context.WithValue(ctx, ledgerTxKey{}, tx)); err != nil {
		return err
	}
	return tx.Commit()
}
func intentFrom(row *ent.ExecutionIntent) biz.Intent {
	return biz.Intent{BusinessKey: row.BusinessKey, ClientOrderID: row.ClientOrderID, RequestHash: row.RequestHash, Book: row.Book, PlanID: row.PlanID, PlanRevision: row.PlanRevision, State: row.State, Reason: row.Reason}
}
func (r *Repo) ClaimIntent(ctx context.Context, in biz.Intent) (biz.Intent, bool, error) {
	row, err := r.client.ExecutionIntent.Create().SetBusinessKey(in.BusinessKey).SetClientOrderID(in.ClientOrderID).SetRequestHash(in.RequestHash).SetBook(in.Book).SetPlanID(in.PlanID).SetPlanRevision(in.PlanRevision).Save(ctx)
	if err == nil {
		return intentFrom(row), true, nil
	}
	if !isConstraintError(err) {
		return biz.Intent{}, false, err
	}
	row, err = r.client.ExecutionIntent.Query().Where(executionintent.Or(executionintent.BusinessKey(in.BusinessKey), executionintent.ClientOrderID(in.ClientOrderID))).Only(ctx)
	if err != nil {
		return biz.Intent{}, false, fmt.Errorf("%w: 委托键冲突", biz.ErrConflict)
	}
	return intentFrom(row), false, nil
}

// isConstraintError 判唯一约束冲突：ent 的 IsConstraintError 靠错误文本匹配，
// zh_CN locale 的 PostgreSQL 返回本地化的「重复键违反唯一约束」会漏判——
// 显式认 SQLSTATE 23505，不只依赖文本（中文机上提交流程的幂等闸门不被击穿）。
func isConstraintError(err error) bool {
	if ent.IsConstraintError(err) {
		return true
	}
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
func (r *Repo) FinishIntent(ctx context.Context, id, state, reason string) error {
	n, err := r.clientFor(ctx).ExecutionIntent.Update().Where(executionintent.ClientOrderID(id)).SetState(state).SetReason(reason).SetUpdatedAt(time.Now()).Save(ctx)
	if err == nil && n != 1 {
		return biz.ErrNotFound
	}
	return err
}
