package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-kratos/kratos/v2/log"
	"google.golang.org/protobuf/encoding/protojson"
	v1 "server/api/platform/v1"
	"server/app/trade/internal/biz"
	"server/app/trade/internal/service"
	"server/conf"
	"server/pkg/observe"
)

func TestHTTPObservationMountedWithClosedTradeContract(t *testing.T) {
	e := biz.NewEngine(biz.NewMemStore(), nil, biz.Settings{}, log.DefaultLogger)
	svc := service.NewTradeService(e, nil, nil)
	srv := NewHTTPServer(&conf.Trade{}, svc, service.NewAdmissionService(svc), service.NewNavService(svc), log.DefaultLogger)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/internal/observability", nil))
	if w.Code != 200 {
		t.Fatalf("HTTP %d %s", w.Code, w.Body)
	}
	out := &v1.ConsumerObservation{}
	if err := protojson.Unmarshal(w.Body.Bytes(), out); err != nil {
		t.Fatal(err)
	}
	if err := observe.ValidateSnapshot("trade", out); err != nil {
		t.Fatal(err)
	}
	if len(out.Operations) < 12 || out.Scope != "process_consumer_operations" {
		t.Fatalf("bad contract %v", out)
	}
}
