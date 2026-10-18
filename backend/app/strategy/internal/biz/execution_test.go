package biz

import (
	"errors"
	"math"
	"testing"
)

func executionFixture() (*Signal, ExecutionFacts) {
	return &Signal{ID: 1, Book: "paper", Symbol: "600519.SH", Side: "buy", Status: StatusPending}, ExecutionFacts{
		Orders: []ExecutionOrder{{ClientOrderID: "original", Book: "paper", Symbol: "600519.SH", Side: "buy", Status: "submitted", Volume: 300}},
	}
}

func TestExecutionProjectionUsesActualFillsAndPreservesUnknown(t *testing.T) {
	cur, facts := executionFixture()
	p, err := planExecution(cur, facts)
	if err != nil || p.Status != StatusExecuting || p.ExecutedPrice != nil || p.ExecutedVolume != nil {
		t.Fatalf("order acknowledgement invented fill: %+v err=%v", p, err)
	}
	facts.Orders[0].Status = "partial"
	facts.Orders[0].Filled = 100
	facts.Fills = []ExecutionFill{{ID: 1, ClientOrderID: "original", Book: "paper", Symbol: cur.Symbol, Side: "buy", Qty: 100, Price: 10.01, Amount: 1001}}
	p, err = planExecution(cur, facts)
	if err != nil || p.Status != StatusDone || *p.ExecutedVolume != 100 || *p.ExecutedPrice != 10.01 {
		t.Fatalf("partial fill not reflected: %+v err=%v", p, err)
	}
	facts.Orders[0].Status = "filled"
	facts.Orders[0].Filled = 300
	facts.Fills = append(facts.Fills, ExecutionFill{ID: 2, ClientOrderID: "original", Book: "paper", Symbol: cur.Symbol, Side: "buy", Qty: 200, Price: 10.02, Amount: 2004})
	p, err = planExecution(cur, facts)
	if err != nil || *p.ExecutedVolume != 300 || *p.ExecutedPrice != 10.0167 {
		t.Fatalf("weighted average not based on actual amount: %+v err=%v", p, err)
	}
	if CanTransition(StatusPending, StatusDone) || ActorAllowed(StatusPending, StatusApproved, ActorTrade) {
		t.Fatal("internal projection weakened external transition authority")
	}
}

func TestExecutionCancellationUnknownAndLateFill(t *testing.T) {
	for _, state := range []string{"pending_confirm", "submitted", "submitting", "cancel_pending", "unknown"} {
		cur, f := executionFixture()
		f.Orders[0].Status = state
		p, err := planExecution(cur, f)
		if err != nil || p.Status != StatusExecuting || p.ExecutedVolume != nil {
			t.Fatalf("%s treated as terminal: %+v %v", state, p, err)
		}
	}
	cur, f := executionFixture()
	f.Orders[0].Status = "cancelled"
	p, err := planExecution(cur, f)
	if err != nil || p.Status != StatusCancelled {
		t.Fatal(p, err)
	}
	f.UnresolvedIntent = true
	p, err = planExecution(cur, f)
	if err != nil || p.Status != StatusExecuting {
		t.Fatal("unresolved intent treated as proof of failure", p, err)
	}
	f.UnresolvedIntent = false
	f.Orders[0].Filled = 100
	f.Fills = []ExecutionFill{{ID: 1, ClientOrderID: "original", Book: cur.Book, Symbol: cur.Symbol, Side: cur.Side, Qty: 100, Price: 10, Amount: 1000}}
	for _, status := range []Status{StatusPending, StatusExecuting, StatusCancelled, StatusExpired, StatusRejected} {
		cur.Status = status
		p, err = planExecution(cur, f)
		if err != nil || p.Status != StatusDone {
			t.Fatalf("actual fill erased by %s: %+v %v", status, p, err)
		}
	}
	cur.Status = StatusClosed
	p, err = planExecution(cur, f)
	if err != nil || p.Status != StatusClosed || p.ExecutedPrice != nil {
		t.Fatal("projection must not fabricate closed-trade allocation or PnL", p, err)
	}
}

func TestExecutionProjectionRejectsContradictoryLedger(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Signal, *ExecutionFacts)
	}{
		{"other_account", func(s *Signal, f *ExecutionFacts) { f.Orders[0].Book = "live" }},
		{"other_symbol", func(s *Signal, f *ExecutionFacts) { f.Orders[0].Symbol = "000001.SZ" }},
		{"other_side", func(s *Signal, f *ExecutionFacts) { f.Orders[0].Side = "sell" }},
		{"unknown_state", func(s *Signal, f *ExecutionFacts) { f.Orders[0].Status = "file_published" }},
		{"overfilled", func(s *Signal, f *ExecutionFacts) { f.Orders[0].Filled = 301 }},
		{"missing_fill", func(s *Signal, f *ExecutionFacts) { f.Orders[0].Filled = 100 }},
		{"duplicate_order", func(s *Signal, f *ExecutionFacts) { f.Orders = append(f.Orders, f.Orders[0]) }},
		{"volume_regression", func(s *Signal, f *ExecutionFacts) { v := 100; s.ExecutedVolume = &v }},
		{"orphan_fill", func(s *Signal, f *ExecutionFacts) {
			f.Fills = []ExecutionFill{{ID: 1, ClientOrderID: "wrong", Book: s.Book, Symbol: s.Symbol, Side: s.Side, Qty: 100, Price: 10, Amount: 1000}}
		}},
		{"invalid_amount", func(s *Signal, f *ExecutionFacts) {
			f.Orders[0].Filled = 100
			f.Fills = []ExecutionFill{{ID: 1, ClientOrderID: "original", Book: s.Book, Symbol: s.Symbol, Side: s.Side, Qty: 100, Price: 10, Amount: 1001}}
		}},
		{"nonfinite_price", func(s *Signal, f *ExecutionFacts) {
			f.Orders[0].Filled = 100
			f.Fills = []ExecutionFill{{ID: 1, ClientOrderID: "original", Book: s.Book, Symbol: s.Symbol, Side: s.Side, Qty: 100, Price: math.NaN(), Amount: 1000}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, f := executionFixture()
			tc.change(s, &f)
			if _, err := planExecution(s, f); err == nil {
				t.Fatal("contradictory ledger accepted")
			}
		})
	}
	s, f := executionFixture()
	f.Orders[0].Filled = 100
	if _, err := planExecution(s, f); !errors.Is(err, ErrExecutionProtected) {
		t.Fatalf("unreconciled order must remain protected: %v", err)
	}
}
