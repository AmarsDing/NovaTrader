package data

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/uuid"
	"server/app/strategy/internal/biz"
	"server/ent"
	"server/ent/executionintent"
	"server/ent/signalevent"
	"server/ent/tradefill"
	"server/ent/tradeorder"
	"server/ent/tradesignal"
	"server/pkg/events"
)

func executionSignal(t *testing.T, d *Data, status biz.Status) (*biz.Usecase, biz.SignalRepo, *biz.Signal, string) {
	t.Helper()
	repo := NewSignalRepo(d)
	s := &biz.Signal{Book: "paper", Time: time.Now(), Symbol: "600519.SH", Name: "合成投影测试", Side: "buy", Entry: 10, StopLoss: 9, TakeProfit: 11, Reason: "synthetic execution contract", Status: status, ValidUntil: time.Now().Add(-time.Minute)}
	if _, err := repo.Create(ctx, s, envFor); err != nil {
		t.Fatal(err)
	}
	id := "projection-" + uuid.NewString()
	t.Cleanup(func() {
		_, _ = d.client.TradeFill.Delete().Where(tradefill.ClientOrderID(id)).Exec(ctx)
		_, _ = d.client.TradeOrder.Delete().Where(tradeorder.ClientOrderID(id)).Exec(ctx)
	})
	uc := biz.NewUsecase(biz.NewSettings(nil), nil, nil, nil, repo, nil, nil, nil, nil, log.DefaultLogger)
	return uc, repo, s, id
}

func projectionOrder(t *testing.T, d *Data, s *biz.Signal, id, state string, filled int) *ent.TradeOrder {
	t.Helper()
	o, err := d.client.TradeOrder.Create().SetClientOrderID(id).SetBook(s.Book).SetSymbol(s.Symbol).SetSide(s.Side).SetStatus(state).SetPrice(10).SetVolume(300).SetFilled(filled).SetSignalID(strconv.Itoa(s.ID)).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestPostgresExecutionProjectionProgressIsAtomicAndIdempotent(t *testing.T) {
	d := newTestData(t)
	uc, repo, s, id := executionSignal(t, d, biz.StatusPending)
	o := projectionOrder(t, d, s, id, "submitted", 0)
	changed, err := uc.ReconcileExecutionOrder(ctx, id)
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	got, _ := repo.Get(ctx, s.ID)
	if got.Status != biz.StatusExecuting || got.ExecutedVolume != nil || got.ExecutedPrice != nil {
		t.Fatalf("invented execution: %+v", got)
	}
	if changed, err = uc.ReconcileExecutionOrder(ctx, id); err != nil || changed {
		t.Fatal("duplicate wakeup added progress", changed, err)
	}
	for _, part := range []struct {
		qty, filled int
		price       float64
		state       string
	}{{100, 100, 10.01, "partial"}, {200, 300, 10.02, "filled"}} {
		tx, err := d.client.Tx(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, err = tx.TradeFill.Create().SetClientOrderID(id).SetBook(s.Book).SetSymbol(s.Symbol).SetSide(s.Side).SetQty(part.qty).SetPrice(part.price).SetAmount(float64(part.qty) * part.price).SetFee(5).SetSignalID(strconv.Itoa(s.ID)).Save(ctx)
		if err == nil {
			_, err = tx.TradeOrder.UpdateOneID(o.ID).SetFilled(part.filled).SetStatus(part.state).Save(ctx)
		}
		if err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		if changed, err = uc.ReconcileExecutionOrder(ctx, id); err != nil || !changed {
			t.Fatal(changed, err)
		}
	}
	got, _ = repo.Get(ctx, s.ID)
	if got.Status != biz.StatusDone || got.ExecutedVolume == nil || *got.ExecutedVolume != 300 || got.ExecutedPrice == nil || *got.ExecutedPrice != 10.0167 || got.PnlPct != nil {
		t.Fatalf("actual fill aggregate incorrect: %+v", got)
	}
	if changed, err = uc.ReconcileExecutionOrder(ctx, id); err != nil || changed {
		t.Fatal("rounded average caused repeated progress", changed, err)
	}
	evs, err := d.client.SignalEvent.Query().Where(signalevent.SignalID(s.ID)).Order(signalevent.ByID()).All(ctx)
	if err != nil || len(evs) != 4 || evs[3].FromStatus != "done" || evs[3].ToStatus != "done" {
		t.Fatalf("missing atomic progress trail: %v err=%v", evs, err)
	}
}

func TestPostgresExecutionProjectionFailureRollsBackSignalAndEvents(t *testing.T) {
	d := newTestData(t)
	uc, repo, s, id := executionSignal(t, d, biz.StatusPending)
	projectionOrder(t, d, s, id, "submitted", 0)
	d.client.Outbox.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(context.Context, ent.Mutation) (ent.Value, error) {
			return nil, errors.New("synthetic outbox failure")
		})
	})
	if _, err := uc.ReconcileExecutionOrder(ctx, id); err == nil {
		t.Fatal("injected outbox failure ignored")
	}
	got, _ := repo.Get(ctx, s.ID)
	n, err := d.client.SignalEvent.Query().Where(signalevent.SignalID(s.ID)).Count(ctx)
	if err != nil || got.Status != biz.StatusPending || n != 1 {
		t.Fatalf("partial projection committed: %+v count=%d err=%v", got, n, err)
	}
}

