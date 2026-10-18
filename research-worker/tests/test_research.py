import copy
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
from nt_research.analytics import nav_returns, reference_metrics, performance, portfolio, vector_screen
from nt_research.evaluate import evaluate
from nt_research.protocol import Invalid, Unavailable, canonical, check_job, sha, dataset_canonical, verify_manifest
from nt_research.runner import run
from nt_research.sandbox import command, stage_inputs, artifact_budget


def base_job():
    return {"schema_version":1,"run_id":"synthetic-fixture","kind":"performance","as_of":"2026-10-15T15:30:00+08:00","seed":7}


def nav():
    dates=["2026-10-09","2026-10-12","2026-10-13","2026-10-14"]
    return {"kind":"account_nav","fee_policy":"net_of_costs","frequency":"1d","currency":"CNY","ledger_hash":"synthetic-ledger-not-real-account","calendar_version":"synthetic-calendar","cash_flow_policy":"no_external_flows","benchmark_kind":"price_index","benchmark_id":"synthetic-index","expected_dates":dates,"points":[{"date":date,"nav":n,"benchmark":b,"external_flow":0,"valuation_complete":True} for date,n,b in zip(dates,[100,90,99,95],[100,101,102,101])]}


def matrix():
    return {"kind":"return_matrix","currency":"CNY","frequency":"1d","known_at_cutoff":True,"symbols":["000001.SZ","600000.SH","600519.SH"],"dates":["2026-10-09","2026-10-12","2026-10-13","2026-10-14"],"returns":[[.01,.02,-.01],[-.01,.01,.02],[.02,-.02,.01],[-.005,.002,-.003]]}


def write_job(root,job=None,data=None):
    job=job or base_job();data=data or nav()
    manifest={"schema_version":1,"manifest_id":"","hash":"","cutoff":job["as_of"],"from":"2026-10-01T00:00:00Z","to":job["as_of"],"freq":"1d","calendar":[],"universe":[],"records":[],"excluded":[],"replay_excluded":None,"raw_hash_kind":"canonical_normalized_payload_sha256"}
    manifest["hash"]=sha(dataset_canonical(manifest));manifest["manifest_id"]="sha256:"+manifest["hash"]
    data.update(manifest_id=manifest["manifest_id"],manifest_hash=manifest["hash"])
    for path,value in [("manifest.json",manifest),("input.json",data)]: (root/path).write_bytes(canonical(value))
    job["manifest"]={"id":manifest["manifest_id"],"hash":manifest["hash"],"path":"manifest.json","sha256":sha((root/"manifest.json").read_bytes())}
    job["input"]={"path":"input.json","sha256":sha((root/"input.json").read_bytes())}
    (root/"job.json").write_bytes(canonical(job));return job


class ContractTests(unittest.TestCase):
    def test_go_generated_manifest_cross_language_hash(self):
        path=Path(__file__).resolve().parents[2]/"backend/pkg/dataset/testdata/manifest_v1.json"
        self.assertTrue(path.exists(),"Go canonical fixture is required")
        body=path.read_bytes();verify_manifest(body)
        body=body.replace(b"9007199254740993",b"9007199254740992")
        with self.assertRaises(Invalid):verify_manifest(body)

    def test_internal_manifest_tampering_cannot_hide_behind_file_hash(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp);job=write_job(root)
            manifest=json.loads((root/"manifest.json").read_bytes());manifest["universe"]=["tampered"]
            body=canonical(manifest);(root/"manifest.json").write_bytes(body);job["manifest"]["sha256"]=sha(body)
            with self.assertRaises(Invalid):check_job(job,root)

    def test_initial_nav_includes_first_loss(self):
        _,values,_,returns,_=nav_returns(base_job(),nav());metrics=reference_metrics(values,returns)
        self.assertAlmostEqual(metrics["max_drawdown"],.1);self.assertAlmostEqual(metrics["total_return"],-.05)
        self.assertEqual(metrics["return_periods"],3)

    def test_zero_trade_is_not_missing(self):
        data=nav()
        for point in data["points"]:point["nav"]=100
        _,values,_,returns,_=nav_returns(base_job(),data)
        self.assertEqual(reference_metrics(values,returns)["total_return"],0)
        self.assertIsNone(reference_metrics(values,returns)["sharpe"])

    def test_missing_single_point_flow_and_future_rejected(self):
        cases=[]
        d=nav();d["points"][1]["nav"]=None;cases.append(d)
        d=nav();d["points"]=d["points"][:1];d["expected_dates"]=d["expected_dates"][:1];cases.append(d)
        d=nav();d["points"][1]["external_flow"]=100;cases.append(d)
        d=nav();d["points"][-1]["date"]="2027-01-01";d["expected_dates"][-1]="2027-01-01";cases.append(d)
        d=nav();d["points"][1]["valuation_complete"]=False;cases.append(d)
        for data in cases:
            with self.assertRaises(Invalid):nav_returns(base_job(),data)

    def test_hash_tamper_path_and_secrets_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp);job=write_job(root);check_job(job,root)
            (root/"input.json").write_text("{}")
            with self.assertRaises(Invalid):check_job(job,root)
            job=write_job(root);job["input"]["path"]="../secret.json"
            with self.assertRaises(Invalid):check_job(job,root)
            job=write_job(root);job["options"]={"broker_api_key":"never accepted"}
            with self.assertRaises(Invalid):check_job(job,root)

    def test_missing_dependency_has_persisted_unavailable_result(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp);write_job(root)
            with patch("nt_research.analytics.require",side_effect=Unavailable("dependency_unavailable:quantstats")):
                result,path=run(root/"job.json",root/"runs")
            self.assertEqual(result["status"],"unavailable");self.assertTrue(path.exists());self.assertTrue(result["artifact_hash"])
            with self.assertRaises(FileExistsError):run(root/"job.json",root/"runs")

    def test_vector_license_gate_precedes_import(self):
        with self.assertRaisesRegex(Unavailable,"license_opt_in"):vector_screen(base_job(),matrix(),Path("."))

    def test_rd_container_is_offline_pinned_and_bounded(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp);(root/"features.bin").write_bytes(b"synthetic")
            job=base_job();job["options"]={"image":"example/research@sha256:"+"b"*64,"max_loops":2,"timeout_seconds":10}
            data={"kind":"rd_agent_bundle","adapter":"novatrader-qlib-v1","base_features_path":"features.bin","files":[{"path":"features.bin","sha256":sha(b"synthetic")}]}
            args=command(job,data,root,root/"output")
            for flag in ["--network=none","--read-only","--cap-drop=ALL","--pull=never","--memory=2g"]:self.assertIn(flag,args)
            self.assertNotIn("--env-file",args);self.assertNotIn("/var/run/docker.sock"," ".join(args))
            (root/"unlisted-secret.txt").write_text("must never mount")
            staged=root/"staged";stage_inputs(data,root,staged)
            self.assertEqual([p.name for p in staged.iterdir()],["features.bin"])
            self.assertTrue(artifact_budget(staged))
            job["options"]["image"]="example/research:latest"
            with self.assertRaises(Invalid):command(job,data,root,root/"output")

    def test_synthetic_labels_never_pass_real_sample_gate_and_split_leak_fails(self):
        job=base_job();data={"kind":"jev_labels","label_schema":"public-news-labels-v1","test_start":"2026-10-12T00:00:00+08:00","rows":[]}
        for i in range(10):
            data["rows"].append({"id":str(i),"published_at":"2026-10-13T10:00:00+08:00","split":"test","label":i%2==0,"stratum":"denial","annotation":"synthetic","predictions":{"jev":{"probability":.9 if i%2==0 else .1,"actual_model":"fixture-model","input_hash":"synthetic","duration_ms":1}}})
        out=evaluate(job,data,Path("."));self.assertFalse(out["minimum_human_labels_met"]);self.assertEqual(out["methods"]["jev"]["precision"],1);self.assertEqual(out["synthetic_count"],10)
        data["rows"][0]["split"]="development"
        with self.assertRaises(Invalid):evaluate(job,data,Path("."))


