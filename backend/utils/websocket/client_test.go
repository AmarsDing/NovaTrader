package websocket

import (
	"sync"
	"testing"
)

func TestClientConcurrentCloseAndSend(t *testing.T) {
	client := &Client{Send: make(chan *WResponse, 100)}
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); client.TrySend(&WResponse{Event: "test"}) }()
		go func() { defer wg.Done(); client.close() }()
	}
	wg.Wait()
	if !client.IsClosed() || client.TrySend(&WResponse{Event: "late"}) {
		t.Fatal("closed transport accepted a message")
	}
	for range client.Send {
	} // drained channel must be closed exactly once
}

func TestClientBoundedTrySend(t *testing.T) {
	client := &Client{Send: make(chan *WResponse, 1)}
	if !client.TrySend(&WResponse{Event: "first"}) || client.TrySend(&WResponse{Event: "overflow"}) {
		t.Fatal("unbounded send")
	}
	client.close()
	if (&Client{}).IsClosed() || !(*Client)(nil).IsClosed() {
		t.Fatal("unexpected close state")
	}
}
