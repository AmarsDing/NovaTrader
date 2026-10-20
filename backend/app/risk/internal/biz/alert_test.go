package biz

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"server/pkg/alert"

	"github.com/go-kratos/kratos/v2/log"
)

type fakeAlertStore struct {
	mu          sync.Mutex
	rows        map[string]alert.Rule
	applies     []alert.Rule
	fires       []*AlertFire
	activeCalls int
	applyErr    error
}

func newFakeAlertStore(rows ...alert.Rule) *fakeAlertStore {
	s := &fakeAlertStore{rows: map[string]alert.Rule{}}
	for _, r := range rows {
		s.rows[r.ID] = r
	}
	return s
}

func (s *fakeAlertStore) sorted() []alert.Rule {
	out := make([]alert.Rule, 0, len(s.rows))
	for _, r := range s.rows {
		out = append(out, r)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].CreatedAt.After(out[j].CreatedAt); j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

func (s *fakeAlertStore) ListAlertRules(context.Context) ([]alert.Rule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sorted(), nil
}

func (s *fakeAlertStore) ActiveAlertRules(context.Context) ([]alert.Rule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activeCalls++
	out := make([]alert.Rule, 0, len(s.rows))
	for _, r := range s.sorted() {
		if r.Status == alert.StatusActive {
			out = append(out, r)
		}
	}
	return out, nil
}

func (s *fakeAlertStore) GetAlertRule(_ context.Context, id string) (alert.Rule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rows[id]
	if !ok {
		return alert.Rule{}, ErrAlertNotFound
	}
	return r, nil
}

func (s *fakeAlertStore) CreateAlertRule(_ context.Context, r alert.Rule, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[r.ID] = r
	return nil
}

func (s *fakeAlertStore) ReplaceAlertRule(_ context.Context, r alert.Rule, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.rows[r.ID]; !ok {
		return ErrAlertNotFound
	}
	s.rows[r.ID] = r
	return nil
}

func (s *fakeAlertStore) DeleteAlertRule(_ context.Context, id, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.rows[id]; !ok {
		return ErrAlertNotFound
	}
	delete(s.rows, id)
	return nil
}

func (s *fakeAlertStore) ApplyAlertStep(_ context.Context, next alert.Rule, fire *AlertFire) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.applyErr != nil {
		return false, s.applyErr
	}
	s.applies = append(s.applies, next)
	cur, ok := s.rows[next.ID]
	if !ok {
		return false, ErrAlertNotFound
	}
	// 与 data 层同一乐观前置：armed_at 被重挂/编辑前移、或已被并发节拍触发，整步作废。
	if !cur.ArmedAt.Equal(next.ArmedAt) || cur.Status != alert.StatusActive || cur.TriggeredAt != nil {
		return false, nil
	}
	s.rows[next.ID] = next
	if fire != nil {
		s.fires = append(s.fires, fire)
	}
	return true, nil
}

type quoteErrorSource struct{ err error }

func (q quoteErrorSource) Quotes(context.Context, []string) (map[string]Snapshot, error) {
	return nil, q.err
}

