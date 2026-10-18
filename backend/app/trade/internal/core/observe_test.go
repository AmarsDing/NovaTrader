package core

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/nats-io/nats.go"
	v1 "server/api/platform/v1"
	"server/app/trade/internal/biz"
	"server/pkg/events"
	"server/pkg/market"
	"server/pkg/observe"
)

func tradeObserved(t *testing.T, key string) *v1.ConsumerCounters {
	t.Helper()
	for _, op := range observe.ForService("trade").Snapshot().Operations {
		if op.Key == key {
			return op.Total
		}
	}
	t.Fatal("missing operation", key)
	return nil
}

type matchLedgerFailure struct{ *biz.MemStore }

func (m *matchLedgerFailure) WithLedger(context.Context, string, func(context.Context) error) error {
	return fmt.Errorf("injected ledger failure")
}

func TestActualMinuteConsumerSeparatesRejectedSkippedCompletedAndFailed(t *testing.T) {
	before := tradeObserved(t, "minute_bar")
	noticeBefore := tradeObserved(t, "minute_notice")
	barTime := time.Now().UTC().Truncate(time.Minute)
	up, down := 11., 9.
	bar := market.Bar{Symbol: "600519.SH", Time: barTime, Open: 10, High: 10, Low: 10, Close: 10, Volume: 1000, LimitKnown: true, LimitUp: &up, LimitDown: &down}
	deliver := func(r *Runner, b market.Bar) {
		env, err := events.New("market", events.SubjectMarketBar, "", map[string]any{"bars": []market.Bar{b}})
		if err != nil {
			t.Fatal(err)
		}
		raw, err := env.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		r.onBars(&nats.Msg{Subject: events.SubjectMarketBar, Data: raw})
	}
	r := &Runner{e: biz.NewEngine(biz.NewMemStore(), allowCheck{}, biz.Settings{}, log.DefaultLogger), now: func() time.Time { return barTime.Add(time.Minute) }, log: log.NewHelper(log.DefaultLogger)}
	r.onBars(&nats.Msg{Subject: events.SubjectMarketBar, Data: []byte(`{}`)})
	unknown := bar
	unknown.LimitKnown = false
	deliver(r, unknown)
	deliver(r, bar) // A real cursor commit, with no invented order/fill claim.
	deliver(r, bar) // Same source minute is skipped, even with a new event ID.
	failed := &Runner{e: biz.NewEngine(&matchLedgerFailure{biz.NewMemStore()}, allowCheck{}, biz.Settings{}, log.DefaultLogger), now: r.now, log: r.log}
	deliver(failed, bar)
	r.stopping = true
	deliver(r, bar)
	after := tradeObserved(t, "minute_bar")
	noticeAfter := tradeObserved(t, "minute_notice")
	if after.Rejected-before.Rejected != 1 || after.Succeeded-before.Succeeded != 1 || after.Skipped-before.Skipped != 1 || after.Failed-before.Failed != 1 {
		t.Fatalf("consumer outcomes confused before=%v after=%v", before, after)
	}
	if noticeAfter.Rejected-noticeBefore.Rejected != 1 || noticeAfter.Cancelled-noticeBefore.Cancelled != 1 {
		t.Fatalf("parse/stop outcomes confused before=%v after=%v", noticeBefore, noticeAfter)
	}
}

type rejectedExitRisk struct{}

func (rejectedExitRisk) Check(context.Context, biz.CheckIn) (biz.CheckOut, error) {
	return biz.CheckOut{Approved: false, Reason: "injected final-risk rejection"}, nil
}
func TestRiskRejectedExitIsNotRecordedAsSuccessfulOrder(t *testing.T) {
	before := tradeObserved(t, "risk_exit")
	r := &Runner{e: biz.NewEngine(biz.NewMemStore(), rejectedExitRisk{}, biz.Settings{}, log.DefaultLogger), log: log.NewHelper(log.DefaultLogger)}
	env, _ := events.New("risk", events.SubjectRiskExit, "", map[string]any{"exit_id": "observe-exit", "symbol": "600519.SH", "account_type": "SIM", "volume": 100, "price": 10})
	raw, _ := env.Marshal()
	r.onExit(&nats.Msg{Subject: events.SubjectRiskExit, Data: raw})
	after := tradeObserved(t, "risk_exit")
	if after.Rejected-before.Rejected != 1 || after.Succeeded-before.Succeeded != 0 {
		t.Fatalf("risk rejection called success before=%v after=%v", before, after)
	}
}
