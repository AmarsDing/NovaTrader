"""Versioned, content-addressed file protocol; no implicit market downloads."""
from __future__ import annotations
import hashlib
import importlib.metadata
import json
import math
import platform
import re
from datetime import datetime
from decimal import Decimal
from pathlib import Path


class Invalid(ValueError):
    pass


class Unavailable(RuntimeError):
    pass


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False, allow_nan=False).encode()


def digest(value):
    return hashlib.sha256(canonical(value)).hexdigest()


def sha(data):
    return hashlib.sha256(data).hexdigest()


def dataset_canonical(value):
    """Match pkg/dataset: sorted keys, precise decimal numbers, Go HTML escaping."""
    def encode(item):
        if item is None:
            return "null"
        if isinstance(item, bool):
            return "true" if item else "false"
        if isinstance(item, (int, float, Decimal)):
            number = Decimal(str(item))
            if not number.is_finite() or abs(number.as_tuple().exponent) > 10000:
                raise Invalid("manifest number exceeds canonical safety bound")
            if number == 0:
                return "0"
            text = format(number, "f")
            return text.rstrip("0").rstrip(".") if "." in text else text
        if isinstance(item, str):
            return json.dumps(item, ensure_ascii=False).replace("<", "\\u003c").replace(">", "\\u003e").replace("&", "\\u0026").replace("\u2028", "\\u2028").replace("\u2029", "\\u2029")
        if isinstance(item, list):
            return "[" + ",".join(encode(child) for child in item) + "]"
        if isinstance(item, dict):
            return "{" + ",".join(encode(key) + ":" + encode(item[key]) for key in sorted(item)) + "}"
        raise Invalid("unsupported manifest value")
    result = encode(value).encode("utf-8")
    if len(result) > 16 * 1024 * 1024:
        raise Invalid("canonical manifest exceeds 16 MiB")
    return result


def verify_manifest(body):
    manifest = json.loads(body, parse_float=Decimal)
    if manifest.get("raw_hash_kind") != "canonical_normalized_payload_sha256":
        raise Invalid("unsupported normalized payload contract")
    identity = manifest.get("hash")
    fingerprint = dict(manifest, hash="", manifest_id="")
    if identity != sha(dataset_canonical(fingerprint)) or manifest.get("manifest_id") != "sha256:" + str(identity):
        raise Invalid("manifest internal hash mismatch")
    cutoff = timestamp(manifest.get("cutoff"))
    seen = set()
    for record in manifest.get("records", []):
        if record.get("availability") != "known" or not record.get("availability_evidence"):
            raise Invalid("manifest includes unavailable record")
        if timestamp(record.get("available_at")) > cutoff or timestamp(record.get("observed_at")) > cutoff:
            raise Invalid("manifest record is after cutoff")
        if timestamp(record["available_at"]) > timestamp(record["observed_at"]):
            raise Invalid("manifest availability is after observation")
        if record.get("raw_hash") != sha(dataset_canonical(record.get("payload"))):
            raise Invalid("manifest payload hash mismatch")
        revision = dict(record, revision_hash="", observed_at="0001-01-01T00:00:00Z")
        if record.get("revision_hash") != sha(dataset_canonical(revision)):
            raise Invalid("manifest revision hash mismatch")
        if record["revision_hash"] in seen:
            raise Invalid("duplicate manifest revision")
        seen.add(record["revision_hash"])


def timestamp(value):
    try:
        result = datetime.fromisoformat(value.replace("Z", "+00:00"))
        if result.tzinfo is None:
            raise ValueError("timezone required")
        return result
    except (ValueError, AttributeError) as exc:
        raise Invalid("timestamp must have an explicit timezone") from exc


def finite(value, name):
    if isinstance(value, bool) or not isinstance(value, (int, float)) or not math.isfinite(value):
        raise Invalid(f"{name}: finite number required; missing is not zero")
    return float(value)


def local_file(root: Path, relative: str):
    if not isinstance(relative, str) or Path(relative).is_absolute():
        raise Invalid("input path must be relative")
    root = root.resolve()
    candidate = root / relative
    if any(part.is_symlink() for part in (candidate, *candidate.parents) if part != root and root in part.parents):
        raise Invalid("input symlinks are forbidden")
    path = candidate.resolve()
    if not path.is_relative_to(root) or not path.is_file():
        raise Invalid("input file escapes the frozen job directory")
    if path.stat().st_size > 64 * 1024 * 1024:
        raise Invalid("input artifact exceeds 64 MiB")
    return path


