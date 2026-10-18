//go:build ignore

// Run from backend: go run ./pkg/dataset/testdata/export_fixture.go
package main

import (
	"encoding/json"
	"os"
	"server/pkg/dataset"
	"time"
)

func main() {
	at := time.Date(2026, 4, 1, 7, 0, 0, 0, time.UTC)
	obs := at.Add(time.Hour)
	r, err := dataset.NewRecord("macro", "CPI/2026-03", "fixture", at.AddDate(0, -1, 0), obs, dataset.Availability{AvailableAt: &at, Evidence: "explicit synthetic publication fixture"}, json.RawMessage(`{"中文":"宏观 <fixture> & 数据","big":9007199254740993,"small":1e-7,"scientific":1e21,"value":1.25}`))
	if err != nil {
		panic(err)
	}
	m, err := dataset.Freeze(dataset.Request{From: at.AddDate(0, -2, 0), To: obs, Cutoff: obs, Freq: "1d"}, []time.Time{at}, []dataset.Record{r})
	if err != nil {
		panic(err)
	}
	p, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		panic(err)
	}
	if err = os.WriteFile("pkg/dataset/testdata/manifest_v1.json", append(p, '\n'), 0600); err != nil {
		panic(err)
	}
}
