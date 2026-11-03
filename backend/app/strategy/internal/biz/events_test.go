package biz

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSignalEventsUnknown(t *testing.T) {
	e := newEnv(t, nil, nil)
	if _, err := e.uc.SignalEvents(context.Background(), 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestSignalEventsOrdered(t *testing.T) {
	e := newEnv(t, nil, nil)
	id := e.signals.put(Signal{Book: "live", Symbol: "600000", Side: "buy", Status: StatusPending})
	now := time.Now()
	e.signals.eventRows = []SignalEvent{
		{ID: 3, SignalID: id, From: "approved", To: "done", Reason: "成交", Actor: "exec", At: now.Add(2 * time.Second)},
		{ID: 1, SignalID: id, From: "", To: "pending", Reason: "创建", Actor: "strategy", At: now},
		{ID: 2, SignalID: id, From: "pending", To: "approved", Reason: "过审", Actor: "risk", At: now.Add(time.Second)},
		{ID: 9, SignalID: id + 100, From: "", To: "pending", Reason: "别的信号", Actor: "strategy", At: now},
	}
	list, err := e.uc.SignalEvents(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("expected 3 events, got %d", len(list))
	}
	for i, want := range []int{1, 2, 3} {
		if list[i].ID != want {
			t.Fatalf("event %d: expected ID %d, got %d", i, want, list[i].ID)
		}
	}
	if list[0].From != "" || list[0].To != "pending" {
		t.Fatalf("create event: got from=%q to=%q", list[0].From, list[0].To)
	}
}

func TestSignalEventsEmpty(t *testing.T) {
	e := newEnv(t, nil, nil)
	id := e.signals.put(Signal{Book: "live", Symbol: "600000", Side: "buy", Status: StatusPending})
	list, err := e.uc.SignalEvents(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("expected empty history, got %d", len(list))
	}
}

func TestMissFunnelDefaultsAndCap(t *testing.T) {
	e := newEnv(t, nil, nil)
	if _, err := e.uc.MissFunnel(context.Background(), 0, ""); err != nil {
		t.Fatal(err)
	}
	today := tradeDay(time.Now())
	if !e.cands.mfTo.Equal(today) {
		t.Fatalf("expected to=%v, got %v", today, e.cands.mfTo)
	}
	if want := today.AddDate(0, 0, -(missFunnelDefaultDays - 1)); !e.cands.mfFrom.Equal(want) {
		t.Fatalf("default days: expected from=%v, got %v", want, e.cands.mfFrom)
	}
	if _, err := e.uc.MissFunnel(context.Background(), 99, ""); err != nil {
		t.Fatal(err)
	}
	if want := today.AddDate(0, 0, -(missFunnelMaxDays - 1)); !e.cands.mfFrom.Equal(want) {
		t.Fatalf("cap days: expected from=%v, got %v", want, e.cands.mfFrom)
	}
}

func TestMissFunnelAggregatesAndFilters(t *testing.T) {
	e := newEnv(t, nil, nil)
	today := tradeDay(time.Now())
	yesterday := today.AddDate(0, 0, -1)
	old := today.AddDate(0, 0, -40) // 超出默认窗口
	seed := []Candidate{
		{TradeDate: today, Strategy: "s", Symbol: "600000", Pool: "intraday", MissReason: "score_low"},
		{TradeDate: today, Strategy: "s", Symbol: "600001", Pool: "intraday", MissReason: "score_low"},
		{TradeDate: today, Strategy: "s", Symbol: "600002", Pool: "intraday", MissReason: "risk_block"},
		{TradeDate: today, Strategy: "s", Symbol: "600003", Pool: "intraday"},
		{TradeDate: today, Strategy: "s", Symbol: "600004", Pool: "swing", MissReason: "score_low"},
		{TradeDate: yesterday, Strategy: "s", Symbol: "600005", Pool: "intraday", MissReason: "limit_up"},
		{TradeDate: old, Strategy: "s", Symbol: "600006", Pool: "intraday", MissReason: "score_low"},
	}
	if err := e.cands.Save(context.Background(), seed); err != nil {
		t.Fatal(err)
	}
	rows, err := e.uc.MissFunnel(context.Background(), 7, "intraday")
	if err != nil {
		t.Fatal(err)
	}
	if e.cands.mfPool != "intraday" {
		t.Fatalf("pool not passed through: %q", e.cands.mfPool)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 days (old day out of window), got %d", len(rows))
	}
	if !rows[0].Day.Equal(yesterday) || !rows[1].Day.Equal(today) {
		t.Fatalf("expected ascending days, got %v then %v", rows[0].Day, rows[1].Day)
	}
	got := rows[1]
	if got.Total != 4 || got.Missed != 3 {
		t.Fatalf("today: expected total=4 missed=3, got total=%d missed=%d", got.Total, got.Missed)
	}
	if got.Reasons["score_low"] != 2 || got.Reasons["risk_block"] != 1 {
		t.Fatalf("reasons mismatch: %v", got.Reasons)
	}
	if rows[0].Total != 1 || rows[0].Reasons["limit_up"] != 1 {
		t.Fatalf("yesterday mismatch: %+v", rows[0])
	}
}

func TestMissFunnelNoCandidates(t *testing.T) {
	e := newEnv(t, nil, nil)
	rows, err := e.uc.MissFunnel(context.Background(), 7, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected no days, got %d", len(rows))
	}
}
