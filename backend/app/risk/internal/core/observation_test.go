package core

import (
	"context"
	"io"
	"testing"

	"server/app/risk/internal/biz"
	"server/pkg/events"
	"server/pkg/observe"

	"github.com/go-kratos/kratos/v2/log"
)

func TestAccountObservationCannotTreatWrongSourceOrInvalidStateAsSuccess(t *testing.T) {
	logger := log.NewStdLogger(io.Discard)
	r := NewRunner(biz.NewEngine(nil, nil, nil, nil, logger), biz.NewAlertCenter(nil, nil, logger), nil, nil, nil, logger)
	reg := observe.ForService("risk")
	var rejected, failed, succeeded uint64
	for _, op := range reg.Snapshot().Operations {
		if op.Key == "account_consume" {
			rejected, failed, succeeded = op.Total.Rejected, op.Total.Failed, op.Total.Succeeded
		}
	}
	r.onAccount(context.Background(), events.Envelope{Source: "untrusted", Subject: events.SubjectTradeAccount})
	r.onAccount(context.Background(), events.Envelope{Source: "trade", Subject: events.SubjectTradeAccount, Payload: []byte(`{"account_type":"SIM"}`)})
	for _, op := range reg.Snapshot().Operations {
		if op.Key != "account_consume" {
			continue
		}
		if op.Total.Rejected != rejected+1 || op.Total.Failed != failed+1 || op.Total.Succeeded != succeeded || op.Inflight != 0 {
			t.Fatal(op)
		}
		return
	}
	t.Fatal("account consumer operation missing")
}
