package core

// closeLoop 级测试（内存装配，无需 PG/NATS）：页内 pair 去重、库存恒等门冷却
// 生效且过期自愈、暂态读错误不冷却不误报、ErrConflict/ErrExecutionProtected
// 跳过不失败、短页回卷 0、超配 Warnf→>2min Errorf 升级、仓储未实现接口时早退、
// 观测操作已注册。

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/log"

	"server/app/strategy/internal/biz"
	"server/app/strategy/internal/data"
	"server/pkg/observe"
)

// closureFakeRepo 同时实现 biz.SignalRepo 的读取面与 biz.ClosureProjectionRepo。
type closureFakeRepo struct {
	biz.SignalRepo // 未用端口保持 nil（与 redeliveryProjectionRepo 同款聚焦装配）
	ids            []int
	facts          *biz.ClosureFacts
	factsErr       error
	factsCalls     int
	signal         *biz.Signal
	getErr         error
	transitionErr  error
	calls          int
	gotClose       []*biz.Close
	gotReason      []string
	gotActor       []string
}

func (r *closureFakeRepo) CloseCandidates(_ context.Context, book string, afterID, limit int) ([]int, error) {
	return r.ids, nil
}

func (r *closureFakeRepo) ClosurePairFacts(_ context.Context, book, symbol string) (*biz.ClosureFacts, error) {
	r.factsCalls++
	if r.factsErr != nil {
		return nil, r.factsErr
	}
	return r.facts, nil
}

func (r *closureFakeRepo) Get(_ context.Context, id int) (*biz.Signal, error) {
	return r.signal, r.getErr
}

func (r *closureFakeRepo) Transition(_ context.Context, id int, from, to biz.Status, reason, actor, trace string, env biz.EnvelopeFunc, patch *biz.SignalPatch) (*biz.Signal, error) {
	r.calls++
	if r.transitionErr != nil {
		return nil, r.transitionErr
	}
	r.gotReason = append(r.gotReason, reason)
	r.gotActor = append(r.gotActor, actor)
	if patch != nil && patch.ExitPrice != nil && patch.PnlPct != nil && patch.ClosedAt != nil {
		r.gotClose = append(r.gotClose, &biz.Close{ExitPrice: *patch.ExitPrice, PnlPct: *patch.PnlPct, At: *patch.ClosedAt})
	}
	out := *r.signal
	out.Status = to
	return &out, nil
}

func newClosureRunner(t *testing.T, repo biz.SignalRepo, now time.Time) *Runner {
	t.Helper()
	uc := biz.NewUsecase(biz.NewSettings(nil), nil, nil, nil, repo, nil, nil, nil, nil, log.DefaultLogger)
	r := NewRunner(uc, &data.Data{}, nil, nil, nil, log.DefaultLogger)
	r.now = func() time.Time { return now }
	return r
}

var closureCoreT0 = time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC)

func closureCoreFacts() *biz.ClosureFacts { // 单候选单轮回窗口：buy 100@10 fee5 → sell 100@11 fee5
	ev := 100
	return &biz.ClosureFacts{
		Signals: []biz.Signal{{ID: 11, Book: "paper", Symbol: "600519.SH", Side: "buy", Status: biz.StatusDone, ExecutedVolume: &ev}},
		Fills: []biz.FillFact{
			{ID: 1, SignalID: "11", Side: "buy", Qty: 100, Price: 10, Amount: 1000, Fee: 5, CreatedAt: closureCoreT0},
			{ID: 2, SignalID: "", Side: "sell", Qty: 100, Price: 11, Amount: 1100, Fee: 5, CreatedAt: closureCoreT0.Add(time.Minute)},
		},
		PendingIntent: map[int]bool{},
	}
}

func closureCoreSignal() *biz.Signal {
	ev := 100
	return &biz.Signal{ID: 11, Book: "paper", Symbol: "600519.SH", Side: "buy", Status: biz.StatusDone, ExecutedVolume: &ev}
}

// 页内 pair 去重：同 pair 多候选只重放一次；plan 经 ActorTrade + Close 落库；短页回卷 0。
func TestClosurePageDeduplicatesPairsAndCloses(t *testing.T) {
	repo := &closureFakeRepo{ids: []int{11, 12}, facts: closureCoreFacts(), signal: closureCoreSignal()}
	r := newClosureRunner(t, repo, closureCoreT0)
	next, closed, err := r.reconcileClosuresPage(context.Background(), 0, 200)
	if err != nil || closed != 1 || next != 0 {
		t.Fatalf("next=%d closed=%d err=%v", next, closed, err)
	}
	if repo.factsCalls != 1 {
		t.Fatalf("same pair candidates must replay once, got %d", repo.factsCalls)
	}
	if repo.calls != 1 || len(repo.gotClose) != 1 || repo.gotActor[0] != biz.ActorTrade {
		t.Fatalf("transition calls=%d closes=%v actors=%v", repo.calls, repo.gotClose, repo.gotActor)
	}
	close0 := repo.gotClose[0]
	if close0.ExitPrice != 11 || close0.PnlPct != 8.9552 || !close0.At.Equal(closureCoreT0.Add(time.Minute)) {
		t.Fatalf("close payload %+v", close0)
	}
	if !strings.HasPrefix(repo.gotReason[0], "闭仓归并 ") {
		t.Fatalf("reason %q", repo.gotReason[0])
	}
}

