package data

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/go-kratos/kratos/v2/log"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	v1 "server/api/trade/v1"
	"server/app/trade/internal/biz"
	tradehttp "server/app/trade/internal/server"
	"server/app/trade/internal/service"
	"server/ent"
	"server/ent/accountsnapshot"
	"server/ent/position"
	"server/ent/simbarcursor"
	"server/ent/tradecash"
	"server/ent/tradefill"
	"server/ent/tradeorder"
	"server/pkg/accountnav"
	"server/pkg/accountstate"
	"server/pkg/dataset"
	"server/pkg/risk"
	"server/pkg/tradecal"
	"strings"
	"testing"
	"time"
)

func navClient(t *testing.T) *ent.Client {
	t.Helper()
	c := accountTestClientDatabase(t, "novatrader_nav_contract_test")
	ctx := context.Background()
	// This dedicated database contains only these owned synthetic NAV fixtures;
	// other tests use account/allocation databases. No production book is read.
	for _, remove := range []func() error{
		func() error {
			_, err := c.AccountSnapshot.Delete().Where(accountsnapshot.Book("paper")).Exec(ctx)
			return err
		},
		func() error { _, err := c.TradeFill.Delete().Where(tradefill.Book("paper")).Exec(ctx); return err },
		func() error { _, err := c.TradeOrder.Delete().Where(tradeorder.Book("paper")).Exec(ctx); return err },
		func() error { _, err := c.Position.Delete().Where(position.Book("paper")).Exec(ctx); return err },
		func() error { _, err := c.TradeCash.Delete().Where(tradecash.Book("paper")).Exec(ctx); return err },
		func() error {
			_, err := c.SimBarCursor.Delete().Where(simbarcursor.Book("paper")).Exec(ctx)
			return err
		},
	} {
		if err := remove(); err != nil {
			t.Fatal(err)
		}
	}
	return c
}
func navAt(day, hour, minute, second int) time.Time {
	return time.Date(2026, 10, day, hour, minute, second, 0, tradecal.Shanghai())
}

