package core

import (
	"reflect"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"server/app/market/internal/biz"
	"server/pkg/events"
	"server/pkg/market"
)

func TestTradeWatchLeaseHandlerRestoresAndReplacesStream(t *testing.T) {
	uc := biz.NewUsecase(nil, nil, nil, biz.Config{}, log.DefaultLogger)
	w := &Worker{uc: uc}
	owner := "trade." + uuid.NewString()
	deliver := func(source string, lease market.TradeWatchLease) {
		env, err := events.New(source, events.SubjectMarketWatchlist, "", lease)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := env.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		w.onWatchlist(&nats.Msg{Subject: events.SubjectMarketWatchlist, Data: raw})
	}
	lease := market.TradeWatchLease{Version: 1, Owner: owner, Symbols: []string{"600519.SH"}, TTLSeconds: 120}
	deliver("other", lease)
	if got := uc.Watch().Symbols(time.Now()); len(got) != 0 {
		t.Fatal("wrong source registered")
	}
	deliver("trade", lease)
	if got := uc.Watch().Symbols(time.Now()); !reflect.DeepEqual(got, lease.Symbols) {
		t.Fatalf("got=%v", got)
	}
	lease.Symbols = []string{"000001.SZ"}
	deliver("trade", lease)
	if got := uc.Watch().Symbols(time.Now()); !reflect.DeepEqual(got, lease.Symbols) {
		t.Fatalf("old symbols retained=%v", got)
	}
	if got := uc.Watch().Symbols(time.Now().Add(121 * time.Second)); len(got) != 0 {
		t.Fatal("expired process retained")
	}
	deliver("trade", lease)
	lease.Symbols = []string{}
	deliver("trade", lease)
	if got := uc.Watch().Symbols(time.Now()); len(got) != 0 {
		t.Fatal("empty ledger did not remove lease")
	}
}
