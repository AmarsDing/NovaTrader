package accountstate

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestProducedContractRejectsIncompleteAndInvalidState(t *testing.T) {
	raw, err := os.ReadFile("testdata/trade-produced-full-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	state, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Ready(state.AsOf, time.Minute) || state.Ready(state.AsOf.Add(61*time.Second), time.Minute) {
		t.Fatal("freshness mismatch")
	}
	mutations := map[string]func(map[string]any){
		"missing cash":               func(m map[string]any) { delete(m, "cash") },
		"null holdings":              func(m map[string]any) { m["holdings"] = nil },
		"unknown version":            func(m map[string]any) { m["version"] = 2 },
		"partial kind":               func(m map[string]any) { m["kind"] = "delta" },
		"unknown account":            func(m map[string]any) { m["account_id"] = "another-user" },
		"negative cash":              func(m map[string]any) { m["available"] = -1 },
		"cash conservation":          func(m map[string]any) { m["available"] = 950000 },
		"equity conservation":        func(m map[string]any) { m["equity"] = 999999 },
		"unknown baseline":           func(m map[string]any) { m["day_start_known"] = false },
		"missing sellable":           func(m map[string]any) { delete(m["holdings"].([]any)[0].(map[string]any), "available") },
		"too many frozen shares":     func(m map[string]any) { m["holdings"].([]any)[0].(map[string]any)["frozen_sell"] = 3001 },
		"order missing quantity":     func(m map[string]any) { delete(m["open_orders"].([]any)[0].(map[string]any), "volume") },
		"order absent from observed": func(m map[string]any) { m["observed_order_ids"] = []string{"b1"} },
		"not a reconciliation":       func(m map[string]any) { m["reconciliation"] = map[string]any{"status": "matched"} },
	}
	for name, change := range mutations {
		t.Run(name, func(t *testing.T) {
			var m map[string]any
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Fatal(err)
			}
			change(m)
			b, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Decode(b); err == nil {
				t.Fatal("invalid snapshot accepted")
			}
		})
	}
}
