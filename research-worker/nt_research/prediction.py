"""Append-only prospective forecasts and purged, offline calibration evaluation.

This is a local application-level archive, not a notarized/WORM evidence store.
Neither endpoint exports a production probability or an order.
"""
from __future__ import annotations
from bisect import bisect_right
from datetime import datetime, timezone, timedelta
import json
import math
from pathlib import Path
import re
import statistics
from .protocol import Invalid, canonical, digest, finite, require, sha, timestamp

TARGET = "fee_net_next_open_to_close_return_v1"
ID = re.compile(r"[A-Za-z0-9_-]{1,80}")


def archive_root(output):
    return output.parent / "prediction-archive"


def validate_forecast(row, job, now):
    if not ID.fullmatch(row.get("prediction_id", "")) or not re.fullmatch(r"\d{6}\.(SH|SZ|BJ)", row.get("symbol", "")):
        raise Invalid("prediction identity/symbol is invalid")
    if row.get("origin") not in {"synthetic", "prospective"}:
        raise Invalid("prediction origin must be explicit")
    for name in ("model_version", "market_state", "liquidity_bucket"):
        if not isinstance(row.get(name), str) or not row[name] or len(row[name]) > 128:
            raise Invalid("version/stratum is missing")
    if not re.fullmatch(r"[0-9a-f]{64}", row.get("feature_hash", "")):
        raise Invalid("feature snapshot hash required")
    created = timestamp(row.get("created_at"))
    if timestamp(job["as_of"]) > created or timestamp(row.get("model_trained_until")) > created:
        raise Invalid("forecast uses future evidence/model")
    if created > now:
        raise Invalid("future issue time")
    if row["origin"] == "prospective" and (now - created).total_seconds() > 300:
        raise Invalid("prospective forecast cannot be backdated by more than five minutes")
    if row.get("score_semantics") not in {"uncalibrated_rank", "unvalidated_return_estimate"}:
        raise Invalid("raw score is not a calibrated probability")
    finite(row.get("score"), "score"); finite(row.get("rule_score"), "rule_score")
    if any("probability" in key for key in row):
        raise Invalid("production probability is unavailable before independent calibration admission")
    target = row.get("target", {})
    if target.get("definition") != TARGET or target.get("execution_policy") != "explicit_fills_only" or not target.get("calendar_version"):
        raise Invalid("freeze an explicit target, calendar and execution policy")
    sessions = target.get("trading_sessions", [])
    if not isinstance(sessions, list) or not 2 <= len(sessions) <= 21 or sessions != sorted(set(sessions)):
        raise Invalid("T+1 requires at least two unique ordered trading sessions")
    for session in sessions:
        try:
            day = datetime.strptime(session, "%Y-%m-%d")
            if day.weekday() >= 5:
                raise ValueError("weekend")
        except (ValueError, TypeError) as exc:
            raise Invalid("invalid calendar session") from exc
    cn = timezone(timedelta(hours=8))
    entry = timestamp(target.get("entry_at")).astimezone(cn)
    exit_at = timestamp(target.get("exit_at")).astimezone(cn)
    if entry.strftime("%Y-%m-%d") != sessions[0] or exit_at.strftime("%Y-%m-%d") != sessions[-1]:
        raise Invalid("entry/exit must match frozen session horizon")
    if entry.strftime("%H:%M:%S") != "09:30:00" or exit_at.strftime("%H:%M:%S") != "15:00:00" or entry.microsecond or exit_at.microsecond:
        raise Invalid("target requires next-session open to a later close")
    if not created < entry < exit_at or (row["origin"] == "prospective" and now >= entry):
        raise Invalid("forecast must be archived before entry")
    costs = target.get("costs", {})
    for key in ("buy_fee_bps", "sell_fee_bps", "slippage_bps"):
        if not 0 <= finite(costs.get(key), key) <= 1000:
            raise Invalid("explicit bounded two-sided costs required")


