// Package allocation calculates a read-only share preview. It has no order,
// strategy activation, database, broker or side-effecting risk capability.
package allocation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"

	"server/pkg/ashare"
	"server/pkg/symbol"
)

type ResearchRecord struct {
	RunID, Kind, Status, ManifestID, ManifestHash, InputHash, ResultHash string
	ResultJSON, InputJSON, OptionsJSON                                   string
	CancelRequested                                                      bool
}
type Portfolio struct {
	RunID        string             `json:"run_id"`
	ManifestID   string             `json:"manifest_id"`
	ManifestHash string             `json:"manifest_hash"`
	InputHash    string             `json:"input_hash"`
	ResultHash   string             `json:"result_hash"`
	FixtureType  string             `json:"fixture_type"`
	Weights      map[string]float64 `json:"weights"`
	CashWeight   float64            `json:"cash_weight"`
	Options      Options            `json:"constraints"`
}
type Options struct {
	MaxWeight     *float64           `json:"max_weight"`
	MaxWeights    map[string]float64 `json:"max_weights"`
	SectorCaps    map[string]float64 `json:"sector_caps"`
	Sectors       map[string]string  `json:"sectors"`
	LockedSymbols []string           `json:"locked_symbols"`
	MaxTurnover   *float64           `json:"max_turnover"`
}

func finite(n float64) bool       { return !math.IsNaN(n) && !math.IsInf(n, 0) }
func HashBytes(raw []byte) string { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }

// DecodePortfolio accepts only the exact host-verified result bytes stored in
// ResearchJob. Client-supplied weights never enter this contract.
func DecodePortfolio(r ResearchRecord) (Portfolio, error) {
	p := Portfolio{RunID: r.RunID, ManifestID: r.ManifestID, ManifestHash: r.ManifestHash, InputHash: r.InputHash, ResultHash: r.ResultHash}
	if r.Kind != "portfolio" || r.Status != "succeeded" || r.CancelRequested || r.RunID == "" || len(r.ResultJSON) == 0 || len(r.ResultJSON) > 4*1024*1024 || len(r.InputJSON) > 4*1024*1024 || len(r.OptionsJSON) > 4*1024*1024 || HashBytes([]byte(r.ResultJSON)) != r.ResultHash {
		return p, fmt.Errorf("研究任务未完成或权威结果哈希不匹配")
	}
	var body struct {
		Version      int    `json:"schema_version"`
		RunID        string `json:"run_id"`
		Kind         string `json:"kind"`
		Status       string `json:"status"`
		ManifestID   string `json:"manifest_id"`
		ManifestHash string `json:"manifest_hash"`
		Result       struct {
			Solver    string             `json:"solver_status"`
			Promotion string             `json:"promotion"`
			Weights   map[string]float64 `json:"weights"`
			Cash      *float64           `json:"cash_weight"`
			Violation *float64           `json:"max_constraint_violation"`
		} `json:"result"`
	}
	if json.Unmarshal([]byte(r.ResultJSON), &body) != nil || body.Version != 1 || body.RunID != r.RunID || body.Kind != "portfolio" || body.Status != "succeeded" || body.ManifestID != r.ManifestID || body.ManifestHash != r.ManifestHash {
		return p, fmt.Errorf("研究结果身份或manifest不一致")
	}
	if body.Result.Solver != "OPTIMAL" || body.Result.Promotion != "shadow_target_only" || body.Result.Cash == nil || body.Result.Violation == nil || !finite(*body.Result.Violation) || *body.Result.Violation < 0 || *body.Result.Violation > 1e-6 {
		return p, fmt.Errorf("研究优化不可行、约束残差未知或结果不是影子目标")
	}
	var input struct {
		Kind         string   `json:"kind"`
		Currency     string   `json:"currency"`
		Frequency    string   `json:"frequency"`
		Symbols      []string `json:"symbols"`
		Fixture      string   `json:"fixture_type"`
		ManifestID   string   `json:"manifest_id"`
		ManifestHash string   `json:"manifest_hash"`
		Known        bool     `json:"known_at_cutoff"`
	}
	if json.Unmarshal([]byte(r.InputJSON), &input) != nil || input.Kind != "return_matrix" || input.Currency != "CNY" || input.Frequency != "1d" || !input.Known || input.ManifestID != r.ManifestID || input.ManifestHash != r.ManifestHash || len(input.Symbols) < 1 || len(input.Symbols) > 100 || len(input.Symbols) != len(body.Result.Weights) || !sort.StringsAreSorted(input.Symbols) {
		return p, fmt.Errorf("研究输入证券集或可见性不完整")
	}
	seen := map[string]bool{}
	sum := *body.Result.Cash
	if !finite(sum) || sum < 0 || sum >= 1 {
		return p, fmt.Errorf("研究现金权重非法")
	}
	for _, s := range input.Symbols {
		parsed, err := symbol.Parse(s)
		w, ok := body.Result.Weights[s]
		if err != nil || parsed.Tongdaxin() != s || !ashare.IsAStock(s) || seen[s] || !ok || !finite(w) || w < 0 || w > 1 {
			return p, fmt.Errorf("研究权重含缺失/非法证券或数值")
		}
		seen[s] = true
		sum += w
	}
	if math.Abs(sum-1) > 1e-6 {
		return p, fmt.Errorf("研究权重与现金不守恒")
	}
	if json.Unmarshal([]byte(r.OptionsJSON), &p.Options) != nil {
		return p, fmt.Errorf("研究约束不可读取")
	}
	check := func(x float64) bool { return finite(x) && x >= 0 && x <= 1 }
	if p.Options.MaxWeight != nil && !check(*p.Options.MaxWeight) || p.Options.MaxTurnover != nil && !check(*p.Options.MaxTurnover) {
		return p, fmt.Errorf("研究约束范围非法")
	}
	for _, m := range []map[string]float64{p.Options.MaxWeights, p.Options.SectorCaps} {
		for _, x := range m {
			if !check(x) {
				return p, fmt.Errorf("研究约束数值非法")
			}
		}
	}
	for _, s := range p.Options.LockedSymbols {
		if !seen[s] {
			return p, fmt.Errorf("锁定证券不在研究集合")
		}
	}
	p.Weights, p.CashWeight, p.FixtureType = body.Result.Weights, *body.Result.Cash, input.Fixture
	return p, nil
}