@unittest.skipUnless(importlib.util.find_spec("quantstats"),"isolated optional QuantStats dependency is unavailable")
class QuantStatsIntegration(unittest.TestCase):
    def test_real_package_uses_same_nav_without_downloading(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp);write_job(root)
            import yfinance
            with patch.object(yfinance,"download",side_effect=AssertionError("network download forbidden")):
                result,path=run(root/"job.json",root/"runs")
            self.assertEqual(result["status"],"succeeded",result)
            comparison=result["result"]["comparison"]
            for key in ["total_return","annual_return","sharpe","max_drawdown"]:self.assertAlmostEqual(comparison[key]["difference"],0,places=8)
            self.assertTrue((path.parent/"performance.html").exists())

    def test_real_package_constant_nav_preserves_undefined_sharpe(self):
        with tempfile.TemporaryDirectory() as tmp:
            data=nav()
            for point in data["points"]:point["nav"]=100
            out=performance(base_job(),data,Path(tmp))
            self.assertEqual(out["metrics"]["total_return"],0)
            self.assertEqual(out["metrics"]["max_drawdown"],0)
            self.assertIsNone(out["quantstats"]["sharpe"])


@unittest.skipUnless(importlib.util.find_spec("pypfopt"),"isolated optional PyPortfolioOpt dependency is unavailable")
class PortfolioIntegration(unittest.TestCase):
    def test_real_solver_feasible_and_infeasible(self):
        job=base_job();job["options"]={"max_weight":.4,"cash_floor":.1,"solver":"CLARABEL"}
        one=portfolio(job,matrix(),Path("."));two=portfolio(job,matrix(),Path("."))
        self.assertEqual(one["solver_status"],"OPTIMAL",one);self.assertEqual(one["weights"],two["weights"])
        self.assertAlmostEqual(sum(one["weights"].values())+one["cash_weight"],1)
        self.assertLessEqual(max(one["weights"].values()),.4+1e-6)
        job["options"]["max_weight"]=.1
        self.assertEqual(portfolio(job,matrix(),Path("."))["solver_status"],"INFEASIBLE")

    def test_locks_turnover_and_sector_constraints(self):
        job=base_job();job["options"]={"max_weight":.5,"cash_floor":.1,"current_weights":{"000001.SZ":.3,"600000.SH":.3,"600519.SH":.3},"locked_symbols":["000001.SZ"],"max_turnover":.05,"sectors":{"000001.SZ":"bank","600000.SH":"bank","600519.SH":"other"},"sector_caps":{"bank":.65}}
        out=portfolio(job,matrix(),Path("."));self.assertEqual(out["solver_status"],"OPTIMAL",out)
        self.assertAlmostEqual(out["weights"]["000001.SZ"],.3,places=6)
        self.assertLessEqual(out["weights"]["000001.SZ"]+out["weights"]["600000.SH"],.65+1e-6)


if __name__=="__main__":unittest.main()
