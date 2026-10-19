package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type replayStore struct {
	dispositionAudit
	found  bool
	err    error
	called []string
	// batch 剧本：script 里没登记的 id 一律按重排成功回；auditErr 让成功审计写失败。
	script map[string]struct {
		requeued bool
		err      error
	}
	auditErr  error
	batchSeen []string
}

func (s *replayStore) RequeueDeadLetter(_ context.Context, eventID string) (bool, error) {
	s.called = append(s.called, eventID)
	return s.found, s.err
}

func (s *replayStore) RequeueDeadLetters(_ context.Context, ids []string) []DeadLetterRequeueOutcome {
	s.batchSeen = append(s.batchSeen, ids...)
	out := make([]DeadLetterRequeueOutcome, 0, len(ids))
	for _, id := range ids {
		if o, ok := s.script[id]; ok {
			out = append(out, DeadLetterRequeueOutcome{EventID: id, Requeued: o.requeued, Err: o.err})
			continue
		}
		out = append(out, DeadLetterRequeueOutcome{EventID: id, Requeued: true})
	}
	return out
}

func (s *replayStore) Write(ctx context.Context, e Entry) error {
	if s.auditErr != nil {
		return s.auditErr
	}
	return s.memAudit.Write(ctx, e)
}

type replayStoreOff struct{ replayStore }

func (s *replayStoreOff) Enabled() bool { return false }

