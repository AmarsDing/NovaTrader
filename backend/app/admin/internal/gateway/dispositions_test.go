package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type dispositionAudit struct {
	memAudit
	listed []Disposition
	wrote  []Disposition
}

func (d *dispositionAudit) ListDispositions(context.Context, string, string, int) ([]Disposition, error) {
	return d.listed, nil
}

func (d *dispositionAudit) WriteDisposition(_ context.Context, row Disposition) error {
	d.wrote = append(d.wrote, row)
	return nil
}

func TestDispositionReadBoundedAndAnnotatedOnly(t *testing.T) {
	g := &Gate{Settings: Settings{Secret: "test-disposition-secret"}, Audit: &dispositionAudit{
		listed: []Disposition{{ID: 1, EventID: "e1", TraceID: "t1", Action: "acknowledged", Note: "已核实", Actor: "owner", CreatedAt: "2026-10-18T01:00:00Z"}},
	}}
	token, err := Issue(g.Settings.Secret, User{Name: "viewer", Role: "viewer"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		path string
		auth bool
		want int
	}{
		{"/api/ops/dispositions?event_id=e1", false, 401},
		{"/api/ops/dispositions", true, 400},
		{"/api/ops/dispositions?event_id=*", true, 400},
		{"/api/ops/dispositions?event_id=e1", true, 200},
		{"/api/ops/dispositions?trace_id=t1", true, 200},
	} {
		req := httptest.NewRequest(http.MethodGet, tt.path, nil)
		if tt.auth {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		g.ServeHTTP(w, req)
		if w.Code != tt.want {
			t.Fatalf("%s %d %s", tt.path, w.Code, w.Body.String())
		}
		if tt.want == 200 && !strings.Contains(w.Body.String(), "不回放事件") {
			t.Fatalf("annotation-only note missing: %s", w.Body.String())
		}
	}
}

func TestDispositionWriteOwnerOnlyClosedActionAndAudited(t *testing.T) {
	store := &dispositionAudit{}
	g := &Gate{Settings: Settings{Secret: "test-disposition-secret"}, Audit: store}
	viewerToken, err := Issue(g.Settings.Secret, User{Name: "viewer", Role: "viewer"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ownerToken, err := Issue(g.Settings.Secret, User{Name: "owner", Role: "owner"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	post := func(token string, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/ops/dispositions", strings.NewReader(body))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		g.ServeHTTP(w, req)
		return w
	}
	if w := post("", `{"event_id":"e1","action":"acknowledged"}`); w.Code != 401 {
		t.Fatalf("anonymous %d", w.Code)
	}
	if w := post(viewerToken, `{"event_id":"e1","action":"acknowledged"}`); w.Code != 403 {
		t.Fatalf("viewer %d", w.Code)
	}
	if w := post(ownerToken, `{"event_id":"e1","action":"replay"}`); w.Code != 400 {
		t.Fatalf("consequential verb must not be accepted: %d", w.Code)
	}
	if w := post(ownerToken, `{"event_id":"*!!","action":"acknowledged"}`); w.Code != 400 {
		t.Fatalf("unsafe event id %d", w.Code)
	}
	if w := post(ownerToken, `{"event_id":"e1","action":"acknowledged","note":"`+strings.Repeat("长", dispositionNoteMax+1)+`"}`); w.Code != 400 {
		t.Fatalf("over-long note %d", w.Code)
	}
	if w := post(ownerToken, `{"event_id":"e1","trace_id":"t1","action":"investigating","note":"以下游对账为准"}`); w.Code != 200 {
		t.Fatalf("valid %d %s", w.Code, w.Body.String())
	}
	if len(store.wrote) != 1 || store.wrote[0].Actor != "owner" || store.wrote[0].Action != "investigating" || store.wrote[0].EventID != "e1" || store.wrote[0].TraceID != "t1" {
		t.Fatalf("stored %+v", store.wrote)
	}
	found := false
	for i := range store.memAudit.rows {
		if store.memAudit.rows[i].Action == "ops.disposition" && store.memAudit.rows[i].OK {
			found = true
		}
	}
	if !found {
		t.Fatal("disposition write must be audit-logged")
	}
}

type dispositionAuditOff struct{ dispositionAudit }

func (d *dispositionAuditOff) Enabled() bool { return false }

// 审计未启用时前置拒绝：连留痕行也不写（审计写不进去就不放行）。
func TestDispositionWriteRefusedBeforeWriteWhenAuditOff(t *testing.T) {
	off := &dispositionAuditOff{}
	g := &Gate{Settings: Settings{Secret: "test-disposition-secret"}, Audit: off}
	ownerToken, err := Issue(g.Settings.Secret, User{Name: "owner", Role: "owner"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/ops/dispositions", strings.NewReader(`{"event_id":"e1","action":"acknowledged"}`))
	req.Header.Set("Authorization", "Bearer "+ownerToken)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)
	if w.Code != 503 {
		t.Fatalf("want 503, got %d %s", w.Code, w.Body.String())
	}
	if len(off.wrote) != 0 {
		t.Fatal("nothing may be written when the audit trail is off")
	}
}
