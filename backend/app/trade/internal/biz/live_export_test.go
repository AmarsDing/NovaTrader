package biz

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"server/pkg/brokerif/remote"
	"server/pkg/tradecal"
)

// fakeFileBroker 是 M13-T03 出口链的 mock remote：记录调用，回放预置回执。
type fakeFileBroker struct {
	mu      sync.Mutex
	submits []remote.Order
	cancels [][2]string
	receipt remote.Receipt
	err     error
}

func (f *fakeFileBroker) Submit(_ context.Context, o remote.Order) (remote.Receipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.submits = append(f.submits, o)
	return f.receipt, f.err
}

func (f *fakeFileBroker) Cancel(_ context.Context, requestID, sid string) (remote.Receipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancels = append(f.cancels, [2]string{requestID, sid})
	return f.receipt, f.err
}

func (f *fakeFileBroker) submitCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.submits)
}

func (f *fakeFileBroker) cancelCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.cancels)
}

// newLiveEngine 是出口链测试夹具：双闸开 + 合成运行证据 + LIVE 资金基线。
func newLiveEngine(t *testing.T, broker *fakeFileBroker, base time.Time) (*Engine, *MemStore) {
	t.Helper()
	e, mem := newEngine(t, allow{})
	e.set.LiveEnabled = true
	e.set.BrokerConnected = true
	withSyntheticAdmission(t, e, mem)
	e.now = func() time.Time { return base }
	if broker != nil {
		e.AttachFileBroker(broker)
	}
	day := base.In(tradecal.Shanghai()).Format("2006-01-02")
	if err := mem.Commit(context.Background(), Batch{Cash: &Cash{Book: BookLive, Cash: 1_000_000, DayStartDate: day, DayStartEquity: 1_000_000}}); err != nil {
		t.Fatal(err)
	}
	return e, mem
}

func placeLiveManual(t *testing.T, e *Engine, clientID, sym string, price float64, volume int) Order {
	t.Helper()
	o, err := e.Place(context.Background(), PlaceRequest{
		ClientOrderID: clientID, Account: "LIVE", Symbol: sym, Side: "buy",
		Price: price, Volume: volume, Source: "manual", Operator: "ada",
	})
	if err != nil {
		t.Fatal(err)
	}
	if o.Status != StatusPending {
		t.Fatalf("expected pending_confirm, got %+v", o)
	}
	return o
}

func trailNotes(mem *MemStore, clientID string) []string {
	var notes []string
	for _, tr := range mem.Trails() {
		if tr.ClientOrderID == clientID {
			notes = append(notes, tr.Note)
		}
	}
	return notes
}

func hasTrail(mem *MemStore, clientID, note string) bool {
	for _, n := range trailNotes(mem, clientID) {
		if n == note {
			return true
		}
	}
	return false
}

func criticalNotifies(mem *MemStore, dedupPrefix string) int {
	n := 0
	for _, ev := range mem.Events() {
		if ev.Subject != "notify.request" {
			continue
		}
		body, _ := ev.Payload.(map[string]any)
		if body["level"] == "critical" && strings.HasPrefix(fmt.Sprint(body["dedup_key"]), dedupPrefix) {
			n++
		}
	}
	return n
}

func TestLiveConfirmPublishesOrderFile(t *testing.T) {
	broker := &fakeFileBroker{receipt: remote.Receipt{State: "FILE_PUBLISHED"}}
	e, mem := newLiveEngine(t, broker, time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC))
	placeLiveManual(t, e, "le-pub", "600519.SH", 10.05, 100)
	o, err := e.Confirm(context.Background(), "le-pub")
	if err != nil || o.Status != StatusSubmitted {
		t.Fatalf("confirm %+v %v", o, err)
	}
	if broker.submitCount() != 1 {
		t.Fatalf("expected exactly one submit, got %d", broker.submitCount())
	}
	got := broker.submits[0]
	if got.SID != "le-pub" || got.Symbol != "SHSE.600519" || got.Side != "buy" || got.Price != "10.05" || got.Volume != 100 {
		t.Fatalf("broker payload %+v", got)
	}
	if !hasTrail(mem, "le-pub", trailExportPublished) {
		t.Fatalf("missing publish trail: %v", trailNotes(mem, "le-pub"))
	}
	if _, err := e.Confirm(context.Background(), "le-pub"); err == nil {
		t.Fatal("second confirm must fail")
	}
	if broker.submitCount() != 1 {
		t.Fatal("confirmed order resubmitted")
	}
}