def load_artifact(root, descriptor, frozen_manifest=False):
    path = local_file(root, descriptor.get("path"))
    body = path.read_bytes()
    if not re.fullmatch(r"[0-9a-f]{64}", descriptor.get("sha256", "")) or sha(body) != descriptor["sha256"]:
        raise Invalid("artifact hash mismatch")
    try:
        value = json.loads(body, parse_constant=lambda value: (_ for _ in ()).throw(Invalid("nonfinite JSON")))
        if frozen_manifest:
            verify_manifest(body)
        return value
    except (ValueError, UnicodeDecodeError) as exc:
        raise Invalid("artifact is not strict JSON") from exc


def check_job(job, root):
    if job.get("schema_version") != 1 or not re.fullmatch(r"[A-Za-z0-9_-]{1,80}", job.get("run_id", "")):
        raise Invalid("invalid research job identity")
    if job.get("run_id") == "prediction-archive":
        raise Invalid("reserved research run identity")
    if job.get("kind") not in {"performance", "portfolio", "vectorbt", "rd_agent", "jev_evaluate", "prediction_archive", "prediction_evaluate"}:
        raise Invalid("unsupported research kind")
    # Credentials and personal account identities must never be admitted, even in options.
    def reject_secrets(value):
        if isinstance(value, dict):
            for key, item in value.items():
                if re.search(r"api.?key|password|secret|credential|account.?id|broker", key, re.I):
                    raise Invalid("credential/account field is forbidden")
                reject_secrets(item)
        elif isinstance(value, list):
            for item in value:
                reject_secrets(item)
    reject_secrets(job)
    descriptor = job.get("manifest", {})
    manifest = load_artifact(root, descriptor, frozen_manifest=True)
    if manifest.get("schema_version") != 1 or manifest.get("hash") != descriptor.get("hash") or manifest.get("manifest_id") != descriptor.get("id"):
        raise Invalid("frozen manifest identity mismatch")
    if manifest.get("manifest_id") != "sha256:" + str(manifest.get("hash")):
        raise Invalid("manifest is not content addressed")
    cutoff = timestamp(manifest.get("cutoff"))
    if timestamp(job.get("as_of")) > cutoff:
        raise Invalid("research as_of exceeds manifest cutoff")
    if job.get("kind") == "prediction_archive" and timestamp(job["as_of"]) != cutoff:
        raise Invalid("forecast evidence_as_of must equal its frozen manifest cutoff")
    payload = load_artifact(root, job.get("input", {}))
    reject_secrets(payload)
    if payload.get("manifest_id") != descriptor["id"] or payload.get("manifest_hash") != descriptor["hash"]:
        raise Invalid("input was exported for a different manifest")
    return manifest, payload


def environment():
    versions = {"python": platform.python_version(), "platform": platform.platform()}
    for name in ("numpy", "pandas", "quantstats", "PyPortfolioOpt", "cvxpy", "clarabel", "vectorbt", "numba"):
        try:
            versions[name] = importlib.metadata.version(name)
        except importlib.metadata.PackageNotFoundError:
            versions[name] = None
    return {"versions": versions, "hash": digest(versions)}


def require(name, import_name=None):
    import importlib
    try:
        return importlib.import_module(import_name or name)
    except (ImportError, OSError) as exc:
        raise Unavailable(f"dependency_unavailable:{name}") from exc


def dates(values, job, minimum=2):
    if not isinstance(values, list) or len(values) < minimum or len(values) > 10000:
        raise Invalid("insufficient or excessive observation count")
    if values != sorted(set(values)):
        raise Invalid("dates must be unique and ascending")
    for date in values:
        try:
            stamp = datetime.strptime(date, "%Y-%m-%d").date()
        except (TypeError, ValueError) as exc:
            raise Invalid("dates must use YYYY-MM-DD") from exc
        if stamp > timestamp(job["as_of"]).date():
            raise Invalid("future observations are forbidden")
    return values