func seedRule(t *testing.T, id, symbol string, cond alert.Condition, target float64) alert.Rule {
	t.Helper()
	r, err := alert.Create(id, symbol, cond, target, t0)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func newAlertCenter(store AlertStore, quotes QuoteSource, now *time.Time) *AlertCenter {
	a := NewAlertCenter(store, quotes, log.DefaultLogger)
	a.now = func() time.Time { return *now }
	return a
}

func TestAlertCreateValidates(t *testing.T) {
	now := t0
	a := newAlertCenter(newFakeAlertStore(), &fakeQuotes{m: map[string]Snapshot{}}, &now)
	if _, err := a.Create(context.Background(), "600519.SH", "sideways", 1700, "", "op"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad condition: %v", err)
	}
	if _, err := a.Create(context.Background(), "600519.SH", "above", 0, "", "op"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad target: %v", err)
	}
	long := make([]byte, 201)
	for i := range long {
		long[i] = 'x'
	}
	if _, err := a.Create(context.Background(), "600519.SH", "above", 1700, string(long), "op"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("long note: %v", err)
	}
	r, err := a.Create(context.Background(), " 600519.sh ", "Above", 1700, " 破前高 ", "op")
	if err != nil {
		t.Fatal(err)
	}
	if r.Symbol != "600519.SH" || r.Status != alert.StatusActive || r.Note != "破前高" || !r.ArmedAt.Equal(t0) {
		t.Fatalf("created rule: %+v", r)
	}
	if r.ID == "" || len(r.ID) < 5 {
		t.Fatalf("id should be generated: %q", r.ID)
	}
}

func TestAlertEditWhitelistRebuild(t *testing.T) {
	old := seedRule(t, "alr-1", "600519.SH", alert.Crosses, 1700)
	last := 1699.0
	trig := t0.Add(-time.Hour)
	old.LastPrice = &last
	old.Status = alert.StatusTriggered
	old.TriggeredAt = &trig
	old.Note = "旧备注"
	store := newFakeAlertStore(old)
	now := t0.Add(time.Minute)
	a := newAlertCenter(store, &fakeQuotes{m: map[string]Snapshot{}}, &now)

	next, err := a.Edit(context.Background(), "alr-1", "000001.SZ", "above", 9.5, "新备注", "op2")
	if err != nil {
		t.Fatal(err)
	}
	if next.Status != alert.StatusActive || next.LastPrice != nil || next.TriggeredAt != nil {
		t.Fatalf("edit must clear triggered-state fields: %+v", next)
	}
	if !next.CreatedAt.Equal(t0) || !next.ArmedAt.Equal(now) {
		t.Fatalf("identity times: %+v", next)
	}
	if next.Symbol != "000001.SZ" || next.Condition != alert.Above || next.TargetPrice != 9.5 || next.Note != "新备注" {
		t.Fatalf("whitelist fields: %+v", next)
	}
	persisted, _ := store.GetAlertRule(context.Background(), "alr-1")
	if persisted.Status != alert.StatusActive || persisted.LastPrice != nil {
		t.Fatalf("stored row should be the rebuilt one: %+v", persisted)
	}
	if _, err := a.Edit(context.Background(), "alr-x", "600519.SH", "above", 1, "", "op"); !errors.Is(err, ErrAlertNotFound) {
		t.Fatalf("missing id: %v", err)
	}
}

func TestAlertRearmKeepsParams(t *testing.T) {
	old := seedRule(t, "alr-1", "600519.SH", alert.Below, 1600)
	old.Status = alert.StatusTriggered
	old.Note = "破位提醒"
	trig := t0.Add(-time.Minute)
	old.TriggeredAt = &trig
	store := newFakeAlertStore(old)
	now := t0.Add(time.Minute)
	a := newAlertCenter(store, &fakeQuotes{m: map[string]Snapshot{}}, &now)

	next, err := a.Rearm(context.Background(), "alr-1", "op")
	if err != nil {
		t.Fatal(err)
	}
	if next.Status != alert.StatusActive || next.TriggeredAt != nil || next.LastPrice != nil {
		t.Fatalf("rearm must re-arm cleanly: %+v", next)
	}
	if next.Symbol != "600519.SH" || next.Condition != alert.Below || next.TargetPrice != 1600 || next.Note != "破位提醒" {
		t.Fatalf("rearm keeps params: %+v", next)
	}
	if _, err := a.Rearm(context.Background(), "alr-x", "op"); !errors.Is(err, ErrAlertNotFound) {
		t.Fatalf("missing id: %v", err)
	}
	if err := a.Delete(context.Background(), "alr-1", "op"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetAlertRule(context.Background(), "alr-1"); !errors.Is(err, ErrAlertNotFound) {
		t.Fatalf("deleted rule must be gone")
	}
	if err := a.Delete(context.Background(), "alr-1", "op"); !errors.Is(err, ErrAlertNotFound) {
		t.Fatalf("delete twice: %v", err)
	}
}

func TestAlertTickFiresAndKeepsBaselines(t *testing.T) {
	aRule := seedRule(t, "alr-a", "600519.SH", alert.Above, 10)
	bRule := seedRule(t, "alr-b", "000001.SZ", alert.Crosses, 10)
	store := newFakeAlertStore(aRule, bRule)
	quotes := &fakeQuotes{m: map[string]Snapshot{
		"600519.SH": {Symbol: "600519.SH", Last: 10.5},
		"000001.SZ": {Symbol: "000001.SZ", Last: 9.9},
	}}
	now := t0
	a := newAlertCenter(store, quotes, &now)

	a.Tick(context.Background())
	if len(store.fires) != 1 || store.fires[0].Rule.ID != "alr-a" || store.fires[0].Price != 10.5 {
		t.Fatalf("above must fire once with price: %+v", store.fires)
	}
	if got := store.rows["alr-a"].Status; got != alert.StatusTriggered {
		t.Fatalf("fired rule persisted triggered: %v", got)
	}
	if lp := store.rows["alr-b"].LastPrice; lp == nil || *lp != 9.9 {
		t.Fatalf("crosses baseline persisted: %v", lp)
	}
	if len(store.fires) > 1 {
		t.Fatal("first tick only establishes the crosses baseline")
	}

	// 第二拍同价：不重复落库、不重复触发。
	now = now.Add(3 * time.Second)
	a.Tick(context.Background())
	if len(store.applies) != 2 {
		t.Fatalf("unchanged prices must not be persisted: %d applies", len(store.applies))
	}

	// 上穿触发 crosses；先撤掉 600519.SH 的快照，确认缺行情只跳过该代码。
	delete(quotes.m, "600519.SH")
	quotes.m["000001.SZ"] = Snapshot{Symbol: "000001.SZ", Last: 10.2}
	now = now.Add(3 * time.Second)
	a.Tick(context.Background())
	if len(store.fires) != 2 || store.fires[1].Rule.ID != "alr-b" {
		t.Fatalf("cross up must fire: %+v", store.fires)
	}
	if store.fires[1].At != now {
		t.Fatalf("fire at beat time: %v", store.fires[1].At)
	}

	// 触发过的规则退出评估：节拍不再读它。
	now = now.Add(3 * time.Second)
	before := len(store.applies)
	a.Tick(context.Background())
	if len(store.applies) != before {
		t.Fatal("triggered rules leave the active set")
	}
}

func TestAlertTickThrottlesAndSkips(t *testing.T) {
	rule := seedRule(t, "alr-a", "600519.SH", alert.Above, 10)
	store := newFakeAlertStore(rule)
	now := t0
	a := newAlertCenter(store, &fakeQuotes{m: map[string]Snapshot{"600519.SH": {Symbol: "600519.SH", Last: 9}}}, &now)

	a.Tick(context.Background())
	a.Tick(context.Background())
	if store.activeCalls != 1 {
		t.Fatalf("beats closer than the throttle must coalesce: %d reads", store.activeCalls)
	}

	b := newAlertCenter(store, quoteErrorSource{err: errors.New("redis down")}, &now)
	now = now.Add(3 * time.Second)
	applies := len(store.applies)
	b.Tick(context.Background())
	if len(store.applies) != applies {
		t.Fatal("quote read failure must not write anything")
	}
}

func TestAlertTickSingleBadRuleDoesNotBlockOthers(t *testing.T) {
	aRule := seedRule(t, "alr-a", "600519.SH", alert.Above, 10)
	bRule := seedRule(t, "alr-b", "000001.SZ", alert.Above, 9)
	store := newFakeAlertStore(aRule, bRule)
	store.applyErr = nil
	quotes := &fakeQuotes{m: map[string]Snapshot{
		"600519.SH": {Symbol: "600519.SH", Last: 10.5},
		"000001.SZ": {Symbol: "000001.SZ", Last: 9.5},
	}}
	now := t0
	a := newAlertCenter(store, quotes, &now)
	// 让第一条在落库时失败（模拟并发删除），第二条必须照样触发落库。
	stub := &flakyStepStore{fakeAlertStore: store, failFor: "alr-a"}
	a.store = stub
	a.Tick(context.Background())
	if len(stub.fires) != 1 || stub.fires[0].Rule.ID != "alr-b" {
		t.Fatalf("one bad rule must not block the rest: %+v", stub.fires)
	}
}

type flakyStepStore struct {
	*fakeAlertStore
	failFor string
}

func (s *flakyStepStore) ApplyAlertStep(ctx context.Context, next alert.Rule, fire *AlertFire) (bool, error) {
	if next.ID == s.failFor {
		return false, ErrAlertNotFound
	}
	return s.fakeAlertStore.ApplyAlertStep(ctx, next, fire)
}

// rearmBetweenStore 模拟「评估途中规则被重新布防」：第一步落库前 armed_at 已被前移，
// CAS 应让整步（含触发与 outbox）作废，规则保持 active 等下拍再评。
type rearmBetweenStore struct {
	*fakeAlertStore
	rearmID string
	done    bool
}

func (s *rearmBetweenStore) ApplyAlertStep(ctx context.Context, next alert.Rule, fire *AlertFire) (bool, error) {
	if next.ID == s.rearmID && !s.done {
		s.done = true
		s.mu.Lock()
		cur := s.rows[s.rearmID]
		cur.ArmedAt = cur.ArmedAt.Add(time.Second)
		s.rows[s.rearmID] = cur
		s.mu.Unlock()
	}
	return s.fakeAlertStore.ApplyAlertStep(ctx, next, fire)
}

func TestAlertTickStaleFireDiscardedAfterConcurrentRearm(t *testing.T) {
	rule := seedRule(t, "alr-a", "600519.SH", alert.Above, 10)
	store := &rearmBetweenStore{fakeAlertStore: newFakeAlertStore(rule), rearmID: "alr-a"}
	quotes := &fakeQuotes{m: map[string]Snapshot{"600519.SH": {Symbol: "600519.SH", Last: 10.5}}}
	now := t0
	a := newAlertCenter(store, quotes, &now)

	a.Tick(context.Background())
	if len(store.fires) != 0 {
		t.Fatalf("stale fire must be discarded, not notified: %+v", store.fires)
	}
	got := store.rows["alr-a"]
	if got.Status != alert.StatusActive || got.TriggeredAt != nil {
		t.Fatalf("rearmed rule must stay active and untriggered: %+v", got)
	}

	// 下一拍按最新读面（重挂后的 armed_at）正常触发。
	now = now.Add(3 * time.Second)
	a.Tick(context.Background())
	if len(store.fires) != 1 || store.fires[0].Rule.ID != "alr-a" {
		t.Fatalf("next beat fires against the fresh read: %+v", store.fires)
	}
	if got := store.rows["alr-a"].Status; got != alert.StatusTriggered {
		t.Fatalf("rule triggered on the next beat: %v", got)
	}
}
