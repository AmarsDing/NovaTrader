package data

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	"github.com/google/uuid"
	v1 "server/api/trade/v1"
	"server/app/trade/internal/biz"
	"server/app/trade/internal/service"
	"server/ent/researchjob"
	"server/pkg/allocation"
	"server/pkg/market"
	"server/pkg/tradecal"
)

type allocationRiskSpy struct{ calls atomic.Int32 }

func (s *allocationRiskSpy) Check(context.Context, biz.CheckIn) (biz.CheckOut, error) {
	s.calls.Add(1)
	return biz.CheckOut{Approved: true}, nil
}

func TestPostgresPortfolioPreviewHTTPUsesAuthorityAndWritesNoBusinessRows(t *testing.T) {
	c := accountTestClientDatabase(t, "novatrader_allocation_contract_test")
	ctx := context.Background()
	repo := NewRepo(c, log.DefaultLogger)
	spy := &allocationRiskSpy{}
	e := biz.NewEngine(repo, spy, biz.Settings{InitialCash: 100000}, log.DefaultLogger)
	// Explicit synthetic ledger fixture in the isolated test database. It cannot
	// be counted as a real market or admission observation.
	if err := repo.Commit(ctx, biz.Batch{Cash: &biz.Cash{Book: biz.BookPaper, Cash: 100000, DayStartDate: time.Now().In(tradecal.Shanghai()).Format("2006-01-02"), DayStartEquity: 100000}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Snapshot(ctx, "SIM"); err != nil {
		t.Fatal(err)
	}
	runID := "allocation_" + uuid.NewString()
	hash := strings.Repeat("a", 64)
	manifest := "sha256:" + hash
	input, _ := json.Marshal(map[string]any{"kind": "return_matrix", "currency": "CNY", "frequency": "1d", "symbols": []string{"000001.SZ"}, "known_at_cutoff": true, "manifest_id": manifest, "manifest_hash": hash, "fixture_type": "synthetic_not_market_evidence"})
	result, _ := json.Marshal(map[string]any{"schema_version": 1, "run_id": runID, "kind": "portfolio", "status": "succeeded", "manifest_id": manifest, "manifest_hash": hash, "result": map[string]any{"solver_status": "OPTIMAL", "promotion": "shadow_target_only", "weights": map[string]float64{"000001.SZ": .05}, "cash_weight": .95, "max_constraint_violation": 0}})
	if _, err := c.ResearchJob.Create().SetRunID(runID).SetKind("portfolio").SetManifestID(manifest).SetManifestHash(hash).SetAsOf(time.Now()).SetInputJSON(string(input)).SetOptionsJSON(`{"max_weight":0.05}`).SetInputHash(strings.Repeat("b", 64)).SetStatus("succeeded").SetResultJSON(string(result)).SetArtifactHash(allocation.HashBytes(result)).Save(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = c.ResearchJob.Delete().Where(researchjob.RunID(runID)).Exec(ctx) })
	repo.quote = func(_ context.Context, s string) (market.Snapshot, error) {
		return market.Snapshot{Symbol: s, Time: time.Now(), AsOf: time.Now(), Last: 10.13}, nil
	}
	count := func() []int {
		t.Helper()
		var out []int
		for _, fn := range []func(context.Context) (int, error){c.TradeOrder.Query().Count, c.TradeFill.Query().Count, c.ExecutionIntent.Query().Count, c.DecisionPlan.Query().Count, c.AccountSnapshot.Query().Count, c.TradeCash.Query().Count, c.Position.Query().Count, c.Outbox.Query().Count, c.StrategyVersion.Query().Count} {
			n, err := fn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, n)
		}
		return out
	}
	before := count()
	srv := khttp.NewServer()
	v1.RegisterPortfolioAllocationHTTPServer(srv, service.NewTradeService(e, nil, nil))
	ts := httptest.NewServer(srv)
	defer ts.Close()
	post := func(account string) (int, allocation.Preview) {
		t.Helper()
		request, _ := json.Marshal(map[string]any{"research_run_id": runID, "account": account})
		resp, err := http.Post(ts.URL+"/v1/trade/portfolio/preview", "application/json", bytes.NewReader(request))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		var wrapper struct {
			PreviewJSON  string `json:"previewJson"`
			PreviewSnake string `json:"preview_json"`
		}
		_ = json.Unmarshal(raw, &wrapper)
		if wrapper.PreviewJSON == "" {
			wrapper.PreviewJSON = wrapper.PreviewSnake
		}
		var p allocation.Preview
		if resp.StatusCode == 200 {
			if err := json.Unmarshal([]byte(wrapper.PreviewJSON), &p); err != nil {
				t.Fatalf("invalid exact preview %s %v", raw, err)
			}
		}
		return resp.StatusCode, p
	}
	code, p := post("SIM")
	if code != 200 || p.Research.RunID != runID || p.AccountStateHash == "" || p.AccountGeneration == "" || p.CanExecute || p.RiskChecked || len(p.Rows) == 0 || p.Rows[0].ReferencePrice == nil {
		t.Fatalf("wrong preview code=%d %+v", code, p)
	}
	if code, _ := post("LIVE"); code != 400 {
		t.Fatalf("LIVE allowed: %d", code)
	}
	if path := os.Getenv("NT_TEST_ALLOCATION_PREVIEW_OUT"); path != "" {
		body, err := json.MarshalIndent(p, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	// Removing the server quote cannot be repaired by the stored NAV/equity.
	repo.quote = func(context.Context, string) (market.Snapshot, error) { return market.Snapshot{}, nil }
	if code, p := post("SIM"); code != 200 || p.AllocationReady || p.Rows[0].ReferencePrice != nil || p.Rows[0].BuyQuantity != 0 {
		t.Fatalf("missing quote became price/permission: %d %+v", code, p)
	}
	if spy.calls.Load() != 0 {
		t.Fatal("read-only preview called side-effecting risk")
	}
	after := count()
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("business writes during preview index=%d before=%v after=%v", i, before, after)
		}
	}
}
