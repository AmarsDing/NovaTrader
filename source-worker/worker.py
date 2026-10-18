"""One bounded, isolated, read-only source job. JSON stdin -> result JSON + immutable evidence."""
from __future__ import annotations

import argparse
import hashlib
import json
import os
import sys
import tempfile
from datetime import datetime, timezone
from pathlib import Path

from nt_sources.adapters import SourceError, openbb_macro, tdx_history


def freeze(directory: Path, rows: list[dict], metadata: dict, raw: bytes) -> dict:
    """Content-address evidence; raw bytes and normalized samples are both retained."""
    raw_hash = hashlib.sha256(raw).hexdigest()
    # For the POC take 10 rows per market when possible. This is a sample, not full-market coverage.
    if metadata["dataset"] == "unadjusted_a_share_daily_bar":
        samples = [r for market in ("SH", "SZ", "BJ") for r in [x for x in rows if x["symbol"].endswith("." + market)][:10]]
    else:
        samples = rows[:30]
    artifact = {"schema_version": 1, "purpose": "source_poc_only", "metadata": metadata,
                "raw_sha256": raw_hash, "row_count": len(rows), "sample_count": len(samples),
                "samples": samples, "training_ready": False}
    payload = json.dumps(artifact, ensure_ascii=False, sort_keys=True, separators=(",", ":"), allow_nan=False).encode()
    artifact_hash = hashlib.sha256(payload).hexdigest()
    directory.mkdir(parents=True, exist_ok=True)
    for name, data in ((raw_hash + ".raw", raw), (artifact_hash + ".json", payload)):
        path = directory / name
        temporary = None
        try:
            with tempfile.NamedTemporaryFile(dir=directory, prefix=".source-", suffix=".tmp", delete=False) as f:
                temporary = Path(f.name)
                f.write(data)
                f.flush()
                os.fsync(f.fileno())
            os.link(temporary, path)  # Atomically publish a complete file, without overwriting an existing artifact.
        except FileExistsError:
            if path.read_bytes() != data:
                raise SourceError("artifact_conflict", "content-addressed artifact differs")
        finally:
            if temporary is not None:
                temporary.unlink(missing_ok=True)
    return {"status": "succeeded", "artifact_hash": artifact_hash, "artifact_file": artifact_hash + ".json",
            "raw_file": raw_hash + ".raw", "row_count": len(rows), "frozen_sample_count": len(samples),
            "training_ready": False, "provider": metadata["provider"]}


def run(job: dict, directory: Path) -> dict:
    allowed = {"schema_version", "enabled", "operation", "date", "series", "start", "end"}
    if not isinstance(job, dict) or set(job) - allowed or job.get("schema_version") != 1:
        raise SourceError("invalid_request", "unsupported source job contract or fields")
    if job.get("enabled") is not True:
        return {"status": "disabled", "training_ready": False}
    operation = job.get("operation")
    if operation == "tdx_history":
        rows, metadata, raw = tdx_history(job.get("date"))
    elif operation == "openbb_macro":
        rows, metadata, raw = openbb_macro(job.get("series"), job.get("start"), job.get("end"))
    else:
        raise SourceError("invalid_request", "operation outside reviewed whitelist")
    try:
        return freeze(directory, rows, metadata, raw)
    except OSError as exc:
        raise SourceError("artifact_error", "durable evidence write failed") from exc


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--output-dir", type=Path, required=True)
    args = parser.parse_args()
    try:
        raw = sys.stdin.buffer.read(65537)
        if len(raw) > 65536:
            raise SourceError("invalid_request", "job exceeds 64KiB")
        result = run(json.loads(raw), args.output_dir)
    except SourceError as exc:
        result = {"status": exc.code, "reason": str(exc), "training_ready": False}
    except (ValueError, TypeError, OSError):
        result = {"status": "invalid_request", "reason": "invalid JSON or artifact I/O failure", "training_ready": False}
    result["completed_at"] = datetime.now(timezone.utc).isoformat()
    print(json.dumps(result, ensure_ascii=False, allow_nan=False))
    return 0 if result["status"] in {"succeeded", "disabled"} else 2


if __name__ == "__main__":
    raise SystemExit(main())
