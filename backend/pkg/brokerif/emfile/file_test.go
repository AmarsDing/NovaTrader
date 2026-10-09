package emfile

import (
	"os"
	"path/filepath"
	"strings"
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
	if filepath.Base(path) != "2026-10-09_09-41-03.120.order.csv" {
		t.Fatal(filepath.Base(path))
	}
}
