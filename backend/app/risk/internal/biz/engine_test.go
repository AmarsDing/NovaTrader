package biz

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"server/pkg/risk"
	"server/pkg/tradecal"

	"github.com/go-kratos/kratos/v2/log"
)

type fakeRepo struct {
	mu       sync.Mutex
	params   map[string]string
	kill     risk.KillState
	mode     risk.Mode
	haveMode bool
	failKill bool
	alerts   []string
	day      DayState
	kills    []risk.KillState
	modes    []risk.Mode
	breakers []string
}

func (f *fakeRepo) LoadParams(context.Context) (map[string]string, error) { return f.params, nil }
func (f *fakeRepo) SaveParam(_ context.Context, key, value, _, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.params == nil {
		f.params = map[string]string{}
	}
	f.params[key] = value
	return nil
}
func (f *fakeRepo) LastKill(context.Context) (risk.KillState, error) { return f.kill, nil }
func (f *fakeRepo) LastMode(context.Context) (risk.Mode, bool, error) {
	return f.mode, f.haveMode, nil
}
func (f *fakeRepo) SaveKill(_ context.Context, k risk.KillState, m risk.Mode) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failKill {
		return errors.New("db down")
	}
	f.kill, f.mode, f.haveMode = k, m, true
	f.kills = append(f.kills, k)
	return nil
}
func (f *fakeRepo) SaveMode(_ context.Context, m risk.Mode, _, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mode, f.haveMode = m, true
	f.modes = append(f.modes, m)
	return nil
}
func (f *fakeRepo) SaveAlert(_ context.Context, message string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.alerts = append(f.alerts, message)
	return nil
}
func (f *fakeRepo) SaveBreaker(_ context.Context, _ risk.AccountType, _ bool, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.breakers = append(f.breakers, reason)
	return nil
}
func (f *fakeRepo) Report(context.Context, time.Time, time.Time) (*Report, error) {
	return &Report{}, nil
}
func (f *fakeRepo) RestoreDay(context.Context, time.Time) (DayState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.day, nil
}
func (f *fakeRepo) RefData(context.Context, time.Time) (map[string]RefInfo, error) {
	return map[string]RefInfo{"000001.SZ": {PrevAmount: 3e8, ListDate: time.Date(1991, 4, 3, 0, 0, 0, 0, time.UTC)}}, nil
}

type fakeQuotes struct{ m map[string]Snapshot }

func (f *fakeQuotes) Quotes(_ context.Context, syms []string) (map[string]Snapshot, error) {
	out := map[string]Snapshot{}
	for _, s := range syms {
		if q, ok := f.m[s]; ok {
			out[s] = q
		}
	}
	return out, nil
}

type fakeAudit struct {
	full bool
	n    int
}

func (f *fakeAudit) Submit(AuditRecord) bool {
	if f.full {
		return false
	}
	f.n++
	return true
}

type fakePub struct {
	mu   sync.Mutex
	msgs []any
}

func (f *fakePub) Publish(_ context.Context, _ string, p any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.msgs = append(f.msgs, p)
	return nil
}

var t0 = time.Date(2026, 10, 9, 10, 0, 0, 0, tradecal.Shanghai())

type rig struct {
	e     *Engine
	repo  *fakeRepo
	q     *fakeQuotes
	audit *fakeAudit
	pub   *fakePub
	clock time.Time
}

func newRig(t *testing.T, repo *fakeRepo) *rig {
	t.Helper()
	if repo == nil {
		repo = &fakeRepo{mode: risk.L1, haveMode: true}
	}
	r := &rig{repo: repo, audit: &fakeAudit{}, pub: &fakePub{}, clock: t0}
	r.q = &fakeQuotes{m: map[string]Snapshot{"000001.SZ": r.snap(10.00)}}
	r.e = NewEngine(repo, r.q, r.audit, r.pub, log.DefaultLogger)
	r.e.now = func() time.Time { return r.clock }
	if err := r.e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.e.OnMarketState(context.Background(), StatePayload{Phase: "HOT", Position: 1, AsOf: t0})
	return r
}

func (r *rig) snap(last float64) Snapshot {
	return Snapshot{Symbol: "000001.SZ", Time: r.clock, AsOf: r.clock, PreClose: 10, Last: last, Bid1: last - 0.01, Ask1: last, Amount: 3e8}
}

func (r *rig) account(equity, avail float64, holdings ...risk.Holding) {
	r.e.OnAccount(context.Background(), AccountPayload{
		AccountType: risk.SIM, Equity: equity, Available: avail, DayStartEquity: 1_000_000,
		Holdings: holdings, AsOf: r.clock,
	})
}

