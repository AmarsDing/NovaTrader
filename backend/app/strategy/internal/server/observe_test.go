package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	v1 "server/api/platform/v1"
	"server/app/strategy/internal/biz"
	"server/app/strategy/internal/service"
	"server/conf"
	"server/pkg/observe"

	"github.com/go-kratos/kratos/v2/log"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestHTTPObservationMountedWithClosedStrategyContract(t *testing.T) {
	uc := biz.NewUsecase(biz.NewSettings(nil), nil, nil, nil, nil, nil, nil, nil, nil, log.DefaultLogger)
	srv := NewHTTPServer(&conf.Strategy{}, service.NewStrategyService(uc), log.DefaultLogger)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/internal/observability", nil))
	if w.Code != 200 {
		t.Fatalf("HTTP %d %s", w.Code, w.Body)
	}
	out := &v1.ConsumerObservation{}
	if err := protojson.Unmarshal(w.Body.Bytes(), out); err != nil {
		t.Fatal(err)
	}
	if err := observe.ValidateSnapshot("strategy", out); err != nil {
		t.Fatal(err)
	}
	if len(out.Operations) != 9 || out.Scope != "process_consumer_operations" {
		t.Fatalf("bad contract %v", out)
	}
}