def archive(job, data, output, now=None):
    if data.get("kind") != "prediction_batch":
        raise Invalid("prediction batch required")
    rows = data.get("predictions", [])
    if not rows or len(rows) > 10000 or len({row.get("prediction_id") for row in rows}) != len(rows):
        raise Invalid("missing, duplicate or excessive predictions")
    now = now or datetime.now(timezone.utc)
    store = archive_root(output); store.mkdir(exist_ok=True)
    prepared = []
    for row in rows:
        validate_forecast(row, job, now)
        content = {"forecast": row, "manifest_id": job["manifest"]["id"], "manifest_hash": job["manifest"]["hash"],
                   "input_sha256": job["input"]["sha256"], "evidence_as_of": job["as_of"]}
        content_hash = digest(content)
        folder = store / row["prediction_id"]
        if folder.exists():
            path = folder / "forecast.json"
            if folder.is_symlink() or not path.is_file() or path.is_symlink():
                raise Invalid("archive entry incomplete or unsafe; never overwrite")
            previous = json.loads(path.read_bytes())
            if previous.get("content_hash") != content_hash or digest(previous.get("content")) != content_hash:
                raise Invalid("prediction identity already exists with different content")
        prepared.append((folder, content, content_hash))
    references = []
    for folder, content, content_hash in prepared:
        try:
            folder.mkdir()  # exclusive claim: each prediction is append-only across run IDs
        except FileExistsError:
            # Concurrent identical work may still be writing. Do not claim it is done.
            path = folder / "forecast.json"
            if not path.is_file() or json.loads(path.read_bytes()).get("content_hash") != content_hash:
                raise Invalid("concurrent archive conflict; retry without changing content")
        else:
            entry = {"schema_version": 1, "archived_at": now.isoformat(), "content_hash": content_hash, "content": content}
            pending = folder / "forecast.pending.json"; pending.write_bytes(canonical(entry)); pending.replace(folder / "forecast.json")
        path = folder / "forecast.json"
        references.append({"prediction_id": folder.name, "archive_sha256": sha(path.read_bytes()), "content_hash": content_hash})
    (output / "predictions.json").write_bytes(canonical(references))
    return {"archived": references, "count": len(references), "display_probability": None,
            "state": "synthetic" if all(row["origin"] == "synthetic" for row in rows) else "prospective_archived",
            "production_approved": False, "note": "Each record is immutable through this API; filesystem administrators can alter files. No probability or trade is published."}


def load_prediction(reference, output):
    identity = reference.get("prediction_id", "")
    if not ID.fullmatch(identity):
        raise Invalid("invalid archive reference")
    folder = archive_root(output) / identity
    path = folder / "forecast.json"
    if folder.is_symlink() or path.is_symlink() or not path.is_file() or path.stat().st_size > 1024 * 1024:
        raise Invalid("missing or unsafe immutable forecast")
    body = path.read_bytes()
    if sha(body) != reference.get("archive_sha256"):
        raise Invalid("archive hash mismatch")
    value = json.loads(body)
    if value.get("schema_version") != 1 or digest(value.get("content")) != value.get("content_hash"):
        raise Invalid("archive content hash mismatch")
    return value


def outcome(forecast, label, as_of):
    """No fill, unavailable prices, suspension or unsettled delisting => unknown."""
    if not isinstance(label, dict):
        return None, "label_missing"
    if label.get("origin") != forecast["origin"]:
        raise Invalid("synthetic labels cannot be attached to real forecasts")
    target = forecast["target"]
    if label.get("fill_status") != "filled":
        return None, "fill_unconfirmed"
    state = label.get("trading_state")
    if state in {"suspended", "limit_locked", "missing"}:
        return None, state
    if state == "delisted" and label.get("delisting_policy") != "confirmed_liquidation":
        return None, "delisting_unresolved"
    if state not in {"complete", "delisted"}:
        return None, "trading_state_unknown"
    if label.get("entry_at") != target["entry_at"] or label.get("exit_at") != target["exit_at"]:
        return None, "execution_horizon_mismatch"
    available = timestamp(label.get("available_at"))
    observed = timestamp(label.get("observed_at"))
    if available < timestamp(target["exit_at"]) or observed < available:
        raise Invalid("outcome is published before it can be known")
    if observed > as_of or available > as_of:
        return None, "outcome_not_yet_available"
    if forecast["origin"] == "prospective" and (not label.get("source") or not re.fullmatch(r"[0-9a-f]{64}", label.get("evidence_hash", ""))):
        return None, "label_evidence_missing"
    if label.get("entry_price") is None or label.get("exit_price") is None:
        return None, "execution_price_missing"
    entry = finite(label.get("entry_price"), "entry_price"); exit_price = finite(label.get("exit_price"), "exit_price")
    if entry <= 0 or exit_price < 0 or (exit_price == 0 and state != "delisted"):
        raise Invalid("invalid executable price")
    costs = target["costs"]
    buy = entry * (1 + (costs["buy_fee_bps"] + costs["slippage_bps"]) / 10000)
    sell = exit_price * (1 - (costs["sell_fee_bps"] + costs["slippage_bps"]) / 10000)
    return sell / buy - 1, None