func buy(id string, vol int) risk.Order {
	return risk.Order{ClientID: id, Account: risk.SIM, Symbol: "000001.SZ", Side: risk.Buy, Price: 10, Volume: vol, Source: risk.Auto, Sector: "银行"}
}

func TestReservationPreventsDoubleSpend(t *testing.T) {
	r := newRig(t, nil)
	r.account(1_000_000, 15_000)
	ctx := context.Background()
	d1, _ := r.e.Check(ctx, buy("a", 1000))
	if !d1.Approved || d1.Volume != 1000 {
		t.Fatal(d1.Reason)
	}
	d2, _ := r.e.Check(ctx, buy("b", 1000))
	if !d2.Approved || d2.Volume != 400 {
		t.Fatalf("second order should only get the remaining cash: %+v", d2)
	}
	// 快照晚于放行 2 秒以上，预留清掉，以快照里的可用资金为准。
	r.clock = r.clock.Add(3 * time.Second)
	r.q.m["000001.SZ"] = r.snap(10)
	r.account(1_000_000, 15_000)
	if d3, _ := r.e.Check(ctx, buy("c", 1000)); d3.Volume != 1000 {
		t.Fatal(d3.Volume)
	}
	if r.audit.n != 3 {
		t.Fatal("every check is audited", r.audit.n)
	}
}

func TestKillSwitchSurvivesRestart(t *testing.T) {
	repo := &fakeRepo{mode: risk.L1, haveMode: true}
	r := newRig(t, repo)
	ctx := context.Background()
	if _, err := r.e.TriggerKill(ctx, risk.KillFromClient, "test", ""); err == nil {
		t.Fatal("client trigger needs operator")
	}
	st, err := r.e.TriggerKill(ctx, risk.KillFromClient, "test", "owner")
	if err != nil || !st.Kill.Active || st.Mode != risk.L0 {
		t.Fatal(err, st)
	}
	// 重启：从库里恢复。
	r2 := newRig(t, repo)
	r2.account(1_000_000, 500_000)
	if d, _ := r2.e.Check(ctx, buy("x", 100)); d.Approved || d.Rule != "R01" {
		t.Fatal("kill switch must survive restart", d)
	}
	if _, err := r2.e.ResetKill(ctx, "owner", "yes", ""); err == nil {
		t.Fatal("needs CONFIRM")
	}
	st, err = r2.e.ResetKill(ctx, "owner", risk.ResetConfirm, "checked")
	if err != nil || st.Kill.Active || st.Mode != risk.L0 {
		t.Fatal(err, st)
	}
	if _, err := r2.e.SetMode(ctx, "L1", "owner", ""); err != nil {
		t.Fatal(err)
	}
	if d, _ := r2.e.Check(ctx, buy("y", 100)); !d.Approved {
		t.Fatal(d.Reason)
	}
}

func TestKillSaveRetried(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	r.repo.failKill = true
	st, err := r.e.TriggerKill(ctx, risk.KillFromTerminal, "终端失联", "")
	if err == nil || !st.Kill.Active {
		t.Fatal("must stop in memory and report the save error", err)
	}
	r.account(1_000_000, 500_000)
	if d, _ := r.e.Check(ctx, buy("a", 100)); d.Rule != "R01" {
		t.Fatal(d.Rule)
	}
	r.repo.failKill = false
	r.e.Tick(ctx)
	if len(r.repo.kills) != 1 || !r.repo.kills[0].Active || r.repo.kills[0].Source != risk.KillFromTerminal {
		t.Fatal(r.repo.kills)
	}
	r.e.Tick(ctx)
	if len(r.repo.kills) != 1 {
		t.Fatal("saved once")
	}
}

func TestNotReadyRejects(t *testing.T) {
	repo := &fakeRepo{}
	e := NewEngine(repo, &fakeQuotes{m: map[string]Snapshot{}}, &fakeAudit{}, &fakePub{}, log.DefaultLogger)
	o := buy("a", 100)
	o.Side, o.Source, o.Operator = risk.Sell, risk.Manual, "owner"
	if d, _ := e.Check(context.Background(), o); d.Approved || d.Rule != risk.RuleInvalid {
		t.Fatal(d)
	}
}

