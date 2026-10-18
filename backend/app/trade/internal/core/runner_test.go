package core

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"server/app/trade/internal/biz"
	"server/pkg/events"
	"server/pkg/market"
	"server/pkg/tradecal"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/nats-io/nats.go"
)

type allowCheck struct{}

func (allowCheck) Check(_ context.Context, in biz.CheckIn) (biz.CheckOut, error) {
	return biz.CheckOut{Approved: true, Volume: in.Volume}, nil
}

func TestRuntimeMinuteBarUsesKnownLimitsForSealedMarkets(t *testing.T) {
	for _, side := range []string{"buy", "sell"} {
		t.Run(side, func(t *testing.T) {
			ctx := context.Background()
			mem := biz.NewMemStore()
			if err := mem.Commit(ctx, biz.Batch{Cash: &biz.Cash{Book: biz.BookPaper, Cash: 1000000, DayStartDate: time.Now().In(tradecal.Shanghai()).Format("2006-01-02"), DayStartEquity: 1000000}, Positions: []biz.Position{{Book: biz.BookPaper, Symbol: "000001.SZ", Quantity: 100, Available: 100, AvgCost: 10, LastPrice: 10}}}); err != nil {
				t.Fatal(err)
			}
			e := biz.NewEngine(mem, allowCheck{}, biz.Settings{}, log.DefaultLogger)
			price := 11.
			if side == "sell" {
				price = 9
			}
			if _, err := e.Place(ctx, biz.PlaceRequest{ClientOrderID: side, Account: "SIM", Symbol: "000001.SZ", Side: side, Price: price, Volume: 100}); err != nil {
				t.Fatal(err)
			}
			barTime := time.Now().UTC().Truncate(time.Minute).Add(2 * time.Minute)
			up, down := 11., 9.
			bar := market.Bar{Symbol: "000001.SZ", Time: barTime, Open: price, High: price, Low: price, Close: price, Volume: 100000, LimitKnown: true, LimitUp: &up, LimitDown: &down}
			env, err := events.New("market", events.SubjectMarketBar, "", map[string]any{"bars": []market.Bar{bar}})
			if err != nil {
				t.Fatal(err)
			}
			raw, err := env.Marshal()
			if err != nil {
				t.Fatal(err)
			}
			r := &Runner{e: e, now: func() time.Time { return barTime.Add(time.Minute) }, log: log.NewHelper(log.DefaultLogger)}
			r.onBars(&nats.Msg{Subject: events.SubjectMarketBar, Data: raw})
			if fills, _ := e.Fills(ctx, "SIM"); len(fills) != 0 {
				t.Fatalf("sealed %s limit filled: %+v", side, fills)
			}
		})
	}
}

func TestMinuteBarEnvelopeReachesSimulation(t *testing.T) {
	tests := []struct {
		name          string
		valid         bool
		subject       string
		missingLimits bool
		source        string
		early         bool
	}{
		{"wrapped market bar", true, events.SubjectMarketBar, false, "market", false},
		{"wrong subject", false, events.SubjectMarketState, false, "market", false},
		{"bare payload", false, "", false, "market", false},
		{"unknown price limits", false, events.SubjectMarketBar, true, "market", false},
		{"wrong producer", false, events.SubjectMarketBar, false, "intel", false},
		{"minute not closed", false, events.SubjectMarketBar, false, "market", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := biz.NewMemStore()
			e := biz.NewEngine(store, allowCheck{}, biz.Settings{}, log.DefaultLogger)
			// 默认模拟撮合在开盘价上加一跳滑点，限价必须覆盖 10.01。
			if _, err := e.Place(context.Background(), biz.PlaceRequest{ClientOrderID: "test", Account: "SIM", Symbol: "000001.SZ", Side: "buy", Price: 10.05, Volume: 100}); err != nil {
				t.Fatal(err)
			}
			barTime := time.Now().UTC().Truncate(time.Minute).Add(2 * time.Minute)
			up, down := 11., 9.
			payload := map[string]any{"bars": []market.Bar{{Symbol: "000001.SZ", Time: barTime, Open: 10, High: 10, Low: 10, Close: 10, Volume: 100000, LimitKnown: !tt.missingLimits, LimitUp: &up, LimitDown: &down}}}
			body, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			if tt.subject != "" {
				env, err := events.New(tt.source, tt.subject, "trace", payload)
				if err != nil {
					t.Fatal(err)
				}
				body, err = env.Marshal()
				if err != nil {
					t.Fatal(err)
				}
			}
			closedAt := barTime.Add(time.Minute)
			if tt.early {
				closedAt = barTime.Add(30 * time.Second)
			}
			r := &Runner{e: e, now: func() time.Time { return closedAt }, log: log.NewHelper(log.DefaultLogger)}
			r.onBars(&nats.Msg{Subject: events.SubjectMarketBar, Data: body})
			fills, err := e.Fills(context.Background(), "SIM")
			if err != nil {
				t.Fatal(err)
			}
			if (len(fills) > 0) != tt.valid {
				t.Fatalf("fills=%+v valid=%v", fills, tt.valid)
			}
			if tt.valid && (len(fills) != 1 || fills[0].Qty != 100 || fills[0].Price != 10.01) {
				t.Fatalf("unexpected default simulation fill: %+v", fills)
			}
			if tt.missingLimits {
				// The first rejected event must not consume the durable minute.
				bars := payload["bars"].([]market.Bar)
				bars[0].LimitKnown = true
				env, err := events.New("market", events.SubjectMarketBar, "", payload)
				if err != nil {
					t.Fatal(err)
				}
				raw, err := env.Marshal()
				if err != nil {
					t.Fatal(err)
				}
				r.onBars(&nats.Msg{Subject: events.SubjectMarketBar, Data: raw})
				if retried, _ := e.Fills(context.Background(), "SIM"); len(retried) != 1 {
					t.Fatalf("unknown metadata consumed cursor: %+v", retried)
				}
			}
		})
	}
}
