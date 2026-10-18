import copy
from datetime import datetime, timezone
import json
from pathlib import Path
import tempfile
import unittest
from nt_research.prediction import TARGET, archive, outcome, validate_forecast, evaluate_predictions
from nt_research.protocol import Invalid, canonical, sha
from nt_research.runner import run
from test_research import write_job


def forecast(index=0, test=False):
    dates = ["2026-09-02", "2026-09-03"] if test else ["2026-08-25", "2026-08-26"]
    return {"prediction_id": ("test-" if test else "train-") + str(index), "symbol": f"{index+1:06d}.SZ", "origin": "synthetic",
            "created_at": "2026-09-01T08:00:00+08:00" if test else "2026-08-24T08:00:00+08:00", "model_version": "synthetic-model-v1",
            "feature_hash": sha(str(index).encode()), "model_trained_until": "2026-07-31T00:00:00Z", "market_state": "synthetic_range", "liquidity_bucket": "synthetic_liquid",
            "score_semantics": "uncalibrated_rank", "score": .9 if index % 2 else .1, "rule_score": .5,
            "target": {"definition": TARGET, "calendar_version": "synthetic-calendar-v1", "execution_policy": "explicit_fills_only", "trading_sessions": dates,
                       "entry_at": dates[0]+"T09:30:00+08:00", "exit_at": dates[-1]+"T15:00:00+08:00",
                       "costs": {"buy_fee_bps": 3, "sell_fee_bps": 8, "slippage_bps": 5}}}


def label(row, positive=True):
    return {"origin": "synthetic", "fill_status": "filled", "trading_state": "complete", "entry_at": row["target"]["entry_at"],
            "exit_at": row["target"]["exit_at"], "entry_price": 10, "exit_price": 11 if positive else 9,
            "available_at": row["target"]["trading_sessions"][-1]+"T15:01:00+08:00", "observed_at": row["target"]["trading_sessions"][-1]+"T15:02:00+08:00"}


def job(kind="prediction_archive", run_id="archive1", as_of="2026-08-01T00:00:00Z"):
    return {"schema_version": 1, "run_id": run_id, "kind": kind, "as_of": as_of,
            "manifest": {"id": "sha256:"+"a"*64, "hash": "a"*64}, "input": {"sha256": "b"*64}}


class PredictionTests(unittest.TestCase):
    def test_append_only_idempotent_and_changed_identity_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            output=Path(tmp)/"run";output.mkdir();data={"kind":"prediction_batch","predictions":[forecast()]}
            first=archive(job(),data,output);second=archive(job(),data,output)
            self.assertEqual(first["archived"],second["archived"])
            data["predictions"][0]["score"] = .8
            with self.assertRaises(Invalid):archive(job(),data,output)
            self.assertIsNone(first["display_probability"])

    def test_prospective_backdate_future_evidence_and_t_plus_one_guards(self):
        row=forecast();now=datetime(2026,8,24,0,1,tzinfo=timezone.utc);row["origin"]="prospective"
        validate_forecast(row,job(),now)
        with self.assertRaises(Invalid):validate_forecast(row,job(),datetime(2026,8,24,1,tzinfo=timezone.utc))
        row["model_trained_until"]="2027-01-01T00:00:00Z"
        with self.assertRaises(Invalid):validate_forecast(row,job(),now)
        row=forecast();row["target"]["trading_sessions"]=row["target"]["trading_sessions"][:1]
        with self.assertRaises(Invalid):validate_forecast(row,job(),now)

    def test_fees_unknown_execution_and_delisting_are_explicit(self):
        row=forecast();stamp=datetime(2026,10,15,tzinfo=timezone.utc)
        realized,reason=outcome(row,label(row),stamp)
        self.assertIsNone(reason);self.assertLess(realized,.1)
        for field,value in [("fill_status","not_filled"),("trading_state","suspended"),("trading_state","limit_locked"),("trading_state","delisted"),("entry_price",None)]:
            item=label(row);item[field]=value
            self.assertIsNone(outcome(row,item,stamp)[0])
        item=label(row);item.update(trading_state="delisted",delisting_policy="confirmed_liquidation",exit_price=0)
        self.assertEqual(outcome(row,item,stamp)[0],-1)
        item=label(row);item["observed_at"]="2027-01-01T00:00:00Z"
        self.assertEqual(outcome(row,item,stamp)[1],"outcome_not_yet_available")

    def dataset(self, root, ntrain=200, ntest=100):
        output=root/"archive-run";output.mkdir()
        rows=[forecast(i) for i in range(ntrain)]+[forecast(i,True) for i in range(ntest)]
        refs=archive(job(),{"kind":"prediction_batch","predictions":rows},output)["archived"]
        observations=[dict(ref,label=label(row,i%2==1)) for i,(ref,row) in enumerate(zip(refs,rows))]
        data={"kind":"prediction_outcomes","time_split":{"calibration_end":"2026-08-31T00:00:00Z","test_start":"2026-09-01T00:00:00Z"},"observations":observations}
        return rows,data

    def test_real_isotonic_package_time_split_and_no_production_probability(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp);_,data=self.dataset(root);out=root/"eval";out.mkdir()
            result=evaluate_predictions(job("prediction_evaluate",as_of="2026-10-15T00:00:00Z"),data,out)
            self.assertEqual(result["calibration_samples"],200);self.assertEqual(result["test_labels"],100)
            self.assertAlmostEqual(result["metrics"]["brier"],0);self.assertAlmostEqual(result["metrics"]["ece_10_bins"],0)
            self.assertEqual(result["state"],"synthetic");self.assertFalse(result["production_approved"]);self.assertIsNone(result["display_probability"])
            calibration=(out/"calibration.json").read_bytes()
            # Alter only sealed test outcomes: fit artifact stays byte-identical.
            for observation in data["observations"][200:]:observation["label"]["exit_price"]=10.5
            again=evaluate_predictions(job("prediction_evaluate",as_of="2026-10-15T00:00:00Z"),data,out)
            self.assertEqual(calibration,(out/"calibration.json").read_bytes());self.assertGreater(again["metrics"]["brier"],0)

    def test_insufficient_samples_unknown_and_hash_tampering_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp);_,data=self.dataset(root,2,2);out=root/"eval";out.mkdir()
            result=evaluate_predictions(job("prediction_evaluate",as_of="2026-10-15T00:00:00Z"),data,out)
            self.assertEqual(result["calibration_state"],"unknown");self.assertIsNone(result["metrics"]["brier"])
            data["observations"][0]["archive_sha256"]="0"*64
            with self.assertRaises(Invalid):evaluate_predictions(job("prediction_evaluate",as_of="2026-10-15T00:00:00Z"),data,out)

    def test_cli_protocol_archives_and_preserves_unknown_evaluation(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp);row=forecast(1,True)
            request=job();write_job(root,request,{"kind":"prediction_batch","predictions":[row]})
            result,path=run(root/"job.json",root/"runs");self.assertEqual(result["status"],"succeeded",result)
            reference=result["result"]["archived"][0]
            data={"kind":"prediction_outcomes","time_split":{"calibration_end":"2026-08-31T00:00:00Z","test_start":"2026-09-01T00:00:00Z"},"observations":[dict(reference,label=label(row))]}
            write_job(root,job("prediction_evaluate","eval",as_of="2026-10-15T00:00:00Z"),data)
            result,path=run(root/"job.json",root/"runs");self.assertEqual(result["status"],"succeeded",result)
            self.assertEqual(result["result"]["calibration_state"],"unknown")


if __name__=="__main__":unittest.main()