// 库存恒等门：读取超限截断（合同 §1.3 枚举）→ loud + 10 分钟冷却；冷却期内不重放；过期自动重试。
func TestClosureIdentityGateCoolsDownAndHeals(t *testing.T) {
	repo := &closureFakeRepo{
		ids: []int{11},
		// 与 data/close.go 真实返回同形态：读取超限截断以 ErrClosureIdentityGate 包装。
		factsErr: fmt.Errorf("strategy: closure pair fills exceed safe bound: %w", biz.ErrClosureIdentityGate),
		signal:   closureCoreSignal(),
	}
	r := newClosureRunner(t, repo, closureCoreT0)
	if _, _, err := r.reconcileClosuresPage(context.Background(), 0, 200); err == nil {
		t.Fatal("identity gate violation must surface")
	}
	if repo.factsCalls != 1 {
		t.Fatalf("facts calls %d", repo.factsCalls)
	}
	st := r.pairState[closurePairKey{book: "paper", symbol: "600519.SH"}]
	if st == nil || st.coolUntil != closureCoreT0.Add(closureCooldown) {
		t.Fatalf("cooldown state %+v", st)
	}
	// 冷却期内：跳过评估（不再读 facts），不视为页错误。
	if _, _, err := r.reconcileClosuresPage(context.Background(), 0, 200); err != nil || repo.factsCalls != 1 {
		t.Fatalf("cooling pair must skip replay: calls=%d err=%v", repo.factsCalls, err)
	}
	// 冷却过期自动重试（迟到成交/投影回补可自愈，不永久拉黑）。
	r.now = func() time.Time { return closureCoreT0.Add(closureCooldown + time.Second) }
	repo.factsErr, repo.facts = nil, closureCoreFacts()
	if _, closed, err := r.reconcileClosuresPage(context.Background(), 0, 200); err != nil || closed != 1 || repo.factsCalls != 2 {
		t.Fatalf("expired cooldown must retry: closed=%d calls=%d err=%v", closed, repo.factsCalls, err)
	}
	if _, ok := r.pairState[closurePairKey{book: "paper", symbol: "600519.SH"}]; ok {
		t.Fatal("clean pair state must be reclaimed")
	}
}

// 暂态读错误（PG 瞬断、页内 5s ctx 超时）不属 §1.3 恒等门枚举：错误上浮页级
// errors.Join 续跑，不 loud、不冷却，下一页立即重读；恢复后当场归属。
func TestClosureTransientFactsErrorSkipsGate(t *testing.T) {
	repo := &closureFakeRepo{ids: []int{11}, factsErr: errors.New("pg: connection reset by peer"), signal: closureCoreSignal()}
	r := newClosureRunner(t, repo, closureCoreT0)
	if _, _, err := r.reconcileClosuresPage(context.Background(), 0, 200); err == nil {
		t.Fatal("transient facts error must surface to the page")
	}
	if repo.factsCalls != 1 {
		t.Fatalf("facts calls %d", repo.factsCalls)
	}
	if st := r.pairState[closurePairKey{book: "paper", symbol: "600519.SH"}]; st != nil {
		t.Fatalf("transient read error must not cool the pair: %+v", st)
	}
	// 无冷却门：下一页立即重读，恢复后当场归属。
	repo.factsErr, repo.facts = nil, closureCoreFacts()
	if _, closed, err := r.reconcileClosuresPage(context.Background(), 0, 200); err != nil || closed != 1 || repo.factsCalls != 2 {
		t.Fatalf("no cooldown expected: closed=%d calls=%d err=%v", closed, repo.factsCalls, err)
	}
}

// 竞态正常结局：ErrConflict / ErrExecutionProtected / ErrBadTransition 跳过不失败，
// 下轮重来（ExpireDue 先例；ErrBadTransition = 同信号跨两窗口次条 plan 面对终态）。
func TestClosureConflictIsSkippedNotFailed(t *testing.T) {
	for _, cause := range []error{biz.ErrConflict, biz.ErrExecutionProtected, biz.ErrBadTransition} {
		repo := &closureFakeRepo{ids: []int{11}, facts: closureCoreFacts(), signal: closureCoreSignal(), transitionErr: cause}
		r := newClosureRunner(t, repo, closureCoreT0)
		_, closed, err := r.reconcileClosuresPage(context.Background(), 0, 200)
		if err != nil || closed != 0 || repo.calls != 1 {
			t.Fatalf("%v: closed=%d calls=%d err=%v", cause, closed, repo.calls, err)
		}
	}
}