func TestDailyLossLatches(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	r.account(979_000, 500_000)
	if d, _ := r.e.Check(ctx, buy("a", 100)); d.Rule != "R15" {
		t.Fatal(d.Rule, d.Reason)
	}
	// 回本也保持到收盘。
	r.clock = r.clock.Add(time.Minute)
	r.q.m["000001.SZ"] = r.snap(10)
	r.account(1_000_000, 500_000)
	if d, _ := r.e.Check(ctx, buy("b", 100)); d.Rule != "R15" {
		t.Fatal("latched", d.Rule)
	}
	if len(r.repo.breakers) != 1 {
		t.Fatal(r.repo.breakers)
	}
	// 重启后锁存和开仓笔数从库里恢复。
	restarted := newRig(t, &fakeRepo{mode: risk.L1, haveMode: true, day: DayState{
		Breakers: map[risk.AccountType]string{risk.SIM: "当日亏损 -2.10%"},
		Buys:     map[risk.AccountType]int{risk.SIM: 3},
	}})
	restarted.account(1_000_000, 500_000)
	if d, _ := restarted.e.Check(ctx, buy("r", 100)); d.Rule != "R15" {
		t.Fatal("latch must survive restart", d.Rule)
	}
	if restarted.e.State().BuysToday != 3 {
		t.Fatal(restarted.e.State().BuysToday)
	}
	// 卖出放行。
	r.account(1_000_000, 500_000, risk.Holding{Symbol: "000001.SZ", Quantity: 1000, Available: 1000, AvgCost: 10, MarketValue: 10_000})
	sell := buy("s", 1000)
	sell.Side = risk.Sell
	if d, _ := r.e.Check(ctx, sell); !d.Approved {
		t.Fatal(d.Reason)
	}
	// 下一交易日解除。
	r.clock = time.Date(2026, 10, 12, 10, 0, 0, 0, tradecal.Shanghai())
	r.q.m["000001.SZ"] = r.snap(10)
	r.account(1_000_000, 500_000)
	if d, _ := r.e.Check(ctx, buy("c", 100)); !d.Approved {
		t.Fatal(d.Reason)
	}
}

func TestMarketDownTriggersKill(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	r.account(1_000_000, 500_000, risk.Holding{Symbol: "000001.SZ", Quantity: 1000, Available: 1000, AvgCost: 10})
	r.e.OnSnapshot(ctx, SnapshotMetaPayload{})
	r.clock = r.clock.Add(10 * time.Second)
	r.e.Tick(ctx)
	if r.e.State().Kill.Active {
		t.Fatal("too early")
	}
	r.clock = r.clock.Add(31 * time.Second)
	r.e.Tick(ctx)
	st := r.e.State()
	if !st.Kill.Active || st.Kill.Source != risk.KillFromMarket || len(r.repo.kills) != 1 {
		t.Fatalf("%+v", st.Kill)
	}
}

func TestMarketHeartbeatKeepsAlive(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	r.account(1_000_000, 500_000, risk.Holding{Symbol: "000001.SZ", Quantity: 1000, Available: 1000, AvgCost: 10})
	r.e.OnSnapshot(ctx, SnapshotMetaPayload{})
	for i := 0; i < 4; i++ {
		r.clock = r.clock.Add(20 * time.Second)
		r.e.OnSnapshot(ctx, SnapshotMetaPayload{AsOf: r.clock, Count: 5000})
		r.e.Tick(ctx)
	}
	if r.e.State().Kill.Active {
		t.Fatal("held stock suspended but market alive")
	}
}

func TestNoonBreakDoesNotTriggerKill(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	r.account(1_000_000, 500_000, risk.Holding{Symbol: "000001.SZ", Quantity: 1000, Available: 1000, AvgCost: 10})
	r.clock = time.Date(2026, 10, 9, 11, 29, 0, 0, tradecal.Shanghai())
	r.q.m["000001.SZ"] = r.snap(10)
	r.e.OnSnapshot(ctx, SnapshotMetaPayload{})
	r.clock = time.Date(2026, 10, 9, 12, 30, 0, 0, tradecal.Shanghai())
	r.e.Tick(ctx)
	// 13:00 开盘后要等满 30 秒才判断。
	r.clock = time.Date(2026, 10, 9, 13, 0, 1, 0, tradecal.Shanghai())
	r.e.Tick(ctx)
	if r.e.State().Kill.Active {
		t.Fatal("afternoon open must not trip on the lunch gap")
	}
}

