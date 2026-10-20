package core

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"server/app/trade/internal/biz"
	"server/pkg/events"
	"server/pkg/market"
	"server/pkg/tradecal"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
)

type lifecycleChecker struct {
	calls      atomic.Int32
	check      func(context.Context)
	onlySymbol string
}

func (c *lifecycleChecker) Check(ctx context.Context, in biz.CheckIn) (biz.CheckOut, error) {
	if c.onlySymbol == "" || c.onlySymbol == in.Symbol {
		c.calls.Add(1)
	}
	if c.check != nil {
		c.check(ctx)
	}
	return biz.CheckOut{Approved: true, Volume: in.Volume}, ctx.Err()
}

func lifecycleRunner(t *testing.T, checker *lifecycleChecker) *Runner {
	return lifecycleRunnerSymbol(t, checker, "000001.SZ")
}

func lifecycleRunnerSymbol(t *testing.T, checker *lifecycleChecker, symbol string) *Runner {
	t.Helper()
	store := biz.NewMemStore()
	if err := store.Commit(context.Background(), biz.Batch{
		Cash:      &biz.Cash{Book: biz.BookPaper, Cash: 1000000, DayStartDate: time.Now().In(tradecal.Shanghai()).Format("2006-01-02"), DayStartEquity: 1100000},
		Positions: []biz.Position{{Book: biz.BookPaper, Symbol: symbol, Quantity: 10000, Available: 10000, AvgCost: 10, LastPrice: 10}},
	}); err != nil {
		t.Fatal(err)
	}
	e := biz.NewEngine(store, checker, biz.Settings{}, log.DefaultLogger)
	return NewRunner(nil, e, nil, nil, nil, nil, log.DefaultLogger)
}

func exitMessage(t *testing.T, source, subject, account string) *nats.Msg {
	return exitMessageSymbol(t, source, subject, account, "000001.SZ")
}

func exitMessageSymbol(t *testing.T, source, subject, account, symbol string) *nats.Msg {
	t.Helper()
	env, err := events.New(source, subject, "lifecycle-test", map[string]any{
		"exit_id": "exit-" + uuid.NewString(), "symbol": symbol, "account_type": account,
		"volume": 100, "price": 9.9, "trigger": "stop_loss",
	})
	if err != nil {
		t.Fatal(err)
	}
	body, err := env.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return &nats.Msg{Subject: events.SubjectRiskExit, Data: body}
}

func TestExitRequiresRiskIdentityAndExplicitAccount(t *testing.T) {
	for _, tc := range []struct {
		name, source, subject, account, wire string
		valid                                bool
	}{
		{"trusted SIM", "risk", events.SubjectRiskExit, "SIM", events.SubjectRiskExit, true},
		{"other producer", "strategy", events.SubjectRiskExit, "SIM", events.SubjectRiskExit, false},
		{"other envelope subject", "risk", events.SubjectTradeOrder, "SIM", events.SubjectRiskExit, false},
		{"other wire subject", "risk", events.SubjectRiskExit, "SIM", events.SubjectTradeOrder, false},
		{"missing account", "risk", events.SubjectRiskExit, "", events.SubjectRiskExit, false},
		{"unknown account", "risk", events.SubjectRiskExit, "unknown", events.SubjectRiskExit, false},
		{"no implicit case conversion", "risk", events.SubjectRiskExit, "sim", events.SubjectRiskExit, false},
		{"no whitespace default", "risk", events.SubjectRiskExit, " ", events.SubjectRiskExit, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checker := &lifecycleChecker{}
			r := lifecycleRunner(t, checker)
			message := exitMessage(t, tc.source, tc.subject, tc.account)
			message.Subject = tc.wire
			r.onExit(message)
			orders, err := r.e.Orders(context.Background(), "SIM")
			if err != nil {
				t.Fatal(err)
			}
			if tc.valid {
				if checker.calls.Load() != 1 || len(orders) != 1 || orders[0].SignalID != "" || orders[0].Source != "watch" || orders[0].Operator != "risk" {
					t.Fatalf("trusted exit lost identity or gained fake signal: %+v", orders)
				}
			} else if checker.calls.Load() != 0 || len(orders) != 0 {
				t.Fatalf("untrusted/missing account reached order engine: %+v", orders)
			}
		})
	}
}

func TestStoppedRunnerIgnoresQueuedExitAndBarCallbacks(t *testing.T) {
	checker := &lifecycleChecker{}
	r := lifecycleRunner(t, checker)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := r.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if err := r.Stop(ctx); err != nil {
		t.Fatal("repeated stop", err)
	}
	r.onExit(exitMessage(t, "risk", events.SubjectRiskExit, "SIM"))
	// A stopped callback must return before touching a missing engine or parsing any bars.
	r.e = nil
	r.onBars(&nats.Msg{Subject: events.SubjectMarketBar, Data: []byte("queued bar")})
	if checker.calls.Load() != 0 {
		t.Fatal("new order reached risk after stop")
	}
	if err := r.Start(context.Background()); err == nil {
		t.Fatal("stopped object restarted with old callbacks")
	}
}

