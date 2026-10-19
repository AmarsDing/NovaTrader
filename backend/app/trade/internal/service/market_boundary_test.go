package service

import (
	"context"
	kerrors "github.com/go-kratos/kratos/v2/errors"
	v1 "server/api/trade/v1"
	"testing"
)

func TestPublicInputsCannotSynthesizeSIMFills(t *testing.T) {
	// Reject before an engine or any ledger is available.
	s := NewTradeService(nil, nil)
	if _, err := s.PlaceOrder(context.Background(), &v1.PlaceOrderRequest{Bar: &v1.Bar{Open: 10, High: 10, Low: 10, Volume: 100000}}); kerrors.Code(err) != 400 {
		t.Fatalf("client bar accepted: %v", err)
	}
	if _, err := s.ApplyBar(context.Background(), &v1.ApplyBarRequest{Account: "SIM", Symbol: "600519.SH", Bar: &v1.Bar{Open: 10}}); kerrors.Code(err) != 403 {
		t.Fatalf("public matcher accepted: %v", err)
	}
}
