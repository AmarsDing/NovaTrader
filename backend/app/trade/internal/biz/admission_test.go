package biz

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/log"

	"server/pkg/admission"
	"server/pkg/tradecal"
)

// syntheticAdmissionStore exists only in tests. Production repositories reject
// historical writes; these fixtures exercise the state machine, not admission.
type syntheticAdmissionStore struct {
	*MemStore
	samples []admission.Sample
}

func (*syntheticAdmissionStore) AdmissionSlotExists(context.Context, time.Time) (bool, error) {
	return false, nil
}
func (*syntheticAdmissionStore) CollectAdmissionFacts(context.Context, time.Time) (AdmissionFacts, error) {
	panic("test fixture has no production collector")
}
func (*syntheticAdmissionStore) SaveAdmissionSample(context.Context, admission.Sample) error {
	panic("test fixtures must never be persisted")
}
func (s *syntheticAdmissionStore) ReadAdmissionSamples(context.Context, time.Time) ([]admission.Sample, error) {
	return s.samples, nil
}

func withSyntheticAdmission(t *testing.T, e *Engine, mem *MemStore) {
	t.Helper()
	pointer := func(v bool) *bool { return &v }
	integer := func(v int) *int { return &v }
	now := time.Now().In(tradecal.Shanghai())
	d := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, tradecal.Shanghai())
	open, err := tradecal.Default.Open(d)
	if err != nil {
		t.Fatal(err)
	}
	if !open || now.Before(d.Add(15*time.Hour+10*time.Minute)) {
		d, err = tradecal.Default.PrevOpen(d)
		if err != nil {
			t.Fatal(err)
		}
	}
	store := &syntheticAdmissionStore{MemStore: mem}
	add := func(at time.Time) {
		s := admission.Sample{
			Version: admission.Version, Book: "paper", AccountID: "default", InstanceID: "synthetic-state-machine-only", ClockSource: "server_wall_clock",
			ObservedAt: at, EndedAt: at.Add(time.Millisecond), AccountSnapshotID: "synthetic-snapshot", AccountGeneration: 1, AccountAsOf: &at,
			AccountTradingDay: at.Format("2006-01-02"), DayStartKnown: pointer(true), LedgerMatched: pointer(true), Reconciliation: "matched", ReconciliationAsOf: &at,
			RiskReady: pointer(true), RiskMode: "L1", KillActive: pointer(false), ConfigReady: pointer(true), RiskConfigRevision: integer(1), StoredConfigRevision: integer(1), RiskAccountAsOf: &at,
			UnresolvedIntents: integer(0), UnknownOrders: integer(0), OutboxKnown: true, OutboxDead: integer(0),
			UniverseKnown: true, UniverseCount: integer(5000), UniverseBasis: "synthetic-only", MarketRows: integer(5000), FreshRows: integer(5000), ClosingRows: integer(5000),
			MarketOldestSourceTime: &at, MarketSources: map[string]int{"synthetic": 5000}, MarketSourceHealthy: pointer(true), CollectionErrors: []string{},
		}
		s.Seal()
		store.samples = append(store.samples, s)
	}
	for range admission.MinRealTradingDays {
		for at := d; at.Before(d.AddDate(0, 0, 1)); at = at.Add(admission.SlotWidth) {
			session, err := tradecal.Default.SessionAt(at)
			if err != nil {
				t.Fatal(err)
			}
			if session.Trading {
				add(at.Add(time.Second))
			}
		}
		add(d.Add(15*time.Hour + 9*time.Minute + 31*time.Second))
		d, err = tradecal.Default.PrevOpen(d)
		if err != nil {
			t.Fatal(err)
		}
	}
	e.store = store
}

