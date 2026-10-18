package data

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"server/pkg/backtest"
	"server/pkg/dataset"
	_ "server/pkg/rules/refstrat"
)

func pitFixture(t *testing.T) (*dataset.Manifest, []time.Time) {
	t.Helper()
	start := time.Date(2026, 1, 5, 0, 0, 0, 0, shanghaiTest())
	observed := start.AddDate(0, 0, 100)
	listing := start.AddDate(-2, 0, 0)
	var records []dataset.Record
	var days []time.Time
	add := func(domain, key string, event, avail time.Time, p any) {
		r, e := dataset.NewRecord(domain, key, "fixture", event, observed, dataset.Availability{AvailableAt: &avail, Evidence: "explicit fixture timestamp"}, p)
		if e != nil {
			t.Fatal(e)
		}
		records = append(records, r)
	}
	add("security", "600001.SH", start.Add(-time.Hour), start.Add(-time.Hour), dataset.Security{Symbol: "600001.SH", Name: "测试证券", ListDate: &listing})
	for i := 0; i < 60; i++ {
		d := start.AddDate(0, 0, i)
		days = append(days, d)
		price := 10 + float64(i)/10
		b := dataset.Bar{Symbol: "600001.SH", Time: d, Freq: "1d", Open: price, High: price + 0.1, Low: price - 0.1, Close: price, Volume: 1000000, Amount: price * 1000000}
		add("bar", "600001.SH/1d/"+d.UTC().Format(time.RFC3339Nano), d, d.Add(15*time.Hour), b)
		add("adj_factor", "600001.SH/"+d.Format("2006-01-02"), d, d.Add(8*time.Hour), dataset.AdjFactor{Symbol: "600001.SH", Date: d, Factor: 1})
	}
	m, e := dataset.Freeze(dataset.Request{From: start, To: observed, Cutoff: observed, Freq: "1d"}, days, records)
	if e != nil {
		t.Fatal(e)
	}
	return m, days
}

func shanghaiTest() *time.Location {
	v, e := time.LoadLocation("Asia/Shanghai")
	if e != nil {
		panic(e)
	}
	return v
}

func TestFrozenReplayIgnoresLateCorrection(t *testing.T) {
	m, days := pitFixture(t)
	original := m.Records[0]
	for _, r := range m.Records {
		if r.Domain == "bar" {
			original = r
			break
		}
	}
	var b dataset.Bar
	if e := json.Unmarshal(original.Payload, &b); e != nil {
		t.Fatal(e)
	}
	b.Close = 999
	a := b.Time.AddDate(0, 0, 10)
	correction, e := dataset.NewRecord("bar", original.BusinessKey, "fixture", b.Time, m.Cutoff, dataset.Availability{AvailableAt: &a, Evidence: "late correction fixture"}, b)
	if e != nil {
		t.Fatal(e)
	}
	input := append(append([]dataset.Record{}, m.Records...), correction)
	m2, e := dataset.Freeze(m.Request, m.Calendar, input)
	if e != nil {
		t.Fatal(e)
	}
	s1, e := sourceFromManifest(m)
	if e != nil {
		t.Fatal(e)
	}
	s2, excluded, e := buildReplaySource(m2)
	if e != nil {
		t.Fatal(e)
	}
	if len(excluded) != 1 || excluded[0].Reason != "revision_not_available_at_bar_completion" {
		t.Fatalf("late vintage not explained: %+v", excluded)
	}
	cfg := backtest.DefaultConfig()
	cfg.Strategy = "breakout_ref"
	cfg.Freq = "1d"
	cfg.Start = days[30].Format("2006-01-02")
	cfg.End = days[59].Format("2006-01-02")
	cfg.HistoryBars = 30
	r1, e := backtest.Run(context.Background(), cfg, s1, nil)
	if e != nil {
		t.Fatal(e)
	}
	r2, e := backtest.Run(context.Background(), cfg, s2, nil)
	if e != nil {
		t.Fatal(e)
	}
	if r1.DataHash != r2.DataHash || r1.FillsHash != r2.FillsHash {
		t.Fatal("future correction leaked into replay")
	}
	if len(s1.Daily["600001.SH"]) != 60 {
		t.Fatal("valid evidence was lost")
	}
}

func TestHistoricalUniverseSuspensionDelistingAndFactor(t *testing.T) {
	m, days := pitFixture(t)
	observed := m.Cutoff
	d := days[40]
	listing := days[0].AddDate(-2, 0, 0)
	addSecurity := func(at time.Time, s dataset.Security) {
		r, e := dataset.NewRecord("security", s.Symbol, "fixture", at, observed, dataset.Availability{AvailableAt: &at, Evidence: "security history fixture"}, s)
		if e != nil {
			t.Fatal(e)
		}
		m.Records = append(m.Records, r)
	}
	addSecurity(d.Add(8*time.Hour), dataset.Security{Symbol: "600001.SH", Name: "ST 测试证券", ListDate: &listing, Suspended: true})
	addSecurity(days[41].Add(8*time.Hour), dataset.Security{Symbol: "600001.SH", Name: "ST 测试证券", ListDate: &listing, DelistDate: &days[45]})
	input := []dataset.Record{}
	for _, r := range m.Records {
		if r.Domain == "adj_factor" && r.BusinessKey == "600001.SH/"+days[42].Format("2006-01-02") {
			continue
		}
		input = append(input, r)
	}
	m2, e := dataset.Freeze(m.Request, m.Calendar, input)
	if e != nil {
		t.Fatal(e)
	}
	src, excluded, e := buildReplaySource(m2)
	if e != nil {
		t.Fatal(e)
	}
	reasons := map[string]int{}
	for _, x := range excluded {
		reasons[x.Reason]++
	}
	if reasons["suspended"] != 1 || reasons["preopen_adjustment_factor_unknown"] != 1 || reasons["delisted"] != 15 {
		t.Fatalf("wrong PIT exclusions: %+v", reasons)
	}
	for _, b := range src.Daily["600001.SH"] {
		if !b.Day.Before(days[45]) {
			t.Fatal("delisted future row entered replay")
		}
	}
}
