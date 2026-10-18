package allocation

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"server/pkg/accountstate"
	"server/pkg/ashare"
	"server/pkg/market"
	"server/pkg/risk"
	"server/pkg/tradecal"
)

func allocationFixture() Input {
	now := time.Date(2026, 10, 12, 10, 30, 0, 0, tradecal.Shanghai())
	return Input{Portfolio: Portfolio{Weights: map[string]float64{"600519.SH": .2, "000001.SZ": .2}, CashWeight: .6}, Account: accountstate.Snapshot{Version: 1, Kind: "full", AccountID: "default", AccountType: risk.SIM, SnapshotID: "frozen", Generation: 9007199254740993, AsOf: now, TradingDay: "2026-10-12", Cash: 100000, Available: 100000, Equity: 100000, DayStartEquity: 100000, DayStartKnown: true, Holdings: []risk.Holding{}, OpenOrders: []risk.OpenOrder{}, ObservedOrders: []string{}, Reconciliation: accountstate.Reconciliation{Status: "matched", AsOf: now}}, AccountMatchesLedger: true, Quotes: map[string]market.Snapshot{"600519.SH": {Symbol: "600519.SH", Time: now, AsOf: now, Last: 10.13}, "000001.SZ": {Symbol: "000001.SZ", Time: now, AsOf: now, Last: 20.07}}, Assets: map[string]Asset{"600519.SH": {Known: true, Sector: "food"}, "000001.SZ": {Known: true, Sector: "bank"}}, Policy: Policy{Known: true, MaxSingle: .3, MaxTotal: .8, MaxSector: .4}, Fee: ashare.DefaultFee(), Now: now}
}
func TestPreviewLotsFeesAndExactAccountIdentity(t *testing.T) {
	in := allocationFixture()
	p := Calculate(in)
	if !p.AllocationReady || p.CanExecute || p.RiskChecked || p.State != "complete" {
		t.Fatalf("wrong permission/constraints: %+v", p)
	}
	if p.AccountGeneration != "9007199254740993" {
		t.Fatal("identity lost precision")
	}
	if p.Summary.BuyCost == nil || *p.Summary.BuyCost <= 37310 || p.Summary.EstimatedFees == nil || *p.Summary.EstimatedFees <= 0 || *p.Summary.BuyFundingGap != 0 {
		t.Fatalf("fees/cash unknown: %+v", p.Summary)
	}
	for _, r := range p.Rows {
		if r.TargetQuantity == nil || r.BuyQuantity%100 != 0 || r.ReferencePrice == nil || r.SourceTime == nil {
			t.Fatalf("incomplete row %+v", r)
		}
	}
	body, err := json.Marshal(p)
	if err != nil || !strings.Contains(string(body), `"account_generation":"9007199254740993"`) {
		t.Fatalf("unsafe JSON identity: %s %v", body, err)
	}
}
func TestPreviewUnknownQuotesCannotReuseSnapshotValuation(t *testing.T) {
	in := allocationFixture()
	in.Account.Cash = 85000
	in.Account.Available = 85000
	in.Account.Holdings = []risk.Holding{{Symbol: "600519.SH", Quantity: 1500, Available: 1500, AvgCost: 10, MarketValue: 15000}}
	q := in.Quotes["600519.SH"]
	q.Time = in.Now.Add(-11 * time.Second)
	q.AsOf = in.Now
	in.Quotes[q.Symbol] = q
	p := Calculate(in)
	if p.AllocationReady || p.Summary.FreshEquity != nil || p.Summary.EstimatedFees != nil {
		t.Fatalf("missing mark became known: %+v", p)
	}
	for _, r := range p.Rows {
		if r.TargetQuantity != nil || r.BuyQuantity != 0 || r.SellQuantity != 0 {
			t.Fatalf("stale equity used: %+v", r)
		}
	}
}
func TestPreviewExplainsT1FrozenCashConcentrationAndLockedTargets(t *testing.T) {
	for _, kind := range []string{"t1", "cash", "concentration", "locked", "open_order", "ledger_changed"} {
		t.Run(kind, func(t *testing.T) {
			in := allocationFixture()
			switch kind {
			case "t1":
				in.Account.Cash = 85000
				in.Account.Available = 85000
				in.Account.Holdings = []risk.Holding{{Symbol: "600519.SH", Quantity: 1500, Available: 0, AvgCost: 10, MarketValue: 15000}}
				in.Portfolio.Weights["600519.SH"] = 0
				in.Portfolio.CashWeight = .8
			case "cash":
				in.Account.Available = 99000
				in.Account.FrozenCash = 1000
				in.Quotes["600519.SH"] = market.Snapshot{Symbol: "600519.SH", Time: in.Now, Last: 10}
				in.Quotes["000001.SZ"] = market.Snapshot{Symbol: "000001.SZ", Time: in.Now, Last: 20}
			case "concentration":
				in.Policy.MaxSingle = .05
			case "locked":
				in.Portfolio.Options.LockedSymbols = []string{"600519.SH"}
			case "open_order":
				in.Account.OpenOrders = []risk.OpenOrder{{ClientID: "pending", Symbol: "600519.SH", Side: risk.Buy, Price: 10, Volume: 100}}
				in.Account.ObservedOrders = []string{"pending"}
				in.Account.FrozenCash = 1005
				in.Account.Available -= 1005
			case "ledger_changed":
				in.AccountMatchesLedger = false
			}
			p := Calculate(in)
			if p.AllocationReady || p.CanExecute {
				t.Fatalf("constraint ignored: %+v", p)
			}
			if kind == "locked" {
				for _, r := range p.Rows {
					if r.Symbol == "600519.SH" && (r.BuyQuantity != 0 || r.SellQuantity != 0) {
						t.Fatal("locked shares changed")
					}
				}
			}
		})
	}
}
func researchFixtureRecord() ResearchRecord {
	h := strings.Repeat("a", 64)
	mid := "sha256:" + h
	input, _ := json.Marshal(map[string]any{"kind": "return_matrix", "currency": "CNY", "frequency": "1d", "symbols": []string{"000001.SZ", "600519.SH"}, "known_at_cutoff": true, "manifest_id": mid, "manifest_hash": h, "fixture_type": "synthetic_not_market_evidence"})
	body, _ := json.Marshal(map[string]any{"schema_version": 1, "run_id": "portfolio-test", "kind": "portfolio", "status": "succeeded", "manifest_id": mid, "manifest_hash": h, "result": map[string]any{"solver_status": "OPTIMAL", "promotion": "shadow_target_only", "weights": map[string]float64{"000001.SZ": .2, "600519.SH": .2}, "cash_weight": .6, "max_constraint_violation": 0}})
	return ResearchRecord{RunID: "portfolio-test", Kind: "portfolio", Status: "succeeded", ManifestID: mid, ManifestHash: h, InputHash: strings.Repeat("b", 64), ResultHash: HashBytes(body), InputJSON: string(input), OptionsJSON: `{"max_weight":0.3}`, ResultJSON: string(body)}
}
func TestOnlyAuthoritativeCompletedPortfolioResultAccepted(t *testing.T) {
	r := researchFixtureRecord()
	p, err := DecodePortfolio(r)
	if err != nil || p.Weights["600519.SH"] != .2 || p.FixtureType != "synthetic_not_market_evidence" {
		t.Fatalf("valid host result denied: %+v %v", p, err)
	}
	for _, mutate := range []func(*ResearchRecord){func(r *ResearchRecord) { r.Kind = "performance" }, func(r *ResearchRecord) { r.Status = "running" }, func(r *ResearchRecord) { r.CancelRequested = true }, func(r *ResearchRecord) { r.ResultJSON += " " }, func(r *ResearchRecord) { r.ManifestHash = "wrong" }, func(r *ResearchRecord) {
		r.ResultJSON = strings.Replace(r.ResultJSON, "OPTIMAL", "INFEASIBLE", 1)
		r.ResultHash = HashBytes([]byte(r.ResultJSON))
	}, func(r *ResearchRecord) { r.OptionsJSON = `{"max_weight":2}` }} {
		copy := r
		mutate(&copy)
		if _, err := DecodePortfolio(copy); err == nil {
			t.Fatalf("invalid authority accepted: %+v", copy)
		}
	}
}
