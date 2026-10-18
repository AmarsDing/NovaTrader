"""Create protocol demos, never market evidence. Run from research-worker/."""
from pathlib import Path
import sys
sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from nt_research.protocol import canonical, dataset_canonical, sha

root = Path(__file__).resolve().parent / "synthetic"
root.mkdir(exist_ok=True)
cutoff = "2026-10-15T07:30:00Z"
manifest = {"schema_version": 1, "manifest_id": "", "hash": "", "from": "2026-10-01T00:00:00Z", "to": cutoff,
            "cutoff": cutoff, "freq": "1d", "calendar": [], "universe": [], "records": [], "excluded": [],
            "replay_excluded": None, "raw_hash_kind": "canonical_normalized_payload_sha256"}
manifest["hash"] = sha(dataset_canonical(manifest)); manifest["manifest_id"] = "sha256:" + manifest["hash"]
(root / "manifest.json").write_bytes(canonical(manifest))
days = ["2026-10-09", "2026-10-12", "2026-10-13", "2026-10-14"]
nav = {"kind": "account_nav", "fixture_type": "synthetic_not_market_evidence", "fee_policy": "net_of_costs", "frequency": "1d",
       "currency": "CNY", "ledger_hash": "synthetic-ledger-no-real-account", "calendar_version": "synthetic-calendar",
       "cash_flow_policy": "no_external_flows", "benchmark_kind": "price_index", "benchmark_id": "synthetic-index", "expected_dates": days,
       "points": [{"date": day, "nav": n, "benchmark": b, "external_flow": 0, "valuation_complete": True}
                  for day, n, b in zip(days, [100, 90, 99, 95], [100, 101, 102, 101])]}
matrix = {"kind": "return_matrix", "fixture_type": "synthetic_not_market_evidence", "currency": "CNY", "frequency": "1d", "known_at_cutoff": True,
          "symbols": ["000001.SZ", "600000.SH", "600519.SH"], "dates": days,
          "returns": [[.01, .02, -.01], [-.01, .01, .02], [.02, -.02, .01], [-.005, .002, -.003]]}
for kind, data, options in [("performance", nav, {}), ("portfolio", matrix, {"cash_floor": .1, "max_weight": .4, "solver": "CLARABEL"})]:
    data.update(manifest_id=manifest["manifest_id"], manifest_hash=manifest["hash"])
    body = canonical(data); (root / (kind + ".input.json")).write_bytes(body)
    job = {"schema_version": 1, "run_id": "synthetic-" + kind, "kind": kind, "as_of": cutoff, "seed": 7,
           "manifest": {"id": manifest["manifest_id"], "hash": manifest["hash"], "path": "manifest.json", "sha256": sha((root / "manifest.json").read_bytes())},
           "input": {"path": kind + ".input.json", "sha256": sha(body)}, "options": options}
    (root / (kind + ".job.json")).write_bytes(canonical(job))
print(root)