func TestPostgresNAVExportHTTPToActualQuantStatsContract(t *testing.T) {
	ctx := context.Background()
	c := navClient(t)
	r := NewRepo(c, log.DefaultLogger)
	clock := navAt(13, 15, 9, 50)
	e := biz.NewEngine(r, planRisk{}, biz.Settings{InitialCash: 100000}, log.DefaultLogger)
	// Engine's private test clock is not exported; use real Snapshot assembly
	// through a small deterministic account fixture and the actual repository.
	// It represents synthetic simulator observations, never historical live data.
	save := func(at time.Time, cash float64, holdings []risk.Holding, ids []string) accountstate.Snapshot {
		s := accountstate.Snapshot{Version: 1, Kind: "full", AccountID: "default", AccountType: risk.SIM, SnapshotID: "synthetic-nav-" + at.Format("20060102150405"), AsOf: at, TradingDay: at.In(tradecal.Shanghai()).Format("2006-01-02"), Cash: cash, Available: cash, Equity: cash, DayStartEquity: 100000, DayStartKnown: true, Holdings: holdings, OpenOrders: []risk.OpenOrder{}, ObservedOrders: ids, Reconciliation: accountstate.Reconciliation{Status: "matched", AsOf: at}}
		for _, h := range holdings {
			s.Equity += h.MarketValue
		}
		stored, err := r.CommitSnapshot(ctx, "paper", biz.AccountView{Book: "paper", Cash: cash, Equity: s.Equity, PositionCount: len(holdings)}, biz.Cash{Book: "paper", Cash: cash, DayStartDate: s.TradingDay, DayStartEquity: 100000}, s)
		if err != nil {
			t.Fatal(err)
		}
		return stored
	}
	initial := save(clock, 100000, []risk.Holding{}, []string{})
	if initial.Valuation == nil || !initial.Valuation.Complete || initial.Valuation.Source != "ledger_cash_only" {
		t.Fatal("cash-only source proof missing")
	}
	// The amounts and fee are actual ent ledger rows; export must reconcile them.
	const orderID = "synthetic-nav-buy"
	if _, err := c.TradeOrder.Create().SetClientOrderID(orderID).SetBook("paper").SetSymbol("600998.SH").SetSide("buy").SetStatus("filled").SetPrice(10).SetVolume(100).SetFilled(100).SetCreatedAt(navAt(14, 10, 0, 0)).Save(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.TradeFill.Create().SetClientOrderID(orderID).SetBook("paper").SetSymbol("600998.SH").SetSide("buy").SetQty(100).SetPrice(10).SetAmount(1000).SetFee(5).SetCreatedAt(navAt(14, 10, 0, 0)).Save(ctx); err != nil {
		t.Fatal(err)
	}
	for _, day := range []int{14, 15} {
		price := 10.1 + float64(day-14)*.1
		if err := r.Commit(ctx, biz.Batch{Positions: []biz.Position{{Book: "paper", Symbol: "600998.SH", Quantity: 100, Available: 100, AvgCost: 10, LastPrice: price}}}); err != nil {
			t.Fatal(err)
		}
		if err := r.WithLedger(ctx, "global", func(c context.Context) error {
			return r.AdvanceSimBar(c, "paper", "600998.SH", navAt(day, 14, 59, 0), price)
		}); err != nil {
			t.Fatal(err)
		}
		state := save(navAt(day, 15, 9, 50), 98995, []risk.Holding{{Symbol: "600998.SH", Quantity: 100, Available: 100, AvgCost: 10, MarketValue: 100 * price}}, []string{orderID})
		if state.Valuation == nil || !state.Valuation.Complete || len(state.Valuation.Marks) != 1 || state.Valuation.Marks[0].Price != price {
			t.Fatalf("source-bar mark not bound: %+v", state.Valuation)
		}
	}
	calendar := []time.Time{navAt(13, 0, 0, 0), navAt(14, 0, 0, 0), navAt(15, 0, 0, 0)}
	m, err := dataset.Freeze(dataset.Request{From: calendar[0], To: calendar[2], Cutoff: navAt(15, 16, 0, 0), Freq: "1d"}, calendar, nil)
	if err != nil {
		t.Fatal(err)
	}
	manifestJSON, _ := json.Marshal(m)
	if err = c.DatasetManifest.Create().SetManifestID(m.ManifestID).SetHash(m.Hash).SetCutoff(m.Cutoff).SetContent(manifestJSON).OnConflict().DoNothing().Exec(ctx); err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	svc := service.NewTradeService(e, nil)
	navSvc := service.NewNavService(svc)
	srv := httptest.NewServer(tradehttp.NewHTTPServer(nil, svc, service.NewAdmissionService(svc), navSvc, log.DefaultLogger))
	defer srv.Close()
	req := &v1.NavExportRequest{Account: "SIM", StartDate: "2026-10-14", EndDate: "2026-10-15", Frequency: "1d", AsOf: navAt(15, 16, 0, 0).Format(time.RFC3339), ManifestId: m.ManifestID, BenchmarkJson: `{"benchmark_id":"synthetic-explicit-index","benchmark_kind":"price_index","points":[{"date":"2026-10-13","value":100},{"date":"2026-10-14","value":101},{"date":"2026-10-15","value":102}]}`}
	before, _ := c.AccountSnapshot.Query().Count(ctx)
	body, _ := protojson.Marshal(req)
	resp, err := http.Post(srv.URL+"/v1/trade/nav/export", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HTTP %d %s", resp.StatusCode, raw)
	}
	var reply v1.NavExportReply
	if err = protojson.Unmarshal(raw, &reply); err != nil {
		t.Fatal(err)
	}
	var report accountnav.Report
	if err = json.Unmarshal([]byte(reply.ReportJson), &report); err != nil {
		t.Fatal(err)
	}
	if !reply.InputReady || reply.InputJson == "" || len(report.Points) != 3 || len(report.Reasons) != 0 || *report.Points[1].Equity != 100005 || *report.Points[2].Equity != 100015 {
		t.Fatalf("formal NAV unavailable: %+v reply=%s", report, raw)
	}
	after, _ := c.AccountSnapshot.Query().Count(ctx)
	if after != before {
		t.Fatal("read-only NAV export wrote an account snapshot")
	}
	// Missing explicit benchmark keeps all observed equity visible but cannot
	// produce a performance input or download a ticker behind the user's back.
	withoutBenchmark := proto.Clone(req).(*v1.NavExportRequest)
	withoutBenchmark.BenchmarkJson = ""
	noBenchmarkBody, _ := protojson.Marshal(withoutBenchmark)
	noBenchmarkResp, err := http.Post(srv.URL+"/v1/trade/nav/export", "application/json", bytes.NewReader(noBenchmarkBody))
	if err != nil {
		t.Fatal(err)
	}
	noBenchmarkRaw, _ := io.ReadAll(noBenchmarkResp.Body)
	_ = noBenchmarkResp.Body.Close()
	var noBenchmarkReply v1.NavExportReply
	if err = protojson.Unmarshal(noBenchmarkRaw, &noBenchmarkReply); err != nil || noBenchmarkReply.InputReady || noBenchmarkReply.InputJson != "" {
		t.Fatalf("missing benchmark admitted: %s %v", noBenchmarkRaw, err)
	}
	proofDir, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	fixture := map[string]any{}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	fixture["_fixture_scope"] = "synthetic_test"
	fixture["_fixture_note"] = "isolated novatrader_nav_contract_test; not real market/admission evidence"
	frozen, _ := json.MarshalIndent(fixture, "", "  ")
	if err = os.WriteFile(filepath.Join(proofDir, "nav-export-contract.json"), frozen, 0600); err != nil {
		t.Fatal(err)
	}
	worker, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "..", "research-worker"))
	if err != nil {
		t.Fatal(err)
	}
	python := filepath.Join(worker, ".venv", "Scripts", "python.exe")
	if _, err = os.Stat(python); err != nil {
		t.Log("optional isolated QuantStats Python unavailable; Go/HTTP contract passed")
		return
	}
	jobDir := t.TempDir()
	output := t.TempDir()
	if err = os.WriteFile(filepath.Join(jobDir, "manifest.json"), manifestJSON, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(jobDir, "input.json"), []byte(reply.InputJson), 0600); err != nil {
		t.Fatal(err)
	}
	job := map[string]any{"schema_version": 1, "run_id": "synthetic-nav-contract", "kind": "performance", "as_of": report.ResearchAsOf.Format(time.RFC3339Nano), "seed": 1, "manifest": map[string]any{"id": m.ManifestID, "hash": m.Hash, "path": "manifest.json", "sha256": navSHA(manifestJSON)}, "input": map[string]any{"path": "input.json", "sha256": reply.InputSha256}, "options": map[string]any{}}
	jobJSON, _ := json.Marshal(job)
	if err = os.WriteFile(filepath.Join(jobDir, "job.json"), jobJSON, 0600); err != nil {
		t.Fatal(err)
	}
	bounded, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(bounded, python, "-I", filepath.Join(worker, "worker.py"), "--job", filepath.Join(jobDir, "job.json"), "--output", output)
	if runOut, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("actual worker rejected NAV export: %v %s", err, runOut)
	}
	resultJSON, err := os.ReadFile(filepath.Join(output, "synthetic-nav-contract", "result.json"))
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Status string `json:"status"`
	}
	if err = json.Unmarshal(resultJSON, &result); err != nil || result.Status != "succeeded" {
		t.Fatalf("actual QuantStats failed: %s %v", resultJSON, err)
	}
	t.Log("actual isolated QuantStats worker accepted unchanged HTTP input_json")
}