// keyset 内存游标：整页朝下推进，短页回卷 0（重启全扫自愈）。
func TestClosurePageCursorAdvancesBoundedAndRewinds(t *testing.T) {
	ids := make([]int, 200)
	for i := range ids {
		ids[i] = i + 1
	}
	repo := &closureFakeRepo{ids: ids, facts: closureCoreFacts(), signal: closureCoreSignal()}
	r := newClosureRunner(t, repo, closureCoreT0)
	next, _, err := r.reconcileClosuresPage(context.Background(), 0, 200)
	if err != nil || next != 200 {
		t.Fatalf("full page must advance to last id: next=%d err=%v", next, err)
	}
	if repo.factsCalls != 1 { // 200 个候选同属一个 pair：整页只重放一次
		t.Fatalf("facts calls %d", repo.factsCalls)
	}
	repo.ids = []int{201}
	next, _, err = r.reconcileClosuresPage(context.Background(), next, 200)
	if err != nil || next != 0 {
		t.Fatalf("short page must rewind to 0: next=%d err=%v", next, err)
	}
}

// Gate-B 超配分级：首见 Warnf 计数，持续 >2 分钟升 Errorf；自愈后计时册清除。
func TestClosureOverAllocationEscalatesAfterTwoMinutes(t *testing.T) {
	ev := 100
	overFacts := &biz.ClosureFacts{
		Signals: []biz.Signal{{ID: 11, Book: "paper", Symbol: "600519.SH", Side: "buy", Status: biz.StatusDone, ExecutedVolume: &ev}},
		Fills: []biz.FillFact{
			{ID: 1, SignalID: "11", Side: "buy", Qty: 120, Price: 10, Amount: 1200, Fee: 5, CreatedAt: closureCoreT0},
			{ID: 2, SignalID: "", Side: "sell", Qty: 120, Price: 11, Amount: 1320, Fee: 5, CreatedAt: closureCoreT0.Add(time.Minute)},
		},
		PendingIntent: map[int]bool{},
	}
	repo := &closureFakeRepo{ids: []int{11}, facts: overFacts, signal: closureCoreSignal()}
	r := newClosureRunner(t, repo, closureCoreT0)
	key := closurePairKey{book: "paper", symbol: "600519.SH"}
	if _, _, err := r.reconcileClosuresPage(context.Background(), 0, 200); err != nil || repo.calls != 0 {
		t.Fatalf("over-allocated signal must skip, not transition: calls=%d err=%v", repo.calls, err)
	}
	st := r.pairState[key]
	if st == nil || st.over[11] != closureCoreT0 || st.escalated[11] {
		t.Fatalf("first sight should only warn: %+v", st)
	}
	r.now = func() time.Time { return closureCoreT0.Add(closureOverEscalate + time.Second) }
	if _, _, err := r.reconcileClosuresPage(context.Background(), 0, 200); err != nil {
		t.Fatal(err)
	}
	if !r.pairState[key].escalated[11] {
		t.Fatal("persistent over-allocation must escalate to Errorf after 2 minutes")
	}
	// 投影环纠正 EV 后：超配自愈，计时册清除，正常归属。
	ev = 120
	if _, closed, err := r.reconcileClosuresPage(context.Background(), 0, 200); err != nil || closed != 1 || repo.calls != 1 {
		t.Fatalf("healed projection must close: closed=%d calls=%d err=%v", closed, repo.calls, err)
	}
	if _, ok := r.pairState[key]; ok {
		t.Fatal("healed pair state must be reclaimed")
	}
}

// 仓储未实现 ClosureProjectionRepo 时 Warnf 早退（executionLoop 类型断言扩展模式同款）。
func TestClosurePageBailsWhenRepoLacksPort(t *testing.T) {
	r := newClosureRunner(t, &closureSignalOnlyRepo{}, closureCoreT0)
	if _, _, err := r.reconcileClosuresPage(context.Background(), 0, 200); err == nil ||
		!strings.Contains(err.Error(), "closure projection repository unavailable") {
		t.Fatalf("missing port must bail early: %v", err)
	}
}

type closureSignalOnlyRepo struct{ biz.SignalRepo }

// 观测操作已注册：ForService("strategy").Op("closure_projection") 无 panic。
func TestClosureProjectionObservationIsRegistered(t *testing.T) {
	before := strategyOp(t, "closure_projection").Total.Attempted
	finish := observe.ForService("strategy").Op("closure_projection").Begin()
	finish(observe.Succeeded)
	after := strategyOp(t, "closure_projection").Total.Attempted
	if after-before != 1 {
		t.Fatalf("op counter not ticking: before=%d after=%d", before, after)
	}
}
