package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	v1 "server/api/admin/v1"
	"strings"
	"testing"
	"time"
)

type traceAudit struct{ memAudit }

func (*traceAudit) ReadTrace(context.Context, string, int) (*v1.GetTraceReply, error) {
	return &v1.GetTraceReply{SchemaVersion: 1, Available: true, Scope: "durable_outbox_history", Events: []*v1.TraceEvent{{EventId: "e1", TraceId: "t1", CausationId: "SECRET\nUNSAFE", EntityId: "SECRET\nUNSAFE", Source: "unsafe-provider", Subject: "trade.order", State: "published"}}}, nil
}
func TestTraceReadAuthBoundedFilterAndProjection(t *testing.T) {
	g := &Gate{Settings: Settings{Secret: "test-trace-secret"}, Audit: &traceAudit{}}
	token, err := Issue(g.Settings.Secret, User{Name: "viewer", Role: "viewer"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		path string
		auth bool
		want int
	}{{"/api/ops/traces", false, 401}, {"/api/ops/traces?trace_id=*", true, 400}, {"/api/ops/traces?limit=-1", true, 400}, {"/api/ops/traces?trace_id=t1", true, 200}} {
		req := httptest.NewRequest(http.MethodGet, tt.path, nil)
		if tt.auth {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		g.ServeHTTP(w, req)
		if w.Code != tt.want {
			t.Fatalf("%s %d %s", tt.path, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "SECRET") || strings.Contains(w.Body.String(), "unsafe-provider") {
			t.Fatal("unsafe metadata escaped projection")
		}
	}
}