// 批量重放审批：逐条后果、审计口径与单行版一致；有界、去重、部分失败如实。
func TestDeadLetterReplayBatchBoundedPerItemAudited(t *testing.T) {
	g0 := &Gate{Settings: Settings{Secret: "test-batch-replay-secret"}}
	viewerToken, err := Issue(g0.Settings.Secret, User{Name: "viewer", Role: "viewer"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ownerToken, err := Issue(g0.Settings.Secret, User{Name: "owner", Role: "owner"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	post := func(g *Gate, token, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/ops/dead-letter-replay-batch", strings.NewReader(body))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		g.ServeHTTP(w, req)
		return w
	}

	// 未登录 / 非 Owner / GET
	{
		g := &Gate{Settings: g0.Settings, Audit: &replayStore{}}
		if w := post(g, "", `{"event_ids":["e1"],"reason":"r"}`); w.Code != 401 {
			t.Fatalf("anonymous %d", w.Code)
		}
		if w := post(g, viewerToken, `{"event_ids":["e1"],"reason":"r"}`); w.Code != 403 {
			t.Fatalf("viewer %d", w.Code)
		}
		req := httptest.NewRequest(http.MethodGet, "/api/ops/dead-letter-replay-batch", nil)
		req.Header.Set("Authorization", "Bearer "+ownerToken)
		w := httptest.NewRecorder()
		g.ServeHTTP(w, req)
		if w.Code != 405 {
			t.Fatalf("GET %d", w.Code)
		}
	}

	// 审计未启用 / 只 Auditor 未接重排口 → 前置拒绝，不触库
	{
		off := &replayStoreOff{}
		g := &Gate{Settings: g0.Settings, Audit: off}
		if w := post(g, ownerToken, `{"event_ids":["e1"],"reason":"r"}`); w.Code != 503 {
			t.Fatalf("audit off %d", w.Code)
		}
		if len(off.batchSeen) != 0 {
			t.Fatal("audit off must refuse before touching the store")
		}
		g = &Gate{Settings: g0.Settings, Audit: &memAudit{}}
		if w := post(g, ownerToken, `{"event_ids":["e1"],"reason":"r"}`); w.Code != 503 {
			t.Fatalf("no store %d", w.Code)
		}
	}

	// 请求形状：非 JSON / 空列表 / 超限 / 非法 id / 缺理由 / 理由超长 —— 全不触库
	{
		store := &replayStore{script: map[string]struct {
			requeued bool
			err      error
		}{}}
		g := &Gate{Settings: g0.Settings, Audit: store}
		bad := []string{
			`not-json`,
			`{"event_ids":[],"reason":"r"}`,
			`{"event_ids":["*!!"],"reason":"r"}`,
			`{"event_ids":["e1"]}`,
			`{"event_ids":["e1"],"reason":"` + strings.Repeat("长", replayReasonMax+1) + `"}`,
		}
		ids65, _ := json.Marshal(map[string]any{
			"event_ids": func() []string {
				out := make([]string, 65)
				for i := range out {
					out[i] = "e" + strings.Repeat("0", i/26+1) + string(rune('a'+i%26))
				}
				return out
			}(),
			"reason": "r",
		})
		bad = append(bad, string(ids65))
		for i, body := range bad {
			if w := post(g, ownerToken, body); w.Code != 400 {
				t.Fatalf("bad shape #%d: %d %s", i, w.Code, w.Body.String())
			}
		}
		if len(store.batchSeen) != 0 {
			t.Fatal("shape errors must not reach the store")
		}
	}

	// 全成功 → 200；逐条成功审计带理由；重复 id 只去重后调用一次
	{
		store := &replayStore{}
		g := &Gate{Settings: g0.Settings, Audit: store}
		w := post(g, ownerToken, `{"event_ids":["e1","e2","e1"],"reason":"上游已修复"}`)
		if w.Code != 200 {
			t.Fatalf("ok %d %s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), `"requeued":2`) || !strings.Contains(w.Body.String(), `"errors":0`) {
			t.Fatalf("counts wrong: %s", w.Body.String())
		}
		if len(store.batchSeen) != 2 {
			t.Fatalf("duplicate id must be deduped: %+v", store.batchSeen)
		}
		if !strings.Contains(w.Body.String(), "重排不等于投递成功") || !strings.Contains(w.Body.String(), "只计首次") {
			t.Fatalf("honest note missing: %s", w.Body.String())
		}
		okRows := 0
		for _, row := range store.memAudit.rows {
			if row.Action == "ops.dead_letter_replay" && row.OK && strings.Contains(row.Detail, "上游已修复") {
				okRows++
			}
		}
		if okRows != 2 {
			t.Fatalf("success audits = %d, want 2", okRows)
		}
	}

	// 部分失败：一条施写报错 → 503、该条 outcome=error 且各怀审计；其余正常重排
	{
		store := &replayStore{script: map[string]struct {
			requeued bool
			err      error
		}{"bad": {err: errors.New("db down")}}}
		g := &Gate{Settings: g0.Settings, Audit: store}
		w := post(g, ownerToken, `{"event_ids":["good","bad"],"reason":"重试"}`)
		if w.Code != 503 {
			t.Fatalf("partial error %d %s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), `"requeued":1`) || !strings.Contains(w.Body.String(), `"errors":1`) {
			t.Fatalf("counts wrong: %s", w.Body.String())
		}
		if !strings.Contains(w.Body.String(), `"outcome":"error"`) || !strings.Contains(w.Body.String(), `"outcome":"requeued"`) {
			t.Fatalf("per-item outcomes missing: %s", w.Body.String())
		}
		var okCnt, failCnt int
		for _, row := range store.memAudit.rows {
			if row.Action != "ops.dead_letter_replay" {
				continue
			}
			if row.OK {
				okCnt++
			} else {
				failCnt++
			}
		}
		if okCnt != 1 || failCnt != 1 {
			t.Fatalf("audit rows ok=%d fail=%d, want 1/1", okCnt, failCnt)
		}
	}

	// 上限边界：恰 64 条放行（钉死 > 不是 >=）；65 条 raw 含 1 条重复 → 去重后恰 64
	// 放行（钉死先判重、后限量的口径顺序）
	{
		ids64 := make([]string, 64)
		for i := range ids64 {
			ids64[i] = "b" + strings.Repeat("0", i/26+1) + string(rune('a'+i%26))
		}
		store := &replayStore{}
		g := &Gate{Settings: g0.Settings, Audit: store}
		body, _ := json.Marshal(map[string]any{"event_ids": ids64, "reason": "批量边界"})
		if w := post(g, ownerToken, string(body)); w.Code != 200 {
			t.Fatalf("exactly 64 must pass: %d %s", w.Code, w.Body.String())
		}
		if len(store.batchSeen) != 64 {
			t.Fatalf("exact-64 batchSeen = %d, want 64", len(store.batchSeen))
		}

		store2 := &replayStore{}
		g2 := &Gate{Settings: g0.Settings, Audit: store2}
		raw65 := append(append([]string{}, ids64...), ids64[0])
		body2, _ := json.Marshal(map[string]any{"event_ids": raw65, "reason": "批量边界"})
		if w := post(g2, ownerToken, string(body2)); w.Code != 200 {
			t.Fatalf("65 raw with 1 dup must pass after dedupe: %d %s", w.Code, w.Body.String())
		}
		if len(store2.batchSeen) != 64 {
			t.Fatalf("deduped batchSeen = %d, want 64", len(store2.batchSeen))
		}
	}

	// 真重排但审计写不进去：如实报失败，不装成功
	{
		store := &replayStore{auditErr: errors.New("audit db down")}
		g := &Gate{Settings: g0.Settings, Audit: store}
		w := post(g, ownerToken, `{"event_ids":["e1"],"reason":"重试"}`)
		if w.Code != 503 || !strings.Contains(w.Body.String(), "审计写入失败") {
			t.Fatalf("audit fail %d %s", w.Code, w.Body.String())
		}
	}

	// 全部无可重排 → 409；每条各一条 OK=false 审计
	{
		store := &replayStore{script: map[string]struct {
			requeued bool
			err      error
		}{"gone": {requeued: false}}}
		g := &Gate{Settings: g0.Settings, Audit: store}
		w := post(g, ownerToken, `{"event_ids":["gone"],"reason":"重复点击"}`)
		if w.Code != 409 {
			t.Fatalf("all skipped %d %s", w.Code, w.Body.String())
		}
		failRows := 0
		for _, row := range store.memAudit.rows {
			if row.Action == "ops.dead_letter_replay" && !row.OK && strings.Contains(row.Detail, "无可重排死信") {
				failRows++
			}
		}
		if failRows != 1 {
			t.Fatalf("skipped audits = %d, want 1", failRows)
		}
	}
}

func TestDeadLetterReplayOwnerOnlyAuditedAndIdempotent(t *testing.T) {
	g0 := &Gate{Settings: Settings{Secret: "test-replay-secret"}}
	viewerToken, err := Issue(g0.Settings.Secret, User{Name: "viewer", Role: "viewer"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ownerToken, err := Issue(g0.Settings.Secret, User{Name: "owner", Role: "owner"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	post := func(g *Gate, token, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/ops/dead-letter-replay", strings.NewReader(body))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		g.ServeHTTP(w, req)
		return w
	}

	// 未登录 / 非 Owner
	{
		g := &Gate{Settings: g0.Settings, Audit: &replayStore{}}
		if w := post(g, "", `{"event_id":"e1","reason":"r"}`); w.Code != 401 {
			t.Fatalf("anonymous %d", w.Code)
		}
		if w := post(g, viewerToken, `{"event_id":"e1","reason":"r"}`); w.Code != 403 {
			t.Fatalf("viewer %d", w.Code)
		}
	}

	// 审计未启用 → 前置拒绝，一行不改
	{
		off := &replayStoreOff{}
		g := &Gate{Settings: g0.Settings, Audit: off}
		if w := post(g, ownerToken, `{"event_id":"e1","reason":"r"}`); w.Code != 503 {
			t.Fatalf("audit off %d", w.Code)
		}
		if len(off.called) != 0 {
			t.Fatal("audit off must refuse before touching the store")
		}
	}

	// 审计只有 Auditor 能力、未接重排施写口 → 503 如实
	{
		g := &Gate{Settings: g0.Settings, Audit: &memAudit{}}
		if w := post(g, ownerToken, `{"event_id":"e1","reason":"r"}`); w.Code != 503 {
			t.Fatalf("no store %d", w.Code)
		}
	}

	// 请求形状：非 JSON / 非法 event_id / 缺理由 / 理由过长
	{
		store := &replayStore{found: true}
		g := &Gate{Settings: g0.Settings, Audit: store}
		if w := post(g, ownerToken, `not-json`); w.Code != 400 {
			t.Fatalf("bad json %d", w.Code)
		}
		if w := post(g, ownerToken, `{"event_id":"*!!","reason":"r"}`); w.Code != 400 {
			t.Fatalf("bad event id %d", w.Code)
		}
		if w := post(g, ownerToken, `{"event_id":"e1"}`); w.Code != 400 {
			t.Fatalf("missing reason %d", w.Code)
		}
		if w := post(g, ownerToken, `{"event_id":"e1","reason":"`+strings.Repeat("长", replayReasonMax+1)+`"}`); w.Code != 400 {
			t.Fatalf("over-long reason %d", w.Code)
		}
		if len(store.called) != 0 {
			t.Fatal("shape errors must not reach the store")
		}
	}

	// 施写失败 → 503 且审计留失败回执
	{
		store := &replayStore{err: errors.New("db down")}
		g := &Gate{Settings: g0.Settings, Audit: store}
		w := post(g, ownerToken, `{"event_id":"e1","reason":"查错重试"}`)
		if w.Code != 503 {
			t.Fatalf("store err %d", w.Code)
		}
		okFound := false
		for i := range store.memAudit.rows {
			row := store.memAudit.rows[i]
			if row.Action == "ops.dead_letter_replay" && !row.OK {
				okFound = true
			}
		}
		if !okFound {
			t.Fatal("failed replay must be audit-logged as failure")
		}
	}

	// 无可重排死信 → 409 如实（幂等防双重重排）
	{
		store := &replayStore{found: false}
		g := &Gate{Settings: g0.Settings, Audit: store}
		w := post(g, ownerToken, `{"event_id":"e1","reason":"重复点击"}`)
		if w.Code != 409 || !strings.Contains(w.Body.String(), "不存在未投递死信") {
			t.Fatalf("conflict %d %s", w.Code, w.Body.String())
		}
	}

	// 成功 → 200，诚实口径备注 + 成功审计
	{
		store := &replayStore{found: true}
		g := &Gate{Settings: g0.Settings, Audit: store}
		w := post(g, ownerToken, `{"event_id":"e1","reason":"上游已修复，人工核可重放"}`)
		if w.Code != 200 {
			t.Fatalf("ok %d %s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "重排不等于投递成功") {
			t.Fatalf("honest note missing: %s", w.Body.String())
		}
		if len(store.called) != 1 || store.called[0] != "e1" {
			t.Fatalf("store calls %+v", store.called)
		}
		okFound := false
		for i := range store.memAudit.rows {
			row := store.memAudit.rows[i]
			if row.Action == "ops.dead_letter_replay" && row.OK && strings.Contains(row.Detail, "上游已修复") {
				okFound = true
			}
		}
		if !okFound {
			t.Fatal("successful replay must be audit-logged with the approval reason")
		}
	}

	// GET 不放行
	{
		g := &Gate{Settings: g0.Settings, Audit: &replayStore{found: true}}
		req := httptest.NewRequest(http.MethodGet, "/api/ops/dead-letter-replay", nil)
		req.Header.Set("Authorization", "Bearer "+ownerToken)
		w := httptest.NewRecorder()
		g.ServeHTTP(w, req)
		if w.Code != 405 {
			t.Fatalf("GET %d %s", w.Code, w.Body.String())
		}
	}
}