func TestOldPaperDaysCannotSatisfyOperationEvidence(t *testing.T) {
	e, mem := newEngine(t, allow{})
	e.set.LiveEnabled, e.set.BrokerConnected = true, true // Isolated state-machine fixture only.
	if err := mem.Commit(context.Background(), Batch{Cash: &Cash{Book: BookPaper, Cash: 1_000_000, PaperDays: 999}}); err != nil {
		t.Fatal(err)
	}
	o, err := e.Place(context.Background(), PlaceRequest{ClientOrderID: "old-days", Account: "LIVE", Symbol: "600519.SH", Side: "buy", Price: 10, Volume: 100})
	if err != nil || o.Status != StatusRejected || !strings.Contains(o.Reason, "旧paper_days") {
		t.Fatalf("legacy counter bypassed actual evidence: %+v err=%v", o, err)
	}
}

func TestSyntheticAdmissionCannotOpenDisconnectedBroker(t *testing.T) {
	e, mem := newEngine(t, allow{})
	withSyntheticAdmission(t, e, mem)
	e.set.LiveEnabled = true
	r, err := e.PaperAdmission(context.Background(), "SIM", 21)
	if err != nil || !r.RunEvidenceReady || r.LiveEnabled {
		t.Fatalf("synthetic fixture failed: %+v err=%v", r, err)
	}
	o, err := e.Place(context.Background(), PlaceRequest{ClientOrderID: "no-broker", Account: "LIVE", Symbol: "600519.SH", Side: "buy", Price: 10, Volume: 100})
	if err != nil || o.Status != StatusRejected || !strings.Contains(o.Reason, "柜台通道") {
		t.Fatalf("operation evidence bypassed broker connection: %+v err=%v", o, err)
	}
}

// ---------------------------------------------------------------------------
// M13-T05 LIVE 放行总闸：Grant 须先审计留痕+运行证据就绪，先持久化后翻闸；
// Revoke 对称；重启状态由持久留痕恢复（fake repo 持久层单测）。

// fakeLiveGrantStore 是留痕/审计端口的内存夹具：raw 模拟 strategy_config 键值，
// audits 模拟 admin 网关 audit_log 的最近成功留痕。
var errSave = errors.New("live grant persist failed")

type fakeLiveGrantStore struct {
	*syntheticAdmissionStore
	raw     string
	hasRaw  bool
	saves   int
	clears  int
	saveErr error
	audits  map[string]bool
}

func (f *fakeLiveGrantStore) LoadLiveGrant(context.Context) (LiveGrant, bool, error) {
	if !f.hasRaw {
		return LiveGrant{}, false, nil
	}
	g, err := ParseLiveGrant(f.raw)
	if err != nil {
		return LiveGrant{}, false, err
	}
	return g, true, nil
}
func (f *fakeLiveGrantStore) SaveLiveGrant(_ context.Context, g LiveGrant) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	raw, err := MarshalLiveGrant(g)
	if err != nil {
		return err
	}
	f.raw, f.hasRaw, f.saves = raw, true, f.saves+1
	return nil
}
func (f *fakeLiveGrantStore) ClearLiveGrant(context.Context) error {
	f.raw, f.hasRaw, f.clears = "", false, f.clears+1
	return nil
}
func (f *fakeLiveGrantStore) RecentLiveGateAudit(_ context.Context, action, actor string, _ time.Duration) (bool, error) {
	return f.audits[action+"|"+actor], nil
}

func newLiveGateEngine(t *testing.T) (*Engine, *fakeLiveGrantStore) {
	t.Helper()
	e, mem := newEngine(t, allow{})
	withSyntheticAdmission(t, e, mem) // 连续≥20 真实交易日证据就绪（合成夹具）
	store := &fakeLiveGrantStore{syntheticAdmissionStore: e.store.(*syntheticAdmissionStore), audits: map[string]bool{}}
	e.store = store
	return e, store
}

