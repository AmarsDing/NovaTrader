package core

import (
	"server/pkg/events"
	"testing"
)

func tradeMessage(t *testing.T, subject, source string, payload any) []byte {
	t.Helper()
	e, err := events.New(source, subject, "causal-test", payload)
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestExecutionEventIdentityOnlyAcceptsOriginalOrderWakeup(t *testing.T) {
	for _, p := range []any{map[string]any{"client_order_id": "same"}, map[string]any{"ClientOrderID": "same", "Qty": 9999, "Price": 9999, "SignalID": "spoofed"}} {
		_, id, err := executionOrderID(events.SubjectTradeFill, tradeMessage(t, events.SubjectTradeFill, "trade", p))
		if err != nil || id != "same" {
			t.Fatalf("valid wakeup rejected: id=%q err=%v", id, err)
		}
	}
	for _, tc := range []struct {
		name, subject, source, transport string
		p                                any
	}{
		{"wrong_source", events.SubjectTradeOrder, "client", events.SubjectTradeOrder, map[string]any{"client_order_id": "same"}},
		{"wrong_transport", events.SubjectTradeOrder, "trade", events.SubjectTradeFill, map[string]any{"client_order_id": "same"}},
		{"missing_id", events.SubjectTradeOrder, "trade", events.SubjectTradeOrder, map[string]any{"signal_id": "1"}},
		{"ambiguous_id", events.SubjectTradeOrder, "trade", events.SubjectTradeOrder, map[string]any{"client_order_id": "one", "ClientOrderID": "two"}},
		{"bad_id_type", events.SubjectTradeOrder, "trade", events.SubjectTradeOrder, map[string]any{"client_order_id": 123}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := executionOrderID(tc.transport, tradeMessage(t, tc.subject, tc.source, tc.p)); err == nil {
				t.Fatal("invalid identity accepted")
			}
		})
	}
}

func TestExecutionLegacyAggregateCompatibilityIsExplicit(t *testing.T) {
	_, id, err := executionOrderID(events.SubjectTradeOrder, tradeMessage(t, events.SubjectTradeOrder, "trade", map[string]any{"book": "paper", "symbol": "600519.SH", "count": 1}))
	if err != nil || id != "" {
		t.Fatalf("legitimate legacy aggregate rejected: %q %v", id, err)
	}
	for _, payload := range []any{
		map[string]any{"book": "paper", "symbol": "600519.SH", "count": 0},
		map[string]any{"book": "other", "symbol": "600519.SH", "count": 1},
		map[string]any{"book": "paper", "symbol": "invalid", "count": 1},
		map[string]any{"book": "paper", "symbol": "600519.SH", "count": 1, "filled": 123},
		map[string]any{"book": "paper", "symbol": "600519.SH"},
	} {
		if _, _, err := executionOrderID(events.SubjectTradeOrder, tradeMessage(t, events.SubjectTradeOrder, "trade", payload)); err == nil {
			t.Fatalf("invalid aggregate accepted: %v", payload)
		}
	}
}
