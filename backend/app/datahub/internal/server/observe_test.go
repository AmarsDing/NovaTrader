package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	v1 "server/api/platform/v1"
	"server/app/datahub/internal/biz"
	"server/app/datahub/internal/service"
	"server/conf"
	"server/pkg/observe"

	"github.com/go-kratos/kratos/v2/log"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestHTTPObservationMountedWithClosedDatahubContract(t *testing.T) {
	srv := NewHTTPServer(&conf.Datahub{}, service.NewDatahubService(&biz.Collector{}), log.DefaultLogger)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/internal/observability", nil))
	if w.Code != 200 {
		t.Fatalf("HTTP %d %s", w.Code, w.Body)
	}
	out := &v1.ConsumerObservation{}
	if err := protojson.Unmarshal(w.Body.Bytes(), out); err != nil {
		t.Fatal(err)
	}
	if err := observe.ValidateSnapshot("datahub", out); err != nil {
		t.Fatal(err)
	}
	if len(out.Operations) != 5 || out.Scope != "process_consumer_operations" {
		t.Fatalf("bad contract %v", out)
	}
}