func TestGrantLiveRejectsWithoutAuditAndEvidence(t *testing.T) {
	ctx := context.Background()
	// 未达准入（无任何运行证据）即使有审计也拒绝；达准入但无审计留痕同样拒绝；
	// 两者都不得写留痕、不得翻闸门。
	e, mem := newEngine(t, allow{})
	bare := &fakeLiveGrantStore{syntheticAdmissionStore: &syntheticAdmissionStore{MemStore: mem}, audits: map[string]bool{"trade.grant_live|owner": true}}
	e.store = bare
	if _, err := e.GrantLive(ctx, "owner"); err == nil || bare.saves != 0 || e.LiveEnabled() {
		t.Fatalf("grant without evidence must stay closed: err=%v saves=%d", err, bare.saves)
	}
	e2, store := newLiveGateEngine(t)
	if _, err := e2.GrantLive(ctx, "owner"); err == nil || store.saves != 0 || e2.LiveEnabled() {
		t.Fatalf("grant without audit trail must stay closed: err=%v saves=%d", err, store.saves)
	}
	if _, err := e2.GrantLive(ctx, "  "); err == nil || store.saves != 0 {
		t.Fatalf("blank operator must be rejected: err=%v", err)
	}
}

func TestGrantLivePersistsBeforeFlipAndSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	e, store := newLiveGateEngine(t)
	store.audits["trade.grant_live|owner"] = true
	// 钉死顺序：GrantLive 返回后留痕已落盘，闸门才被翻开（先留痕后生效）。
	report, err := e.GrantLive(ctx, "owner")
	if err != nil || !report.RunEvidenceReady || !report.LiveEnabled {
		t.Fatalf("grant with evidence+audit must succeed: %+v err=%v", report, err)
	}
	if !e.LiveEnabled() || store.saves != 1 || !store.hasRaw {
		t.Fatalf("grant must flip gate after persisting: saves=%d has=%v", store.saves, store.hasRaw)
	}
	grant, err := ParseLiveGrant(store.raw)
	if err != nil || grant.GrantedBy != "owner" || grant.GrantedAt.IsZero() || grant.Evidence.ConsecutiveDays < admission.MinRealTradingDays || grant.Evidence.LatestClosedDay == "" {
		t.Fatalf("persisted grant must carry audit fields: %+v err=%v", grant, err)
	}
	// 重启恢复：同一份留痕经统一判定重新开门（生产路径 = data.NewSettings 映射）。
	if !LiveGateFromGrant(grant, true, nil) || LiveGateFromGrant(LiveGrant{}, false, nil) {
		t.Fatal("restore predicate must open on persisted grant only")
	}
	e2 := NewEngine(store, allow{}, Settings{InitialCash: 1_000_000, PaperDaysRequired: 20, LiveEnabled: LiveGateFromGrant(grant, true, nil)}, log.DefaultLogger)
	if !e2.LiveEnabled() {
		t.Fatal("restarted engine must restore the gate from the persisted grant")
	}
	// 撤权须先审计；清除留痕成功后才关闸门（与放行同一份先留痕后翻闸纪律）。
	if err := e2.RevokeLive(ctx, "owner"); err == nil {
		t.Fatal("revoke without audit must be rejected")
	}
	store.audits["trade.revoke_live|owner"] = true
	if err := e2.RevokeLive(ctx, "owner"); err != nil || e2.LiveEnabled() || store.clears != 1 || store.hasRaw {
		t.Fatalf("audited revoke must clear persistence then close: err=%v clears=%d has=%v", err, store.clears, store.hasRaw)
	}
	if LiveGateFromGrant(LiveGrant{}, false, nil) {
		t.Fatal("cleared persistence must restore as closed")
	}
}

func TestGrantLiveFailsClosedOnPersistError(t *testing.T) {
	ctx := context.Background()
	e, store := newLiveGateEngine(t)
	store.audits["trade.grant_live|owner"] = true
	store.saveErr = errSave
	if _, err := e.GrantLive(ctx, "owner"); err == nil || e.LiveEnabled() {
		t.Fatalf("persist failure must not flip the gate: err=%v", err)
	}
}
