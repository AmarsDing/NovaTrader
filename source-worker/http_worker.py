"""Optional loopback source service. Fixed adapters, complete records, no database access."""
from __future__ import annotations

import argparse
import hashlib
import json
import socket
import threading
from datetime import datetime, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

from nt_sources.adapters import SourceError, openbb_macro, tdx_history
from worker import freeze

MAX_REQUEST = 4096
MAX_RESPONSE = 8 * 1024 * 1024
MAX_RECORDS = 10000
RECORDED_DATE = "2025-01-06"
RECORDED_SHA = "dfca352934d3681cb9e8e5644f0492d02a562d0a13f751acbc1d85febdf970f5"


def encode(value) -> bytes:
    return json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":"), allow_nan=False).encode()


def collect(job: dict, evidence: Path, *, recorded_only: bool = False) -> dict:
    allowed = {"schema_version", "operation", "date", "series", "start", "end"}
    if not isinstance(job, dict) or set(job) - allowed or type(job.get("schema_version")) is not int or job["schema_version"] != 1:
        raise SourceError("invalid_request", "unsupported source records contract")
    operation = job.get("operation")
    if operation == "tdx_history":
        if set(job) != {"schema_version", "operation", "date"}:
            raise SourceError("invalid_request", "daily archive requires only a date")
        if recorded_only:
            if job["date"] != RECORDED_DATE:
                raise SourceError("unavailable", "recorded mode has no reviewed archive for this date")
            path = Path(__file__).parent / "evidence" / ("tdx-" + RECORDED_DATE) / (RECORDED_SHA + ".raw")
            raw = path.read_bytes()
            if len(raw) > MAX_RESPONSE or hashlib.sha256(raw).hexdigest() != RECORDED_SHA:
                raise SourceError("artifact_conflict", "reviewed archive integrity mismatch")
            rows, meta, raw = tdx_history(job["date"], fetch=lambda _: (raw, {}))
        else:
            rows, meta, raw = tdx_history(job["date"])
        domain = "daily_bar"
    elif operation == "openbb_macro":
        if set(job) != {"schema_version", "operation", "series", "start", "end"}:
            raise SourceError("invalid_request", "macro requires a reviewed series and date range")
        rows, meta, raw = openbb_macro(job["series"], job["start"], job["end"])
        domain = "macro"
    else:
        raise SourceError("invalid_request", "operation outside daily archive and macro whitelist")
    if not rows or len(rows) > MAX_RECORDS:
        raise SourceError("format_changed", "record count outside 1..10000")
    raw_hash = hashlib.sha256(raw).hexdigest()
    if len(raw) > MAX_RESPONSE:
        raise SourceError("format_changed", "provider evidence exceeds 8MiB")
    records_hash = hashlib.sha256(encode(rows)).hexdigest()
    mode = "recorded_provider_bytes" if recorded_only and operation == "tdx_history" else "provider_fetch"
    meta = dict(meta, acquisition_mode=mode)
    # Evidence remains content addressed. Its 30 samples are independent of this full response.
    freeze(evidence, rows, meta, raw)
    result = {"schema_version": 1, "status": "succeeded", "domain": domain,
              "provider": meta["provider"], "provider_raw_sha256": raw_hash,
              "provider_raw_kind": meta["raw_kind"], "acquisition_mode": mode,
              "records_sha256": records_hash, "record_count": len(rows), "records": rows,
              "observed_at": datetime.now(timezone.utc).isoformat(), "available_at": None,
              "training_ready": False, "coverage": meta.get("coverage", {"coverage_ratio": None}),
              "metadata": meta}
    if len(encode(result)) > MAX_RESPONSE:
        raise SourceError("format_changed", "full response exceeds 8MiB")
    return result


class SourceServer(ThreadingHTTPServer):
    daemon_threads = True

    def __init__(self, address, *, enabled=False, evidence: Path, recorded_only=False):
        if address[0] != "127.0.0.1":
            raise ValueError("source worker only accepts loopback binding")
        self.enabled, self.evidence, self.recorded_only = enabled, evidence, recorded_only
        self.slots = threading.BoundedSemaphore(2)
        super().__init__(address, Handler)

    def process_request(self, request, client_address):
        request.settimeout(5)
        if not self.slots.acquire(blocking=False):
            try:
                request.sendall(b"HTTP/1.1 503 Service Unavailable\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
            except OSError:
                pass
            self.shutdown_request(request)
            return
        try:
            super().process_request(request, client_address)
        except BaseException:
            self.slots.release()
            raise

    def process_request_thread(self, request, client_address):
        try:
            super().process_request_thread(request, client_address)
        finally:
            self.slots.release()


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass  # Do not log provider URLs, requests, paths or secret-containing exception bodies.

    def reply(self, status, value):
        body = encode(value)
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Connection", "close")
        self.end_headers()
        self.wfile.write(body)
        self.close_connection = True

    def do_GET(self):
        if self.path != "/healthz":
            self.reply(404, {"status": "unsupported"})
            return
        self.reply(200, {"service": "source-worker", "enabled": self.server.enabled,
                         "providers": "unprobed", "coverage_known": False,
                         "recorded_only": self.server.recorded_only})

    def do_POST(self):
        if self.path != "/v1/source/collect":
            self.reply(404, {"status": "unsupported"})
            return
        if not self.server.enabled:
            self.reply(503, {"schema_version": 1, "status": "disabled", "training_ready": False})
            return
        try:
            if self.headers.get("Transfer-Encoding") is not None:
                raise SourceError("invalid_request", "chunked source requests are unsupported")
            length = int(self.headers.get("Content-Length", "0"))
            if length <= 0 or length > MAX_REQUEST:
                raise SourceError("invalid_request", "request must be 1..4096 bytes")
            body = self.rfile.read(length)
            if len(body) != length:
                raise SourceError("invalid_request", "truncated request")
            result = collect(json.loads(body), self.server.evidence, recorded_only=self.server.recorded_only)
            self.reply(200, result)
        except SourceError as exc:
            self.reply(400 if exc.code == "invalid_request" else 503,
                       {"schema_version": 1, "status": exc.code, "reason": str(exc), "training_ready": False})
        except (ValueError, TypeError, OSError, socket.timeout):
            self.reply(503, {"schema_version": 1, "status": "unavailable", "reason": "source request or evidence I/O failed", "training_ready": False})


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--port", type=int, default=18082)
    parser.add_argument("--enabled", action="store_true")
    parser.add_argument("--recorded-only", action="store_true")
    parser.add_argument("--evidence-dir", type=Path, required=True)
    args = parser.parse_args()
    if args.port != 0 and not 1024 <= args.port <= 65535:
        parser.error("port must be 0 or 1024..65535")
    with SourceServer(("127.0.0.1", args.port), enabled=args.enabled,
                      evidence=args.evidence_dir, recorded_only=args.recorded_only) as server:
        print(json.dumps({"schema_version": 1, "loopback_port": server.server_address[1],
                          "enabled": server.enabled, "recorded_only": server.recorded_only}), flush=True)
        server.serve_forever(poll_interval=0.2)


if __name__ == "__main__":
    main()