func TestWatchPublishesExitOnce(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	r.account(1_000_000, 500_000, risk.Holding{Symbol: "000001.SZ", Quantity: 1000, Available: 1000, AvgCost: 10, StopPrice: 9.5})
	r.q.m["000001.SZ"] = r.snap(9.40)
	r.e.OnSnapshot(ctx, SnapshotMetaPayload{})
	r.e.OnSnapshot(ctx, SnapshotMetaPayload{})
	if len(r.pub.msgs) != 1 {
		t.Fatal(len(r.pub.msgs))
	}
	ex := r.pub.msgs[0].(ExitPayload)
	if ex.Trigger != risk.ExitStopLoss || ex.Volume != 1000 || ex.ExitID == "" {
		t.Fatalf("%+v", ex)
	}
	r.clock = r.clock.Add(61 * time.Second)
	r.q.m["000001.SZ"] = r.snap(9.40)
	r.e.OnSnapshot(ctx, SnapshotMetaPayload{})
	if len(r.pub.msgs) != 2 {
		t.Fatal("cooldown is 60s")
	}
	// 利空标记触发卖出。
	r2 := newRig(t, nil)
	r2.account(1_000_000, 500_000, risk.Holding{Symbol: "000001.SZ", Quantity: 1000, Available: 1000, AvgCost: 10})
	r2.e.OnIntelAlert(IntelAlertPayload{Stocks: []string{"000001.SZ"}, Sentiment: -0.2})
	r2.e.OnSnapshot(ctx, SnapshotMetaPayload{})
	if len(r2.pub.msgs) != 0 {
		t.Fatal("mild news is not bad news")
	}
	r2.e.OnIntelAlert(IntelAlertPayload{Stocks: []string{"sz000001"}, Sentiment: -0.8, Title: "立案"})
	r2.e.OnSnapshot(ctx, SnapshotMetaPayload{})
	if len(r2.pub.msgs) != 1 || r2.pub.msgs[0].(ExitPayload).Trigger != risk.ExitBadNews {
		t.Fatal(r2.pub.msgs)
	}
}

func TestAuditFullRejects(t *testing.T) {
	r := newRig(t, nil)
	r.account(1_000_000, 500_000)
	r.audit.full = true
	d, _ := r.e.Check(context.Background(), buy("a", 100))
	if d.Approved || d.Rule != risk.RuleInvalid {
		t.Fatal(d)
	}
	if r.e.State().BuysToday != 0 {
		t.Fatal("rejected order must not be counted")
	}
}

func TestRejectRateAlert(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	for _, kv := range [][2]string{
		{"reject_alert_min", "4"},
		{"reject_alert_rate", "0.5"},
		{"reject_alert_cooldown_sec", "60"},
	} {
		if _, err := r.e.UpdateParam(ctx, kv[0], kv[1], "owner"); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		r.e.Check(ctx, buy(string(rune('a'+i)), 100))
	}
	r.e.Tick(ctx)
	if len(r.repo.alerts) != 0 {
		t.Fatal("three rejects are below the minimum")
	}
	r.e.Check(ctx, buy("d", 100))
	r.e.Tick(ctx)
	if len(r.repo.alerts) != 1 {
		t.Fatal(r.repo.alerts)
	}
	r.e.Tick(ctx)
	if len(r.repo.alerts) != 1 {
		t.Fatal("cooldown")
	}
	r.clock = r.clock.Add(61 * time.Second)
	r.e.Tick(ctx)
	if len(r.repo.alerts) != 2 {
		t.Fatal(r.repo.alerts)
	}
}

func TestParamAndMode(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	if _, err := r.e.SetMode(ctx, "L2", "owner", ""); err == nil {
		t.Fatal("L2 needs live admission")
	}
	if _, err := r.e.UpdateParam(ctx, "risk.live_admitted", "true", "owner"); err != nil {
		t.Fatal(err)
	}
	if st, err := r.e.SetMode(ctx, "L2", "owner", ""); err != nil || st.Mode != risk.L2 {
		t.Fatal(err)
	}
	st, err := r.e.UpdateParam(ctx, "live_admitted", "false", "owner")
	if err != nil || st.Mode != risk.L1 {
		t.Fatal("closing admission downgrades to L1", err, st.Mode)
	}
	if _, err := r.e.UpdateParam(ctx, "max_orders_per_sec", "500", "owner"); err == nil {
		t.Fatal("HFT line")
	}
	if r.repo.params["risk.live_admitted"] != "false" {
		t.Fatal(r.repo.params)
	}
	// 人工风险度下限 3：仓位系数 0，买单被拒。
	if _, err := r.e.UpdateParam(ctx, "risk_level", "3", "owner"); err != nil {
		t.Fatal(err)
	}
	r.account(1_000_000, 500_000)
	if d, _ := r.e.Check(ctx, buy("a", 100)); d.Approved {
		t.Fatal("risk level 3 blocks buys")
	}
}