func TestStopCancelsAndWaitsForRunningExit(t *testing.T) {
	entered, cancelled, callbackDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	checker := &lifecycleChecker{check: func(ctx context.Context) {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("exit callback has no operation deadline")
		}
		close(entered)
		<-ctx.Done()
		close(cancelled)
	}}
	r := lifecycleRunner(t, checker)
	// The same lifecycle context installed by Start, without requiring a broker for this test.
	r.runCtx, r.cancel = context.WithCancel(context.Background())
	go func() { defer close(callbackDone); r.onExit(exitMessage(t, "risk", events.SubjectRiskExit, "SIM")) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("callback did not reach risk")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := r.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelled:
	default:
		t.Fatal("stop did not cancel order context")
	}
	select {
	case <-callbackDone:
	case <-time.After(time.Second):
		t.Fatal("stop left callback running")
	}
	r.onExit(exitMessage(t, "risk", events.SubjectRiskExit, "SIM"))
	if checker.calls.Load() != 1 {
		t.Fatal("stale callback created a new order")
	}
}

func TestStopDeadlineAndConcurrentCallbackAdmission(t *testing.T) {
	r := lifecycleRunner(t, &lifecycleChecker{})
	ctx, finish, ok := r.beginCallback()
	if !ok {
		t.Fatal("callback admission failed")
	}
	defer finish()
	stopCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := r.Stop(stopCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("did not honor stop deadline: %v", err)
	}
	// The direct-call compatibility context is bounded; after Stop no callback is admitted.
	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("missing callback deadline")
	}
	if _, _, ok := r.beginCallback(); ok {
		t.Fatal("admitted new callback while waiting for shutdown")
	}
}

func TestCancelledStartupCannotLeaveActiveRunner(t *testing.T) {
	r := lifecycleRunner(t, &lifecycleChecker{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := r.Start(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled startup: %v", err)
	}
	stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
	defer stopCancel()
	if err := r.Stop(stopCtx); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := r.beginCallback(); ok {
		t.Fatal("failed startup remained active")
	}
}

func TestIsolatedNATSRunnerStopUnsubscribesBothAndPreservesDurable(t *testing.T) {
	raw := os.Getenv("NT_TEST_NATS_URL")
	if raw == "" {
		t.Skip("set isolated NT_TEST_NATS_URL")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.Port() == "4222" {
		t.Fatal("requires explicit non-production loopback NATS")
	}
	bus, err := events.Dial(raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bus.Close)
	checker := &lifecycleChecker{}
	// The shared isolated stream may retain older fixtures. A unique synthetic
	// symbol prevents those recovery events from reserving this test's position.
	randomID := uuid.New()
	code := 100000 + (int(randomID[0])<<16|int(randomID[1])<<8|int(randomID[2]))%900000
	symbol := fmt.Sprintf("%06d.SZ", code)
	checker.onlySymbol = symbol
	r := lifecycleRunnerSymbol(t, checker, symbol)
	r.bus = bus
	r.interval = 20 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer r.Stop(context.Background())
	bars, exits := r.sub, r.exitSub
	if bars == nil || exits == nil {
		t.Fatal("both subscriptions were not installed")
	}
	message := exitMessageSymbol(t, "risk", events.SubjectRiskExit, "SIM", symbol)
	env, _ := events.Unmarshal(message.Data)
	publish := func(env events.Envelope) {
		body, err := env.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		ack, err := bus.JetStream().Publish(env.Subject, body, nats.MsgId(env.EventID), nats.Context(ctx))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = bus.JetStream().DeleteMsg(events.StreamName, ack.Sequence) })
	}
	publish(env)
	deadline := time.Now().Add(time.Second)
	for checker.calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if checker.calls.Load() != 1 {
		t.Fatal("actual risk exit did not reach callback")
	}
	found := false
	for !found && time.Now().Before(deadline) {
		orders, err := r.e.Orders(ctx, "SIM")
		if err != nil {
			t.Fatal(err)
		}
		for _, order := range orders {
			found = found || (order.Symbol == symbol && order.Status == biz.StatusSubmitted && order.SignalID == "")
		}
		if !found {
			time.Sleep(5 * time.Millisecond)
		}
	}
	if !found {
		t.Fatal("this test's exit did not create its expected SIM order")
	}
	if err := r.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if bars.IsValid() || exits.IsValid() {
		t.Fatal("stop left a live subscription")
	}
	if _, err := bus.JetStream().ConsumerInfo(events.StreamName, exitConsumer); err != nil {
		t.Fatal("stop deleted durable recovery position", err)
	}
	message = exitMessageSymbol(t, "risk", events.SubjectRiskExit, "SIM", symbol)
	env, _ = events.Unmarshal(message.Data)
	publish(env)
	barEnv, _ := events.New("market", events.SubjectMarketBar, "", map[string]any{"bars": []market.Bar{}})
	if err := bus.Publish(ctx, barEnv); err != nil {
		t.Fatal(err)
	}
	if err := bus.Conn().Flush(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if checker.calls.Load() != 1 {
		t.Fatal("new order reached risk after actual unsubscribe")
	}
}
