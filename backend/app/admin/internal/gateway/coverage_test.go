package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type coverageReader struct {
	memAudit
	coverage *TraceCoverage
	err      error
	limit    int
}

func (s *coverageReader) ReadTraceCoverage(_ context.Context, limit int) (*TraceCoverage, error) {
	s.limit = limit
	return s.coverage, s.err
}

func TestTraceCoverageReadOnlyPrincipalGated(t *testing.T) {
	g0 := &Gate{Settings: Settings{Secret: "test-coverage-secret"}}
	viewerToken, err := Issue(g0.Settings.Secret, User{Name: "viewer", Role: "viewer"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	get := func(g *Gate, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/ops/trace-coverage", nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		g.ServeHTTP(w, req)
		return w
	}

	// 未登录 → 401
	if w := get(&Gate{Settings: g0.Settings, Audit: &coverageReader{}}, ""); w.Code != 401 {
		t.Fatalf("anonymous %d", w.Code)
	}

	// 未接施读口 → 503 如实
	if w := get(&Gate{Settings: g0.Settings, Audit: &memAudit{}}, viewerToken); w.Code != 503 {
		t.Fatalf("no reader %d", w.Code)
	}

	// 施读失败 → 503
	{
		store := &coverageReader{err: errors.New("db down")}
		if w := get(&Gate{Settings: g0.Settings, Audit: store}, viewerToken); w.Code != 503 {
			t.Fatalf("reader err %d", w.Code)
		}
	}

	// viewer 可读（只读口径，与观测同权）；窗口上限常量如实传到 data 层，响应形状齐全
	{
		store := &coverageReader{coverage: &TraceCoverage{
			SchemaVersion: 1, Available: true, Scope: "durable_outbox_history", AsOf: "2026-10-19T00:00:00Z",
			WindowLimit: 2000, WindowRows: 2, OldestInWindow: "2026-10-18T23:00:00Z",
			Totals: SourceCoverage{Total: 2, WithTrace: 2, Children: 1, OrphanParents: 1},
			BySource: []SourceCoverage{
				{Source: "strategy", Total: 1, WithTrace: 1},
				{Source: "trade", Total: 1, WithTrace: 1, Children: 1, OrphanParents: 1},
			},
		}}
		w := get(&Gate{Settings: g0.Settings, Audit: store}, viewerToken)
		if w.Code != 200 {
			t.Fatalf("viewer read %d %s", w.Code, w.Body.String())
		}
		if store.limit != traceCoverageWindowLimit {
			t.Fatalf("window limit not enforced by gateway: %d", store.limit)
		}
		var body TraceCoverage
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("body shape: %v %s", err, w.Body.String())
		}
		if body.Scope != "durable_outbox_history" || !body.Available || body.Totals.Children != 1 || len(body.BySource) != 2 {
			t.Fatalf("unexpected coverage body: %+v", body)
		}
	}

	// 非 GET → 405
	{
		req := httptest.NewRequest(http.MethodPost, "/api/ops/trace-coverage", nil)
		req.Header.Set("Authorization", "Bearer "+viewerToken)
		w := httptest.NewRecorder()
		(&Gate{Settings: g0.Settings, Audit: &coverageReader{coverage: &TraceCoverage{}}}).ServeHTTP(w, req)
		if w.Code != 405 {
			t.Fatalf("POST %d", w.Code)
		}
	}
}
