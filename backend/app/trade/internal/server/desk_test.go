package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"server/app/trade/internal/biz"
	"server/app/trade/internal/service"

	"github.com/go-kratos/kratos/v2/log"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
)

type allowRisk struct{}

func (allowRisk) Check(context.Context, biz.CheckIn) (biz.CheckOut, error) {
	return biz.CheckOut{Approved: true}, nil
}

func TestDeskRoutes(t *testing.T) {
	mem := biz.NewMemStore()
	engine := biz.NewEngine(mem, allowRisk{}, biz.Settings{InitialCash: 1_000_000}, log.DefaultLogger)
	q := biz.Quote{Open: 10, High: 10.2, Low: 9.9, Close: 10.05, Volume: 100000, LimitUp: 11, LimitDown: 9}
	if _, err := engine.Place(context.Background(), biz.PlaceRequest{
		ClientOrderID: "d1", Account: "SIM", Symbol: "600519.SH", Side: "buy",
		Price: 10.05, Volume: 100, Quote: &q,
	}); err != nil {
		t.Fatal(err)
	}
	srv := khttp.NewServer()
	mountDesk(srv, service.NewTradeService(engine))
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	orders := getJSON(t, ts.URL+"/v1/trade/orders?account=SIM")
	list, _ := orders["orders"].([]any)
	if len(list) != 1 {
		t.Fatalf("orders %#v", orders)
	}
	fills := getJSON(t, ts.URL+"/v1/trade/fills?account=SIM")
	got, _ := fills["fills"].([]any)
	if len(got) != 1 {
		t.Fatalf("fills %#v", fills)
	}

	res, err := http.Post(ts.URL+"/v1/trade/orders/missing/confirm", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("confirm %d %s", res.StatusCode, body)
	}
}

func getJSON(t *testing.T, raw string) map[string]any {
	t.Helper()
	res, err := http.Get(raw)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 {
		t.Fatalf("%s %d %s", raw, res.StatusCode, body)
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
