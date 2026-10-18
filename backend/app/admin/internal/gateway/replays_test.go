package gateway

import (
	"context"
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
}

func (s *replayStore) RequeueDeadLetter(_ context.Context, eventID string) (bool, error) {
	s.called = append(s.called, eventID)
	return s.found, s.err
}

type replayStoreOff struct{ replayStore }

func (s *replayStoreOff) Enabled() bool { return false }

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