func TestLiveSubmitUnknownNeverResubmits(t *testing.T) {
	broker := &fakeFileBroker{receipt: remote.Receipt{State: "UNKNOWN", Reason: "PREPARED"}}
	e, mem := newLiveEngine(t, broker, time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC))
	placeLiveManual(t, e, "le-unk", "600519.SH", 10.05, 100)
	o, err := e.Confirm(context.Background(), "le-unk")
	if err != nil || o.Status != StatusSubmitted {
		t.Fatalf("UNKNOWN must keep submitted: %+v %v", o, err)
	}
	if !hasTrail(mem, "le-unk", trailTransportUnknown) {
		t.Fatalf("missing transport_unknown trail: %v", trailNotes(mem, "le-unk"))
	}
	if criticalNotifies(mem, "trade:live-export-unknown:") != 1 {
		t.Fatal("UNKNOWN must notify critical")
	}
	if _, err := e.Confirm(context.Background(), "le-unk"); err == nil {
		t.Fatal("second confirm must fail")
	}
	if broker.submitCount() != 1 {
		t.Fatal("UNKNOWN resubmitted automatically")
	}
}

func TestLiveSubmitTransportUnknownKeepsSubmitted(t *testing.T) {
	broker := &fakeFileBroker{err: remote.ErrTransportUnknown}
	e, mem := newLiveEngine(t, broker, time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC))
	placeLiveManual(t, e, "le-net", "600519.SH", 10.05, 100)
	o, err := e.Confirm(context.Background(), "le-net")
	if err != nil || o.Status != StatusSubmitted {
		t.Fatalf("transport unknown must keep submitted: %+v %v", o, err)
	}
	if !hasTrail(mem, "le-net", trailTransportUnknown) {
		t.Fatalf("missing transport_unknown trail: %v", trailNotes(mem, "le-net"))
	}
	if criticalNotifies(mem, "trade:live-export-unknown:") != 1 {
		t.Fatal("transport unknown must notify critical")
	}
}

func TestLiveSubmitExplicitErrorFailsOrder(t *testing.T) {
	broker := &fakeFileBroker{err: errors.New("broker remote: ACCOUNT_NOT_ALLOWED (HTTP 403)")}
	e, mem := newLiveEngine(t, broker, time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC))
	placeLiveManual(t, e, "le-fail", "600519.SH", 10.05, 100)
	o, err := e.Confirm(context.Background(), "le-fail")
	if err != nil {
		t.Fatal(err)
	}
	if o.Status != StatusRejected || !strings.Contains(o.Reason, "出口失败") {
		t.Fatalf("explicit error must fail the order: %+v", o)
	}
	if criticalNotifies(mem, "trade:live-export-failed:") != 1 {
		t.Fatal("explicit failure must notify critical")
	}
	if broker.submitCount() != 1 {
		t.Fatal("failed submit must not retry")
	}
}

func TestLiveCancelRoutesToBrokerWithoutLocalFlip(t *testing.T) {
	broker := &fakeFileBroker{receipt: remote.Receipt{State: "FILE_PUBLISHED"}}
	e, mem := newLiveEngine(t, broker, time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC))
	placeLiveManual(t, e, "le-can", "600519.SH", 10.05, 100)
	if _, err := e.Confirm(context.Background(), "le-can"); err != nil {
		t.Fatal(err)
	}
	o, err := e.Cancel(context.Background(), "le-can")
	if err != nil {
		t.Fatal(err)
	}
	if o.Status != StatusSubmitted {
		t.Fatalf("LIVE cancel must not presuppose terminal: %+v", o)
	}
	if broker.cancelCount() != 1 {
		t.Fatalf("expected one cancel, got %d", broker.cancelCount())
	}
	got := broker.cancels[0]
	if got[0] != "le-can#cancel" || got[1] != "le-can" {
		t.Fatalf("cancel identity %+v", got)
	}
	if !hasTrail(mem, "le-can", trailCancelPublished) {
		t.Fatalf("missing cancel publish trail: %v", trailNotes(mem, "le-can"))
	}
}

func TestLiveCancelUnknownNeverResubmits(t *testing.T) {
	broker := &fakeFileBroker{receipt: remote.Receipt{State: "UNKNOWN", Reason: "PREPARED"}}
	e, mem := newLiveEngine(t, broker, time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC))
	placeLiveManual(t, e, "le-cu", "600519.SH", 10.05, 100)
	if _, err := e.Confirm(context.Background(), "le-cu"); err != nil {
		t.Fatal(err)
	}
	o, err := e.Cancel(context.Background(), "le-cu")
	if err != nil || o.Status != StatusSubmitted {
		t.Fatalf("cancel UNKNOWN must keep order working: %+v %v", o, err)
	}
	if !hasTrail(mem, "le-cu", trailTransportUnknown) {
		t.Fatalf("missing transport_unknown trail: %v", trailNotes(mem, "le-cu"))
	}
	if criticalNotifies(mem, "trade:live-cancel-unknown:") != 1 {
		t.Fatal("cancel UNKNOWN must notify critical")
	}
}

func TestLiveCancelPendingNeedsNoBroker(t *testing.T) {
	broker := &fakeFileBroker{receipt: remote.Receipt{State: "FILE_PUBLISHED"}}
	e, _ := newLiveEngine(t, broker, time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC))
	placeLiveManual(t, e, "le-pc", "600519.SH", 10.05, 100)
	o, err := e.Cancel(context.Background(), "le-pc")
	if err != nil || o.Status != StatusCancelled || o.Reason != "确认已放弃" {
		t.Fatalf("pending cancel %+v %v", o, err)
	}
	if broker.cancelCount() != 0 || broker.submitCount() != 0 {
		t.Fatal("pending cancel must not touch broker")
	}
}

