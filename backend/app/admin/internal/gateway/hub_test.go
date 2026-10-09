package gateway

import (
	"encoding/json"
	"testing"

	"server/utils/websocket"

	"github.com/go-kratos/kratos/v2/log"
)

func TestHubResumeAndCoalesce(t *testing.T) {
	h := NewHub(log.DefaultLogger)
	h.Publish("signal", "signal", map[string]any{"id": 1})
	h.Publish("signal", "signal", map[string]any{"id": 2})
	client := &websocket.Client{Send: make(chan *websocket.WResponse, 4)}
	got := h.subscribe(client, []string{"signal"}, nil)
	if len(got) != 0 {
		t.Fatalf("first subscribe replayed %d", len(got))
	}
	replay := h.subscribe(client, []string{"signal"}, map[string]int64{"signal": 1})
	if len(replay) != 1 || replay[0].Seq != 2 {
		t.Fatalf("replay %+v", replay)
	}
	msg := replay[0].response()
	if msg.Topic != "signal" || msg.Seq != 2 || msg.Event != "signal" {
		t.Fatalf("frame %+v", msg)
	}
	body, _ := msg.Payload.(map[string]any)
	if body["id"] != float64(2) && body["id"] != 2 {
		t.Fatalf("payload %#v", msg.Payload)
	}

	h.Publish("market", "market", map[string]int{"n": 1})
	h.Publish("market", "market", map[string]int{"n": 2})
	h.flushMarket()
	market := h.subscribe(client, []string{"market"}, map[string]int64{"market": 0})
	if len(market) != 1 {
		t.Fatalf("market frames %d", len(market))
	}
	raw, _ := json.Marshal(market[0].response().Payload)
	if string(raw) != `{"n":2}` {
		t.Fatalf("market payload %s", raw)
	}
}

func TestHubOnText(t *testing.T) {
	h := NewHub(log.DefaultLogger)
	h.Publish("alert", "notify", map[string]string{"title": "停机"})
	client := &websocket.Client{Send: make(chan *websocket.WResponse, 4)}
	if !h.onText(client, []byte(`{"op":"sub","topics":["alert","nope"],"resume":{"alert":0}}`)) {
		t.Fatal("sub not handled")
	}
	msg := <-client.Send
	if msg.Topic != "alert" || msg.Event != "notify" || msg.Seq != 1 {
		t.Fatalf("push %+v", msg)
	}
	ack := <-client.Send
	if ack.Event != "sub" {
		t.Fatalf("ack %+v", ack)
	}
	if h.onText(client, []byte(`{"e":"ping"}`)) {
		t.Fatal("old protocol consumed")
	}
}
