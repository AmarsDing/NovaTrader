package kb

import (
	"context"
	"fmt"
	"testing"
	"time"

	"server/conf"

	"github.com/go-kratos/kratos/v2/log"
)

// fakeRepo 只实现 BackfillCases 用到的方法，其余走内嵌接口，被调到就 panic（测试中不允许）。
type fakeRepo struct {
	KnowledgeRepo
	rows      []SignalEventRow
	cases     map[SignalRef]CaseRecord
	caseSeq   int
	listErr   error
	existsErr error
}

func (f *fakeRepo) ListPublishedSignalEvents(_ context.Context, _ string, since time.Time, limit int) ([]SignalEventRow, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []SignalEventRow
	for _, row := range f.rows {
		if !row.PublishedAt.Before(since) {
			out = append(out, row)
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeRepo) ExistingSignalCases(_ context.Context, refs []SignalRef) (map[SignalRef]bool, error) {
	if f.existsErr != nil {
		return nil, f.existsErr
	}
	out := map[SignalRef]bool{}
	for _, ref := range refs {
		if _, ok := f.cases[ref]; ok {
			out[ref] = true
		}
	}
	return out, nil
}

func (f *fakeRepo) CreateCase(_ context.Context, c CaseRecord, _ NewDocument, _ []ChunkRow) (int, int, []int, bool, error) {
	ref := SignalRef{Book: c.Book, SignalID: c.SignalID}
	if rec, ok := f.cases[ref]; ok {
		_ = rec
		return 1, 1, nil, false, nil
	}
	f.caseSeq++
	f.cases[ref] = c
	return f.caseSeq, f.caseSeq, []int{f.caseSeq}, true, nil
}

func newFakeRepo() *fakeRepo { return &fakeRepo{cases: map[SignalRef]CaseRecord{}} }

func TestBackfillCases(t *testing.T) {
	repo := newFakeRepo()
	uc := NewKnowledgeUsecase(repo, nil, &conf.Kb{}, log.DefaultLogger)
	now := time.Date(2026, 10, 20, 15, 30, 0, 0, time.UTC)
	loc := time.FixedZone("CST", 8*3600)
	sigTime := time.Date(2026, 10, 20, 9, 41, 0, 0, loc).UTC()
	closedTime := time.Date(2026, 10, 20, 14, 57, 0, 0, loc).UTC()
	// RFC3339 时间戳，strategy 的 SignalUpdated 载荷用同一格式（time.Time 的 JSON 序列化结果）。
	ts := func(t time.Time) string { return t.Format(time.RFC3339Nano) }
	repo.rows = []SignalEventRow{
		{EventID: "e1", PublishedAt: now.Add(-time.Hour), Payload: []byte(fmt.Sprintf(
			`{"signal_id":1001,"book":"paper","status":"closed","stock_code":"000001.SZ","stock_name":"平安银行",`+
				`"strategy":"weak2strong","signal_type":"buy","signal_time":%q,"closed_at":%q,`+
				`"exit_price":11.2,"pnl_pct":3.2,"holding_days":1,"reasoning":"弱转强",`+
				`"pattern":"强势回踩","emotion_phase":"发酵","attribution":"missed","missed":true}`, ts(sigTime), ts(closedTime)))},
		{EventID: "e2", PublishedAt: now.Add(-time.Hour), Payload: []byte(fmt.Sprintf(
			`{"signal_id":1002,"book":"paper","status":"closed","stock_code":"000002.SZ","signal_time":%q,`+
				`"pattern":"首板","emotion_phase":"启动","attribution":"news_misread"}`, ts(sigTime)))}, // 缺 missed
		{EventID: "e3", PublishedAt: now.Add(-time.Hour), Payload: []byte(fmt.Sprintf(
			`{"signal_id":1003,"book":"paper","status":"closed","stock_code":"000003.SZ","signal_time":%q,`+
				`"emotion_phase":"分歧","attribution":"missed","missed":false}`, ts(sigTime)))}, // 缺 pattern
		{EventID: "e4", PublishedAt: now.Add(-time.Hour), Payload: []byte(fmt.Sprintf(
			`{"signal_id":1004,"book":"paper","status":"done","stock_code":"000004.SZ","signal_time":%q,`+
				`"pattern":"首板","emotion_phase":"启动","attribution":"missed","missed":false}`, ts(sigTime)))}, // 非终态
		{EventID: "e5", PublishedAt: now.Add(-time.Hour), Payload: []byte(`{"signal_id":`)}, // 坏载荷
		{EventID: "e6", PublishedAt: now.Add(-time.Hour), Payload: []byte(fmt.Sprintf(
			`{"signal_id":1006,"book":"paper","status":"closed","stock_code":"","signal_time":%q,`+
				`"pattern":"首板","emotion_phase":"启动","attribution":"missed","missed":false}`, ts(sigTime)))}, // 缺 stock_code，RecordCase 拒绝
		{EventID: "e7", PublishedAt: now.Add(-time.Hour), Payload: []byte(fmt.Sprintf(
			`{"signal_id":1001,"book":"paper","status":"closed","stock_code":"000001.SZ","stock_name":"平安银行",`+
				`"strategy":"weak2strong","signal_type":"buy","signal_time":%q,"closed_at":%q,`+
				`"exit_price":11.2,"pnl_pct":3.2,"holding_days":1,"reasoning":"弱转强",`+
				`"pattern":"强势回踩","emotion_phase":"发酵","attribution":"missed","missed":true}`, ts(sigTime), ts(closedTime)))}, // 与 e1 同信号重复投递
	}
	res, err := uc.BackfillCases(context.Background(), "strategy.signal.updated", now.Add(-2*time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	if res.Scanned != 7 || res.Created != 1 || res.Skipped != 2 || res.Failed != 2 || res.Existing != 0 {
		t.Fatalf("result = %+v, want scanned=7 created=1 existing=0 skipped=2 failed=2", res)
	}
	rec, ok := repo.cases[SignalRef{Book: "paper", SignalID: 1001}]
	if !ok {
		t.Fatal("signal 1001 must have a case")
	}
	if rec.Attribution != "missed" || rec.Pattern != "强势回踩" || rec.EmotionPhase != "发酵" {
		t.Fatalf("case attribution fields = %+v", rec)
	}
	if rec.Outcome != "win" || rec.ClosedAt == (time.Time{}) {
		t.Fatalf("case outcome/closed_at = %+v", rec)
	}

	// 重跑幂等：同一批事件再跑，全部按已存在去重，不重复建案例。
	res2, err := uc.BackfillCases(context.Background(), "strategy.signal.updated", now.Add(-2*time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Created != 0 || res2.Existing != 1 {
		t.Fatalf("rerun = %+v, want created=0 existing=1", res2)
	}

	// 已有案例的候选批量去重计数。
	res3, err := uc.BackfillCases(context.Background(), "strategy.signal.updated", now.Add(-2*time.Hour), 3)
	if err != nil {
		t.Fatal(err)
	}
	if res3.Scanned != 3 {
		t.Fatalf("limited run = %+v, want scanned=3", res3)
	}
}

func TestBackfillCasesRepoError(t *testing.T) {
	repo := newFakeRepo()
	repo.listErr = fmt.Errorf("db down")
	uc := NewKnowledgeUsecase(repo, nil, &conf.Kb{}, log.DefaultLogger)
	if _, err := uc.BackfillCases(context.Background(), "strategy.signal.updated", time.Now(), 100); err == nil {
		t.Fatal("list error must propagate")
	}
	repo.listErr = nil
	repo.existsErr = fmt.Errorf("db down")
	repo.rows = []SignalEventRow{{EventID: "e1", PublishedAt: time.Now(), Payload: []byte(
		`{"signal_id":1,"book":"paper","status":"closed","stock_code":"000001.SZ","signal_time":"2026-10-20T09:41:00+08:00"}`)}}
	if _, err := uc.BackfillCases(context.Background(), "strategy.signal.updated", time.Time{}, 100); err == nil {
		t.Fatal("exists error must propagate")
	}
}