def calibration_metrics(probabilities, outcomes):
    if not probabilities:
        return {"brier": None, "ece_10_bins": None, "samples": 0}
    labels = [int(value > 0) for value in outcomes]
    brier = statistics.mean((p - y) ** 2 for p, y in zip(probabilities, labels))
    bins = []; ece = 0.0
    for index in range(10):
        selected = [(p, y) for p, y in zip(probabilities, labels) if min(int(p * 10), 9) == index]
        if selected:
            confidence = statistics.mean(p for p, _ in selected); observed = statistics.mean(y for _, y in selected)
            ece += len(selected) / len(labels) * abs(confidence - observed)
            bins.append({"bin": index, "count": len(selected), "mean_probability": confidence, "observed_positive": observed})
    return {"brier": brier, "ece_10_bins": ece, "samples": len(labels), "bins": bins}


def score_drift(training, test):
    if len(training) < 50 or len(test) < 50:
        return {"state": "unknown", "reason": "insufficient_drift_samples", "psi": None}
    ordered = sorted(training)
    cuts = sorted(set(ordered[min(len(ordered) - 1, len(ordered) * i // 10)] for i in range(1, 10)))
    def proportions(values):
        counts = [0] * (len(cuts) + 1)
        for value in values:
            counts[bisect_right(cuts, value)] += 1
        return [(count + .5) / (len(values) + .5 * len(counts)) for count in counts]
    a, b = proportions(training), proportions(test)
    psi = sum((q - p) * math.log(q / p) for p, q in zip(a, b))
    return {"state": "shift_detected" if psi >= .2 else "no_large_shift_detected", "psi": psi, "threshold": .2,
            "definition": "training-decile raw-score PSI with Jeffreys smoothing; descriptive, not a significance test"}


def evaluate_predictions(job, data, output):
    if data.get("kind") != "prediction_outcomes":
        raise Invalid("prediction outcomes required")
    split = data.get("time_split", {})
    calibration_end = timestamp(split.get("calibration_end")); test_start = timestamp(split.get("test_start"))
    if calibration_end >= test_start or test_start > timestamp(job["as_of"]):
        raise Invalid("freeze calibration/gap/test chronology")
    refs = data.get("observations", [])
    if not refs or len(refs) > 10000 or len({r.get("prediction_id") for r in refs}) != len(refs):
        raise Invalid("missing or duplicate prediction observations")
    training = []; test = []; excluded = []; cohorts = set(); origins = set(); identities = set()
    for reference in refs:
        stored = load_prediction(reference, output); row = stored["content"]["forecast"]
        target = row["target"]; origins.add(row["origin"])
        identity = (row["symbol"], target["entry_at"], target["exit_at"], row["model_version"])
        if identity in identities:
            raise Invalid("duplicate target observation cannot inflate sample size")
        identities.add(identity)
        cohorts.add(digest({"model": row["model_version"], "definition": target["definition"], "horizon": len(target["trading_sessions"]),
                           "costs": target["costs"], "score_semantics": row["score_semantics"]}))
        issued = timestamp(row["created_at"])
        if row["origin"] == "prospective" and timestamp(stored["archived_at"]) >= timestamp(target["entry_at"]):
            raise Invalid("real archive is not prospective")
        realized, reason = outcome(row, reference.get("label"), timestamp(job["as_of"]))
        item = {"row": row, "realized": realized, "reason": reason, "archive_sha256": reference["archive_sha256"]}
        label = reference.get("label") or {}
        if issued >= test_start:
            test.append(item)
        elif issued < calibration_end and realized is not None and timestamp(label["observed_at"]) <= calibration_end:
            training.append(item)
        else:
            excluded.append({"prediction_id": row["prediction_id"], "reason": reason or "purged_boundary_or_unknown_training_label"})
    if len(cohorts) != 1 or len(origins) != 1:
        raise Invalid("do not mix target horizons/model/cost definitions or real/synthetic evidence")
    if not test:
        raise Invalid("sealed test prediction partition is empty")
    labeled = [item for item in test if item["realized"] is not None]
    coverage = len(labeled) / len(test)
    synthetic = origins == {"synthetic"}
    # Fixed evidence threshold cannot be weakened through options.
    enough = len(training) >= 200 and len(labeled) >= 100 and len({item["realized"] > 0 for item in training}) == 2
    report = {"target": TARGET, "state": "synthetic" if synthetic else "unknown", "production_approved": False,
              "display_probability": None, "calibration_samples": len(training), "test_predictions": len(test), "test_labels": len(labeled),
              "coverage": coverage, "abstained_or_unresolved": [{"prediction_id": item["row"]["prediction_id"], "reason": item["reason"]} for item in test if item["realized"] is None],
              "excluded": excluded, "time_split": split, "metrics": {"brier": None, "ece_10_bins": None},
              "baseline": {"cash_realized_return": 0, "rule_calibrated_brier": None, "training_prior_brier": None},
              "drift": score_drift([i["row"]["score"] for i in training], [i["row"]["score"] for i in test]),
              "calibration_state": "unknown", "reason": "insufficient_disjoint_calibration_or_test_evidence",
              "definitions": {"label": "positive unit notional return after frozen two-sided bps costs, conditional on confirmed fills",
                              "t_plus_one": "exit is at least the next frozen trading session after entry",
                              "unknown": "missing fill/price, suspension, locked limit, unresolved delisting or late label are not zero returns",
                              "limits": "not account NAV; per-unit bps does not model broker minimum commissions, portfolio cash or lot rounding"}}
    for name in ("market_state", "liquidity_bucket"):
        report[name] = []
        for group in sorted({item["row"][name] for item in test}):
            samples = [item["realized"] for item in test if item["row"][name] == group and item["realized"] is not None]
            report[name].append({"name": group, "predictions": sum(item["row"][name] == group for item in test), "labels": len(samples),
                                 "mean_net_return": statistics.mean(samples) if samples else None,
                                 "observed_min_max": [min(samples), max(samples)] if samples else None})
    if enough:
        isotonic = require("scikit-learn", "sklearn.isotonic")
        labels = [int(item["realized"] > 0) for item in training]
        models = {}
        for name in ("score", "rule_score"):
            model = isotonic.IsotonicRegression(out_of_bounds="clip").fit([item["row"][name] for item in training], labels)
            probabilities = [float(value) for value in model.predict([item["row"][name] for item in labeled])]
            models[name] = {"x": model.X_thresholds_.tolist(), "y": model.y_thresholds_.tolist()}
            metrics = calibration_metrics(probabilities, [item["realized"] for item in labeled])
            if name == "score": report["metrics"] = metrics
            else: report["baseline"]["rule_calibrated_brier"] = metrics["brier"]
        report["baseline"]["training_prior_brier"] = calibration_metrics([statistics.mean(labels)] * len(labeled), [i["realized"] for i in labeled])["brier"]
        artifact = {"schema_version": 1, "method": "isotonic", "fit_until": split["calibration_end"], "models": models,
                    "training_archives": [item["archive_sha256"] for item in training],
                    "training_labels_hash": digest([{ "archive": item["archive_sha256"], "return": item["realized"]} for item in training]), "origin": next(iter(origins)),
                    "promotion": "offline_research_only"}
        (output / "calibration.json").write_bytes(canonical(artifact))
        report.update(calibration_state="synthetic_diagnostic" if synthetic else "offline_fit_only", calibration_hash=digest(artifact),
                      state="synthetic" if synthetic else "research_only", reason="independent_production_admission_not_granted")
    (output / "prediction-evaluation.json").write_bytes(canonical(report))
    return report
