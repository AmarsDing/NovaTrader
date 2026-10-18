package emfile

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"server/pkg/brokerif"
)

func TestWriteOrder(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 9, 9, 41, 3, 120_000_000, time.Local)
	path, err := WriteOrder(dir, "ACC1", brokerif.Order{
		ClientID: "sid-1", Symbol: "600519.SH", Side: brokerif.Buy, Price: 10.5, Volume: 200,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if !strings.Contains(text, "sid,account_id,symbol,volume,order_type,order_business(order_biz),price,comment") {
		t.Fatal(text)
	}
	if !strings.Contains(text, "sid-1,ACC1,SHSE.600519,200,1,1,10.50,") {
		t.Fatal(text)
	}
	if _, err := os.Stat(path + ".fin"); err != nil {
		t.Fatal(err)
	}
	if len(filepath.Base(path)) != 64+len(".order.csv") {
		t.Fatal(filepath.Base(path))
	}
}

func TestConcurrentSameSIDPublishesOnce(t *testing.T) {
	dir := t.TempDir()
	order := brokerif.Order{ClientID: "sid-race", Symbol: "600519.SH", Side: brokerif.Buy, Price: 10.5, Volume: 200}
	var wait sync.WaitGroup
	errorsSeen := make(chan error, 32)
	for i := 0; i < 32; i++ {
		wait.Add(1)
		go func() { defer wait.Done(); _, err := WriteOrder(dir, "ACC1", order, time.Now()); errorsSeen <- err }()
	}
	wait.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		if err != nil && !errors.Is(err, ErrUnknown) {
			t.Fatal(err)
		}
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.order.csv"))
	if err != nil || len(files) != 1 {
		t.Fatalf("expected one CSV, got %d: %v", len(files), err)
	}
}

func TestIdentitySurvivesTerminalCleanup(t *testing.T) {
	dir := t.TempDir()
	order := brokerif.Order{ClientID: "sid-1", Symbol: "600519.SH", Side: brokerif.Buy, Price: 10.5, Volume: 200}
	path, err := WriteOrder(dir, "ACC1", order, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteOrder(dir, "ACC1", order, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("consumed CSV was republished")
	}
	order.Volume = 300
	if _, err := WriteOrder(dir, "ACC1", order, time.Now()); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
}

func TestRejectUnsafeAndUnverifiedOrders(t *testing.T) {
	base := brokerif.Order{ClientID: "sid-1", Symbol: "600519.SH", Side: brokerif.Buy, Price: 10.5, Volume: 200}
	for _, price := range []float64{math.NaN(), math.Inf(1), 0, -1, 10.501} {
		value := base
		value.Price = price
		if _, err := encodeOrder("ACC1", value); err == nil {
			t.Fatalf("accepted price %v", price)
		}
	}
	for _, id := range []string{"a,evil", "../escape", "x\r\nnew", ""} {
		value := base
		value.ClientID = id
		if _, err := encodeOrder("ACC1", value); err == nil {
			t.Fatal("accepted unsafe identity")
		}
	}
	base.Symbol = "830001.BJ"
	if _, err := encodeOrder("ACC1", base); err == nil {
		t.Fatal("unverified BJ prefix")
	}
}