func TestPostgresNAVValuationRejectsLegacyAndMismatchedCursor(t *testing.T) {
	c := navClient(t)
	ctx := context.Background()
	r := NewRepo(c, log.DefaultLogger)
	const symbol = "600998.SH"
	if err := r.Commit(ctx, biz.Batch{Positions: []biz.Position{{Book: "paper", Symbol: symbol, Quantity: 100, Available: 100, AvgCost: 10, LastPrice: 10}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SimBarCursor.Create().SetBook("paper").SetSymbol(symbol).SetLastBarStart(navAt(16, 14, 58, 0)).Save(ctx); err != nil {
		t.Fatal(err)
	}
	s := accountstate.Snapshot{Version: 1, Kind: "full", AccountID: "default", AccountType: risk.SIM, SnapshotID: "synthetic-proof-legacy", AsOf: navAt(16, 15, 9, 50), TradingDay: "2026-10-16", Cash: 99000, Available: 99000, Equity: 100000, DayStartKnown: true, DayStartEquity: 100000, Holdings: []risk.Holding{{Symbol: symbol, Quantity: 100, Available: 100, AvgCost: 10, MarketValue: 1000}}, OpenOrders: []risk.OpenOrder{}, ObservedOrders: []string{}, Reconciliation: accountstate.Reconciliation{Status: "matched", AsOf: navAt(16, 15, 9, 50)}}
	s.AsOf = s.AsOf.Add(123 * time.Nanosecond)
	s.Reconciliation.AsOf = s.Reconciliation.AsOf.Add(123 * time.Nanosecond)
	save := func(s accountstate.Snapshot) accountstate.Snapshot {
		state, err := r.CommitSnapshot(ctx, "paper", biz.AccountView{Book: "paper", Cash: s.Cash, Equity: s.Equity, PositionCount: 1}, biz.Cash{Book: "paper", Cash: s.Cash, DayStartDate: s.TradingDay, DayStartEquity: 100000}, s)
		if err != nil {
			t.Fatal(err)
		}
		return state
	}
	legacy := save(s)
	persisted, err := c.AccountSnapshot.Get(ctx, int(legacy.Generation))
	if err != nil || legacy.AsOf.Nanosecond()%1000 != 0 || !legacy.AsOf.Equal(persisted.SnapshotTime) {
		t.Fatal("state and indexed snapshot timestamp differ", legacy.AsOf, persisted, err)
	}
	if legacy.Valuation == nil || legacy.Valuation.Complete {
		t.Fatal("legacy cursor fabricated a validated price")
	}
	// Isolated zero-time quotes can update the test ledger, but cannot certify
	// any server-source minute or advance the valuation proof.
	if err := r.Commit(ctx, biz.Batch{Cash: &biz.Cash{Book: "paper", Cash: 99000, DayStartDate: time.Now().In(tradecal.Shanghai()).Format("2006-01-02"), DayStartEquity: 100000}}); err != nil {
		t.Fatal(err)
	}
	e := biz.NewEngine(r, planRisk{}, biz.Settings{}, log.DefaultLogger)
	if _, err := e.ApplyBar(ctx, "SIM", symbol, biz.Quote{Close: 10}); err != nil {
		t.Fatal(err)
	}
	old, err := c.SimBarCursor.Query().Where(simbarcursor.Book("paper"), simbarcursor.Symbol(symbol)).Only(ctx)
	if err != nil || old.ValuationVersion != 0 || old.LastClose != 0 {
		t.Fatal("zero-time quote gained source proof", old, err)
	}
	if err = r.WithLedger(ctx, "global", func(c context.Context) error { return r.AdvanceSimBar(c, "paper", symbol, navAt(16, 14, 59, 0), 10) }); err != nil {
		t.Fatal(err)
	}
	known := save(s)
	if known.Valuation == nil || !known.Valuation.Complete || !known.Valuation.Marks[0].BarEnd.Equal(navAt(16, 15, 0, 0)) {
		t.Fatal("validated source-minute close proof missing", known.Valuation)
	}
	if _, err = c.Position.Update().Where(position.Book("paper"), position.Symbol(symbol)).SetCurrentPrice(10.1).Save(ctx); err != nil {
		t.Fatal(err)
	}
	s.Holdings[0].MarketValue = 1010
	s.Equity = 100010
	mismatch := save(s)
	if mismatch.Valuation == nil || mismatch.Valuation.Complete {
		t.Fatal("different position mark reused old source price")
	}
}

func TestPostgresNAVReaderSelectsLatestActualCloseAndRejectsOversizedEvidence(t *testing.T) {
	c := navClient(t)
	ctx := context.Background()
	r := NewRepo(c, log.DefaultLogger)
	ids := []int{}
	t.Cleanup(func() {
		if _, err := c.AccountSnapshot.Delete().Where(accountsnapshot.IDIn(ids...)).Exec(ctx); err != nil {
			t.Error(err)
		}
	})
	// These rows exercise storage selection only. Padding is deliberately not
	// an AccountSnapshotV1 and can never become a performance/admission sample.
	save := func(book string, at time.Time, state json.RawMessage) int {
		t.Helper()
		builder := c.AccountSnapshot.Create().SetBook(book).SetSnapshotTime(at).
			SetTotalEquity(100000).SetAvailableFunds(100000).SetUnrealizedPnl(0).
			SetRealizedPnl(0).SetRiskRatio(0).SetPositionCount(0)
		if state != nil {
			builder.SetState(state)
		}
		row, err := builder.Save(ctx)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, row.ID)
		return row.ID
	}
	state := json.RawMessage(`{"synthetic_storage_fixture":true}`)
	save("paper", navAt(13, 14, 59, 59), state)
	save("paper", navAt(13, 15, 0, 0), state)
	save("paper", navAt(13, 15, 9, 40), state)
	lastTieID := save("paper", navAt(13, 15, 9, 40), state)
	save("paper", navAt(13, 15, 9, 50), state)     // after the explicit as_of
	save("paper", navAt(13, 15, 10, 0), state)     // outside the closing window
	save("nav-other", navAt(13, 15, 9, 44), state) // a separate synthetic test book
	q := accountnav.Query{Request: accountnav.Request{AsOf: navAt(13, 15, 9, 45)}, ExpectedDates: []string{"2026-10-13"}, From: navAt(13, 0, 0, 0), To: navAt(13, 16, 0, 0)}
	e, err := r.ReadNAVEvidence(ctx, q)
	if err != nil || len(e.Snapshots) != 1 || e.Snapshots[0].ID != lastTieID {
		t.Fatalf("did not select last observed SIM closing row using time/ID: %+v %v", e.Snapshots, err)
	}
	lastLegacyID := save("paper", navAt(13, 15, 9, 59), nil)
	q.AsOf = navAt(13, 16, 0, 0)
	e, err = r.ReadNAVEvidence(ctx, q)
	if err != nil || len(e.Snapshots) != 1 || e.Snapshots[0].ID != lastLegacyID || len(e.Snapshots[0].State) != 0 {
		t.Fatalf("latest legacy aggregate was hidden by an older state: %+v %v", e.Snapshots, err)
	}
	padding := json.RawMessage(`{"synthetic_storage_padding":"` + strings.Repeat("x", 8<<20) + `"}`)
	save("paper", navAt(14, 15, 9, 50), padding)
	save("paper", navAt(15, 15, 9, 50), padding)
	q.ExpectedDates = []string{"2026-10-14", "2026-10-15"}
	q.AsOf, q.To = navAt(15, 16, 0, 0), navAt(15, 16, 0, 0)
	if _, err = r.ReadNAVEvidence(ctx, q); err == nil || !strings.Contains(err.Error(), "16 MiB") {
		t.Fatalf("oversized selected evidence must fail instead of truncate: %v", err)
	}
}

func navSHA(p []byte) string { return fmt.Sprintf("%x", sha256.Sum256(p)) }