func TestLiveGatesClosedStillReject(t *testing.T) {
	// 闸关回归：默认 Settings 双闸恒 false，LIVE 委托必须被拒。
	e, _ := newEngine(t, allow{})
	o, err := e.Place(context.Background(), PlaceRequest{
		ClientOrderID: "le-gate", Account: "LIVE", Symbol: "600519.SH", Side: "buy",
		Price: 10, Volume: 100, Source: "manual", Operator: "ada",
	})
	if err != nil {
		t.Fatal(err)
	}
	if o.Status != StatusRejected {
		t.Fatalf("closed gates must reject: %+v", o)
	}
}

func TestLiveCancelDisconnectedBrokerFailsClosed(t *testing.T) {
	broker := &fakeFileBroker{receipt: remote.Receipt{State: "FILE_PUBLISHED"}}
	e, _ := newLiveEngine(t, broker, time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC))
	placeLiveManual(t, e, "le-dc", "600519.SH", 10.05, 100)
	if _, err := e.Confirm(context.Background(), "le-dc"); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	e.set.BrokerConnected = false
	e.mu.Unlock()
	if _, err := e.Cancel(context.Background(), "le-dc"); err == nil {
		t.Fatal("disconnected broker must fail closed")
	}
	if broker.cancelCount() != 0 {
		t.Fatal("cancel attempted with disconnected broker")
	}
}

func TestLiveChaseCancelsViaBrokerWithoutLocalFlip(t *testing.T) {
	broker := &fakeFileBroker{receipt: remote.Receipt{State: "FILE_PUBLISHED"}}
	base := time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
	e, mem := newLiveEngine(t, broker, base)
	// 可卖数量：先补一手 T+0 可卖持仓（早于卖出委托）。
	if err := mem.Commit(context.Background(), Batch{Positions: []Position{{Book: BookLive, Symbol: "600519.SH", Quantity: 100, Available: 100, AvgCost: 9.5}}}); err != nil {
		t.Fatal(err)
	}
	o, err := e.Place(context.Background(), PlaceRequest{
		ClientOrderID: "le-chase", Account: "LIVE", Symbol: "600519.SH", Side: "sell",
		Price: 10, Volume: 100, Source: "watch", Operator: "risk",
	})
	if err != nil || o.Status != StatusPending {
		t.Fatalf("watch live place %+v %v", o, err)
	}
	if _, err = e.Confirm(context.Background(), "le-chase"); err != nil {
		t.Fatal(err)
	}
	e.now = func() time.Time { return base.Add(61 * time.Second) }
	if err := e.SweepWorking(context.Background()); err != nil {
		t.Fatal(err)
	}
	if broker.cancelCount() != 1 || broker.cancels[0][1] != "le-chase" {
		t.Fatalf("live chase must issue broker cancel: %+v", broker.cancels)
	}
	fresh, err := e.Get(context.Background(), "le-chase")
	if err != nil || fresh.Status != StatusSubmitted {
		t.Fatalf("live chase must not flip local state: %+v %v", fresh, err)
	}
	orders, err := e.Orders(context.Background(), "LIVE")
	if err != nil || len(orders) != 1 {
		t.Fatalf("live chase must not re-place: %+v %v", orders, err)
	}
}

func TestSimChainUnaffectedByBroker(t *testing.T) {
	// SIM 零回归：挂了出口也不影响模拟链（place 直接成交 + 本地撤单）。
	broker := &fakeFileBroker{receipt: remote.Receipt{State: "FILE_PUBLISHED"}}
	e, mem := newLiveEngine(t, broker, time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC))
	q := bar()
	o, err := e.Place(context.Background(), PlaceRequest{
		ClientOrderID: "sim-1", Account: "SIM", Symbol: "600519.SH", Side: "buy",
		Price: 10.05, Volume: 100, Quote: &q, Source: "manual", Operator: "ada",
	})
	if err != nil || o.Status != StatusFilled || o.Filled != 100 {
		t.Fatalf("sim place %+v %v", o, err)
	}
	p, err := e.Place(context.Background(), PlaceRequest{
		ClientOrderID: "sim-2", Account: "SIM", Symbol: "600519.SH", Side: "buy",
		Price: 9.9, Volume: 100, Source: "manual", Operator: "ada",
	})
	if err != nil || p.Status != StatusSubmitted {
		t.Fatalf("sim pending %+v %v", p, err)
	}
	c, err := e.Cancel(context.Background(), "sim-2")
	if err != nil || c.Status != StatusCancelled {
		t.Fatalf("sim cancel %+v %v", c, err)
	}
	if broker.submitCount() != 0 || broker.cancelCount() != 0 {
		t.Fatal("SIM chain must never touch broker")
	}
	if len(mem.Events()) == 0 {
		t.Fatal("sim events missing")
	}
}
