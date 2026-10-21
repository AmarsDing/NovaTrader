package remote

import (
	"encoding/json"
	"strings"
	"testing"
)

// M13-T04 快照解析单测：载荷形状对齐 broker-gw reader.normalize 的输出，
// 身份回声/行级校验/追加流与全量流的新鲜度门逐个差分化断言。

func frame(t *testing.T, mutate func(map[string]any)) json.RawMessage {
	t.Helper()
	f := map[string]any{
		"protocol":      Protocol,
		"mode":          "ISOLATED",
		"account_id":    "55001234",
		"reports_ready": true,
		"reasons":       []string{},
		"can_open_live": false,
		"observed_at":   1761093600.0,
		"reports": map[string]any{
			"cash.csv": map[string]any{
				"name": "cash.csv", "available": true, "fresh": true,
				"records": []any{map[string]any{"balance": "100000.00", "available": "99994.96", "frozen": "0.00"}},
			},
			"position.csv": map[string]any{
				"name": "position.csv", "available": true, "fresh": true,
				"records": []any{map[string]any{"symbol": "SHSE.600519", "volume": 1000, "volume_today": 200, "available_now": 800}},
			},
			"order_status.csv": map[string]any{
				"name": "order_status.csv", "available": true, "fresh": true,
				"records": []any{map[string]any{
					"sid": "SID1", "symbol": "SHSE.600000", "state": "PARTIAL", "order_business": "1",
					"price": "10.00", "volume": 1000, "filled_volume": 400,
				}},
			},
			"order_status_change.csv": map[string]any{
				"name": "order_status_change.csv", "available": true, "fresh": nil,
				"records": []any{map[string]any{
					"sid": "SID2", "symbol": "SZSE.000001", "state": "CANCELLED", "order_business": "1",
					"price": "8.00", "volume": 100, "filled_volume": 0,
				}},
			},
			"execution_report.csv": map[string]any{
				"name": "execution_report.csv", "available": true, "fresh": nil,
				"records": []any{
					map[string]any{"sid": "SID1", "exec_id": "EX9", "symbol": "SHSE.600000", "kind": "FILL", "order_business": "1", "price": "10.00", "amount": "4000.00", "volume": 400},
					map[string]any{"sid": "SID2", "kind": "CANCEL_REJECTED"},
				},
			},
		},
	}
	if mutate != nil {
		mutate(f)
	}
	raw, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestParseSnapshotValidFrame(t *testing.T) {
	snap, err := ParseSnapshot(frame(t, nil), "55001234")
	if err != nil {
		t.Fatal(err)
	}
	if !snap.Ready || snap.AccountID != "55001234" || snap.Observed.IsZero() {
		t.Fatalf("header: %+v", snap)
	}
	// 变更流并入委托节（同名回报并集）。
	if len(snap.Orders.Records) != 2 || !snap.Orders.Ready() {
		t.Fatalf("orders: %+v", snap.Orders)
	}
	// 追加流 fresh=null 只要 available 即就绪；全量流 fresh=true 就绪。
	if !snap.Executions.Ready() || len(snap.Executions.Records) != 2 {
		t.Fatalf("executions: %+v", snap.Executions)
	}
	if !snap.Positions.Ready() || !snap.Cash.Ready() {
		t.Fatalf("full streams ready: %+v / %+v", snap.Positions, snap.Cash)
	}
	row := snap.Orders.Records[0]
	if row.SID != "SID1" || row.FilledVolume != 400 || row.Price != "10.00" {
		t.Fatalf("order row: %+v", row)
	}
	if side, err := row.Side(); err != nil || side != "buy" {
		t.Fatalf("business side: %v %v", side, err)
	}
	fill := snap.Executions.Records[0]
	if fill.ExecID != "EX9" || fill.Kind != "FILL" || fill.Amount != "4000.00" {
		t.Fatalf("fill row: %+v", fill)
	}
	if pos := snap.Positions.Records[0]; pos.Symbol != "SHSE.600519" || pos.AvailableNow != 800 {
		t.Fatalf("position row: %+v", pos)
	}
	cash := snap.Cash.Records[0]
	if cash.Balance != "100000.00" || cash.Frozen != "0.00" {
		t.Fatalf("cash row keeps decimal strings: %+v", cash)
	}
}

func TestParseSnapshotIdentityMismatch(t *testing.T) {
	cases := map[string]func(map[string]any){
		"wrong account":  func(f map[string]any) { f["account_id"] = "5599" },
		"wrong protocol": func(f map[string]any) { f["protocol"] = "eastmoney-file-csv-v0" },
		"not isolated":   func(f map[string]any) { f["mode"] = "SHARED" },
		"can open":       func(f map[string]any) { f["can_open_live"] = true },
	}
	for name, mutate := range cases {
		if _, err := ParseSnapshot(frame(t, mutate), "55001234"); err == nil || !strings.Contains(err.Error(), "identity") {
			t.Fatalf("%s must fail closed: %v", name, err)
		}
	}
	if _, err := ParseSnapshot(frame(t, nil), "5599"); err == nil {
		t.Fatalf("client account echo mismatch must fail closed")
	}
}

func TestParseSnapshotRowValidation(t *testing.T) {
	reports := func(f map[string]any) map[string]any { return f["reports"].(map[string]any) }
	cases := map[string]func(map[string]any){
		"bad business enum": func(f map[string]any) {
			reports(f)["order_status.csv"].(map[string]any)["records"].([]any)[0].(map[string]any)["order_business"] = "9"
		},
		"filled over volume": func(f map[string]any) {
			reports(f)["order_status.csv"].(map[string]any)["records"].([]any)[0].(map[string]any)["filled_volume"] = 1001
		},
		"non-numeric price": func(f map[string]any) {
			reports(f)["order_status.csv"].(map[string]any)["records"].([]any)[0].(map[string]any)["price"] = "N/A"
		},
		"unknown state": func(f map[string]any) {
			reports(f)["order_status.csv"].(map[string]any)["records"].([]any)[0].(map[string]any)["state"] = "HALF_FILLED"
		},
		"fill without exec id": func(f map[string]any) {
			reports(f)["execution_report.csv"].(map[string]any)["records"].([]any)[0].(map[string]any)["exec_id"] = ""
		},
		"negative execution price": func(f map[string]any) {
			reports(f)["execution_report.csv"].(map[string]any)["records"].([]any)[0].(map[string]any)["price"] = "0"
		},
		"available over volume": func(f map[string]any) {
			reports(f)["position.csv"].(map[string]any)["records"].([]any)[0].(map[string]any)["available_now"] = 1001
		},
		"negative cash": func(f map[string]any) {
			reports(f)["cash.csv"].(map[string]any)["records"].([]any)[0].(map[string]any)["balance"] = "-1.00"
		},
		"duplicate cash rows": func(f map[string]any) {
			c := reports(f)["cash.csv"].(map[string]any)
			c["records"] = append(c["records"].([]any), map[string]any{"balance": "1.00", "available": "1.00", "frozen": "0.00"})
		},
		"missing report": func(f map[string]any) { delete(reports(f), "position.csv") },
	}
	for name, mutate := range cases {
		if _, err := ParseSnapshot(frame(t, mutate), "55001234"); err == nil {
			t.Fatalf("%s must be rejected, not silently zeroed", name)
		}
	}
}

func TestSnapshotReportReadiness(t *testing.T) {
	// 追加流 fresh=null：available 即就绪。
	appendStream := SnapshotReport[ExecutionRow]{Available: true}
	if !appendStream.Ready() {
		t.Fatalf("append stream with null fresh must be ready")
	}
	// 全量流 fresh=false 不就绪；fresh=true 就绪；available=false 一律不就绪。
	stale := SnapshotReport[PositionRow]{Available: true, Fresh: boolp(false)}
	fresh := SnapshotReport[PositionRow]{Available: true, Fresh: boolp(true)}
	missing := SnapshotReport[PositionRow]{Available: false}
	if stale.Ready() || missing.Ready() || !fresh.Ready() {
		t.Fatalf("readiness gate wrong")
	}
}

func boolp(v bool) *bool { return &v }