func TestPostgresExpiryWaitsForTradeSignalLockAndThenSeesOrder(t *testing.T) {
	d := newTestData(t)
	_, repo, s, id := executionSignal(t, d, biz.StatusPending)
	tx, err := d.client.Tx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.TradeSignal.Query().Where(tradesignal.ID(s.ID)).ForShare().Only(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.TradeOrder.Create().SetClientOrderID(id).SetBook(s.Book).SetSymbol(s.Symbol).SetSide(s.Side).SetStatus("submitted").SetPrice(10).SetVolume(300).SetSignalID(strconv.Itoa(s.ID)).Save(ctx); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := repo.Transition(ctx, s.ID, biz.StatusPending, biz.StatusExpired, "expired", "strategy", "", envFor, nil)
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("expiry bypassed in-flight signal lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if !errors.Is(err, biz.ErrExecutionProtected) {
			t.Fatalf("committed trade not protected: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("expiry did not resume")
	}
	got, _ := repo.Get(ctx, s.ID)
	if got.Status != biz.StatusPending {
		t.Fatal("expiry erased committed order", got.Status)
	}
}

func TestPostgresClaimedIntentPreventsExpiryAndCancellation(t *testing.T) {
	d := newTestData(t)
	_, repo, s, id := executionSignal(t, d, biz.StatusPending)
	key := fmt.Sprintf("signal:SIM:default:%d", s.ID)
	intent, err := d.client.ExecutionIntent.Create().SetBusinessKey(key).SetClientOrderID(id).SetRequestHash("synthetic").SetBook(s.Book).SetState("claimed").Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = d.client.ExecutionIntent.Delete().Where(executionintent.ID(intent.ID)).Exec(ctx) })
	for _, to := range []biz.Status{biz.StatusExpired, biz.StatusCancelled} {
		if _, err = repo.Transition(ctx, s.ID, biz.StatusPending, to, "synthetic", "strategy", "", envFor, nil); !errors.Is(err, biz.ErrExecutionProtected) {
			t.Fatalf("intent did not protect %s: %v", to, err)
		}
	}
}

func TestPostgresZeroFillCancellationAndLateFillCorrection(t *testing.T) {
	d := newTestData(t)
	uc, repo, s, id := executionSignal(t, d, biz.StatusPending)
	o := projectionOrder(t, d, s, id, "cancelled", 0)
	if changed, err := uc.ReconcileExecutionOrder(ctx, id); err != nil || !changed {
		t.Fatal(changed, err)
	}
	got, _ := repo.Get(ctx, s.ID)
	if got.Status != biz.StatusCancelled || got.ExecutedPrice != nil {
		t.Fatal("zero fill cancellation not reflected", got)
	}
	tx, err := d.client.Tx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.TradeFill.Create().SetClientOrderID(id).SetBook(s.Book).SetSymbol(s.Symbol).SetSide(s.Side).SetQty(100).SetPrice(10).SetAmount(1000).SetFee(5).SetSignalID(strconv.Itoa(s.ID)).Save(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.TradeOrder.UpdateOneID(o.ID).SetFilled(100).Save(ctx); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, _, err = uc.ReconcileExecutionsPage(events.WithTraceID(ctx, "synthetic-restart"), 0, 200); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.Get(ctx, s.ID)
	if got.Status != biz.StatusDone || *got.ExecutedVolume != 100 || got.PnlPct != nil {
		t.Fatal("late fill disappeared or invented PnL", got)
	}
}
