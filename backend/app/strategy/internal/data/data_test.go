package data

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"

	"server/app/strategy/internal/biz"
	"server/conf"
	"server/ent/outbox"
	"server/ent/signalevent"
	"server/pkg/events"
	"server/pkg/rules"
	"server/pkg/tradecal"

	"github.com/go-kratos/kratos/v2/log"
)

var ctx = context.Background()

func newTestData(t *testing.T) *Data {
	t.Helper()
	d, cleanup, err := NewData(&conf.Postgres{
		Host: "127.0.0.1", Port: 5432, User: "postgres", Password: "postgres",
		Database: "novatrader_strategytest", SslMode: "disable",
	}, &conf.Redis{}, log.DefaultLogger)
	if err != nil {
		t.Skip(err)
	}
	t.Cleanup(cleanup)
	c := d.client
	for _, del := range []func() error{
		func() error { _, err := c.SignalEvent.Delete().Exec(ctx); return err },
		func() error { _, err := c.TradeSignal.Delete().Exec(ctx); return err },
		func() error { _, err := c.StrategyCandidate.Delete().Exec(ctx); return err },
		func() error { _, err := c.StockBlacklist.Delete().Exec(ctx); return err },
		func() error { _, err := c.Outbox.Delete().Exec(ctx); return err },
		func() error { _, err := c.MarketData.Delete().Exec(ctx); return err },
		func() error { _, err := c.StockBasic.Delete().Exec(ctx); return err },
		func() error { _, err := c.Position.Delete().Exec(ctx); return err },
	} {
		if err := del(); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func envFor(s *biz.Signal, from biz.Status) (events.Envelope, error) {
	return events.New("strategy", events.SubjectSignal, s.TraceID, map[string]any{"signal_id": s.ID, "status": s.Status, "from": from})
}

func TestSignalRepo(t *testing.T) {
	d := newTestData(t)
	repo := NewSignalRepo(d)
	day := time.Date(2026, 10, 9, 0, 0, 0, 0, tradecal.Shanghai())
	at := day.Add(10 * time.Hour)
	ai := 80.0
	ver := 3
	sig := &biz.Signal{
		Book: "paper", Time: at, TradeDate: day, Symbol: "600000.SH", Name: "浦发银行", Side: rules.SideBuy,
		Strategy: rules.NameFirstBoard, VersionID: &ver, Entry: 11.45, EntryLow: 11.39, EntryHigh: 11.51,
		StopLoss: 10.94, TakeProfit: 12.1, ATR: 0.2571, PositionPct: 0.03, ValidUntil: at.Add(30 * time.Minute),
		HoldDaysMax: 2, RuleScore: 86.7, AIScore: &ai, FinalScore: 82.68, Dims: map[string]float64{"technical": 80},
		Evidence: []string{"F:m06_rule_score"}, Reason: "首板弱转强", Status: biz.StatusPending, TraceID: "t-1",
	}
	id, err := repo.Create(ctx, sig, envFor)
	if err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Strategy != sig.Strategy || got.EntryLow != 11.39 || got.ATR != 0.2571 || got.PositionPct != 0.03 ||
		*got.AIScore != 80 || *got.VersionID != 3 || got.Dims["technical"] != 80 || got.Evidence[0] != "F:m06_rule_score" ||
		!got.TradeDate.Equal(day) || !got.ValidUntil.Equal(sig.ValidUntil) || got.HoldDaysMax != 2 || got.TraceID != "t-1" {
		t.Fatalf("roundtrip %+v", got)
	}
	if n, _ := repo.CountBuys(ctx, "paper", day); n != 1 {
		t.Fatalf("count %d", n)
	}
	if open, _ := repo.HasOpen(ctx, "paper", "600000.SH", rules.SideBuy); !open {
		t.Fatal("should be open")
	}

	if _, err := repo.Transition(ctx, id, biz.StatusApproved, biz.StatusExecuting, "", "trade", "t-1", envFor, nil); !errors.Is(err, biz.ErrConflict) {
		t.Fatalf("stale from err=%v", err)
	}
	for _, step := range [][2]biz.Status{
		{biz.StatusPending, biz.StatusRiskChecking}, {biz.StatusRiskChecking, biz.StatusApproved},
		{biz.StatusApproved, biz.StatusExecuting}, {biz.StatusExecuting, biz.StatusDone},
	} {
		if _, err := repo.Transition(ctx, id, step[0], step[1], "ok", "risk", "t-1", envFor, nil); err != nil {
			t.Fatal(err)
		}
	}
	evs, err := d.client.SignalEvent.Query().Where(signalevent.SignalID(id)).Order(signalevent.ByID()).All(ctx)
	if err != nil || len(evs) != 5 || evs[0].FromStatus != "" || evs[4].ToStatus != "done" {
		t.Fatalf("signal events %v err=%v", evs, err)
	}
	if n, _ := d.client.Outbox.Query().Where(outbox.Subject(events.SubjectSignal)).Count(ctx); n != 5 {
		t.Fatalf("outbox %d", n)
	}
	last, _ := d.client.Outbox.Query().Order(outbox.ByID()).All(ctx)
	var p map[string]any
	_ = json.Unmarshal(last[4].Payload, &p)
	if p["status"] != "done" || p["from"] != "executing" {
		t.Fatalf("payload %v", p)
	}
	if b, _ := repo.LastDoneBuy(ctx, "paper", "600000.SH"); b == nil || b.ID != id {
		t.Fatalf("last done %v", b)
	}
	exit, pnl, days := 12.0, 4.8, 1
	closed, err := repo.Transition(ctx, id, biz.StatusDone, biz.StatusClosed, "仓位归零", "trade", "t-1", envFor, &biz.SignalPatch{
		ExitPrice: &exit, PnlPct: &pnl, ClosedAt: &at, HoldingDays: &days,
	})
	if err != nil {
		t.Fatal(err)
	}
	if closed.ExitPrice == nil || *closed.ExitPrice != 12 || closed.PnlPct == nil || *closed.PnlPct != 4.8 ||
		closed.HoldingDays == nil || *closed.HoldingDays != 1 || !closed.ClosedAt.Equal(at) {
		t.Fatalf("closed %+v", closed)
	}
	if b, _ := repo.LastDoneBuy(ctx, "paper", "600000.SH"); b != nil {
		t.Fatalf("closed buy is not an open position: %+v", b)
	}

	exp := *sig
	exp.Status, exp.ValidUntil = biz.StatusPending, at.Add(-time.Minute)
	expID, err := repo.Create(ctx, &exp, envFor)
	if err != nil {
		t.Fatal(err)
	}
	due, err := repo.Due(ctx, at)
	if err != nil || len(due) != 1 || due[0].ID != expID {
		t.Fatalf("due %v err=%v", due, err)
	}
	list, err := repo.List(ctx, biz.SignalFilter{Book: "paper", Day: day, Limit: 10})
	if err != nil || len(list) != 2 || list[0].ID != expID {
		t.Fatalf("list %v err=%v", list, err)
	}
	if list, _ := repo.List(ctx, biz.SignalFilter{Status: biz.StatusClosed, Limit: 10}); len(list) != 1 {
		t.Fatalf("status filter %d", len(list))
	}
}

func TestCandidateRepo(t *testing.T) {
	d := newTestData(t)
	repo := NewCandidateRepo(d)
	day := time.Date(2026, 10, 9, 0, 0, 0, 0, tradecal.Shanghai())
	ai, final, sid := 70.0, 80.0, 42
	base := biz.Candidate{TradeDate: day, Strategy: "s", Symbol: "600000.SH", Name: "浦发银行", Pool: biz.PoolPre,
		Stage: biz.StageRanked, RuleScore: 86, RefPrice: 11}
	if err := repo.Save(ctx, []biz.Candidate{base}); err != nil {
		t.Fatal(err)
	}
	sel := base
	sel.Pool, sel.Stage, sel.AIScore, sel.FinalScore, sel.SignalID, sel.RefPrice = biz.PoolIntraday, biz.StageSelected, &ai, &final, &sid, 11.45
	if err := repo.Save(ctx, []biz.Candidate{sel}); err != nil {
		t.Fatal(err)
	}
	later := base
	later.MissReason = biz.MissCooldown
	if err := repo.Save(ctx, []biz.Candidate{later}); err != nil {
		t.Fatal(err)
	}
	list, err := repo.List(ctx, biz.CandidateFilter{Day: day, Limit: 10})
	if err != nil || len(list) != 1 {
		t.Fatalf("list %v err=%v", list, err)
	}
	c := list[0]
	if c.Stage != biz.StageSelected || c.MissReason != "" || *c.SignalID != 42 || c.RefPrice != 11.45 || c.Pool != biz.PoolPre {
		t.Fatalf("candidate %+v", c)
	}
	if syms, _ := repo.PoolSymbols(ctx, day, biz.PoolPre); len(syms) != 1 {
		t.Fatalf("pool %v", syms)
	}
	pend, err := repo.PendingOutcomes(ctx, day.AddDate(0, 0, -30), day.AddDate(0, 0, 1))
	if err != nil || len(pend) != 1 {
		t.Fatalf("pending %v err=%v", pend, err)
	}
	t5 := 0.1
	if err := repo.SetOutcome(ctx, c.ID, nil, nil, &t5); err != nil {
		t.Fatal(err)
	}
	if pend, _ := repo.PendingOutcomes(ctx, day.AddDate(0, 0, -30), day.AddDate(0, 0, 1)); len(pend) != 0 {
		t.Fatalf("still pending %v", pend)
	}
}

func TestBlacklistAndPositions(t *testing.T) {
	d := newTestData(t)
	bl := NewBlacklistRepo(d)
	now := time.Now()
	past := now.Add(-time.Hour)
	if err := bl.Add(ctx, "600000.SH", "立案", "user", nil); err != nil {
		t.Fatal(err)
	}
	if err := bl.Add(ctx, "000001.SZ", "临时", "user", &past); err != nil {
		t.Fatal(err)
	}
	if err := bl.Add(ctx, "600000.SH", "立案调查", "user", nil); err != nil {
		t.Fatal(err)
	}
	act, err := bl.Active(ctx, now)
	if err != nil || len(act) != 1 || !act["600000.SH"] {
		t.Fatalf("active %v err=%v", act, err)
	}
	if err := bl.Remove(ctx, "600000.SH"); err != nil {
		t.Fatal(err)
	}
	if act, _ := bl.Active(ctx, now); len(act) != 0 {
		t.Fatalf("after remove %v", act)
	}

	cost := 12.0
	d.client.Position.Create().SetBook("paper").SetSymbol("600000.SH").SetQuantity(1000).SetAvailable(500).SetAvgCost(cost).ExecX(ctx)
	d.client.Position.Create().SetBook("paper").SetSymbol("000001.SZ").SetQuantity(0).ExecX(ctx)
	d.client.Position.Create().SetBook("live").SetSymbol("600519.SH").SetQuantity(100).ExecX(ctx)
	hs, err := NewPositionRepo(d).Holdings(ctx, "paper")
	if err != nil || len(hs) != 1 || hs[0].Available != 500 || hs[0].AvgCost != 12 {
		t.Fatalf("holdings %v err=%v", hs, err)
	}
}

func TestMarketSource(t *testing.T) {
	d := newTestData(t)
	sh := tradecal.Shanghai()
	asOf := time.Date(2026, 10, 9, 10, 0, 0, 0, sh)
	list := time.Date(2024, 10, 9, 0, 0, 0, 0, sh)
	d.client.StockBasic.Create().SetStockCode("600000.SH").SetStockName("浦发银行").SetMarket("SH").SetListDate(list).ExecX(ctx)
	d.client.StockBasic.Create().SetStockCode("600001.SH").SetStockName("已退市").SetMarket("SH").
		SetListDate(list).SetDelistDate(asOf.AddDate(0, -1, 0)).ExecX(ctx)

	// 三根日线，第三根除权：不复权价腰斩，后复权因子翻倍。
	bars := []struct {
		day   time.Time
		close float64
		adj   float64
	}{
		{time.Date(2026, 9, 29, 0, 0, 0, 0, sh), 20, 1},
		{time.Date(2026, 9, 30, 0, 0, 0, 0, sh), 22, 1},
		{time.Date(2026, 10, 9, 0, 0, 0, 0, sh), 11.5, 2}, // asOf 当天，不能进 Bars
	}
	for _, b := range bars {
		d.client.MarketData.Create().SetSymbol("600000.SH").SetFreq("1d").SetBarTime(b.day).
			SetOpen(b.close).SetHigh(b.close).SetLow(b.close).SetClose(b.close).SetVolume(1000).SetAmount(1e8).
			SetAdjFactor(b.adj).ExecX(ctx)
	}
	ms := NewMarketSource(d, log.DefaultLogger)
	snaps, err := ms.Snapshots(ctx, []string{"600000.SH", "600001.SH", "999999.SH"}, asOf)
	if err != nil || len(snaps) != 1 {
		t.Fatalf("snaps %v err=%v", snaps, err)
	}
	s := snaps[0]
	if len(s.Bars) != 2 || s.Bars[1].Close != 22 || s.Today != nil || s.Stage != rules.StageWarm || !s.ListDate.Equal(list) {
		t.Fatalf("snapshot %+v", s)
	}
	uni, err := ms.Universe(ctx, asOf)
	if err != nil || len(uni) != 1 {
		t.Fatalf("universe %d err=%v", len(uni), err)
	}

	closes, err := ms.DailyCloses(ctx, "600000.SH", bars[2].day, bars[2].day)
	if err != nil {
		t.Fatal(err)
	}
	// 以 9/30 为基准：11.5 × 2 / 1 = 23，相对 22 是上涨。
	if px := closes["2026-10-09"]; math.Abs(px-23) > 1e-9 {
		t.Fatalf("adjusted close %v", closes)
	}
}
