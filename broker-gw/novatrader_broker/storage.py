"""Transport receipts only. No order/position/cash database lives in the gateway."""

import hashlib
import json
import os
from pathlib import Path
import tempfile
import time
import uuid

from .protocol import PROTOCOL, ProtocolError, identifier


MARKER = ".novatrader-isolated.json"


def safe_path(path):
    path = Path(path).absolute()
    if str(path).startswith(("\\\\", "//")):
        raise ProtocolError("NETWORK_PATH_FORBIDDEN")
    for parent in (path, *path.parents):
        if parent.is_symlink() or (hasattr(parent, "is_junction") and parent.is_junction()):
            raise ProtocolError("LINK_PATH_FORBIDDEN")
    return path.resolve()


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=True).encode("ascii")


def atomic_json(path, value):
    path = safe_path(path)
    fd, temp = tempfile.mkstemp(prefix=".state-", dir=path.parent)
    try:
        with os.fdopen(fd, "wb") as handle:
            handle.write(canonical(value))
            handle.flush()
            os.fsync(handle.fileno())
        for attempt in range(6):
            try:
                os.replace(temp, path)
                break
            except PermissionError as error:
                # Windows readers briefly hold an existing receipt without
                # FILE_SHARE_DELETE. Only retry metadata replacement, never CSV publication.
                if getattr(error, "winerror", None) not in {5, 32, 33} or attempt == 5:
                    raise
                time.sleep(0.01 * (attempt + 1))
    finally:
        if os.path.exists(temp):
            os.unlink(temp)


def initialize(root):
    root = safe_path(root)
    if root.exists() and any(root.iterdir()):
        raise ProtocolError("INITIALIZATION_REQUIRES_EMPTY_DIRECTORY")
    root.mkdir(parents=True, exist_ok=True)
    for name in ("input", "output", "spool", "staging"):
        (root / name).mkdir()
    marker = {"protocol": PROTOCOL, "mode": "ISOLATED", "instance_id": str(uuid.uuid4()), "created_at": time.time()}
    atomic_json(root / MARKER, marker)
    return marker


class Storage:
    def __init__(self, root, account_id):
        self.root = safe_path(root)
        self.account_id = identifier(account_id, account=True)
        try:
            marker = json.loads(safe_path(self.root / MARKER).read_text("utf-8"))
        except (OSError, ValueError):
            raise ProtocolError("ISOLATED_ROOT_REQUIRED") from None
        if marker.get("protocol") != PROTOCOL or marker.get("mode") != "ISOLATED":
            raise ProtocolError("ISOLATED_ROOT_REQUIRED")
        for name in ("input", "output", "spool", "staging"):
            if not safe_path(self.root / name).is_dir():
                raise ProtocolError("INVALID_ROOT_LAYOUT")
        self.output = safe_path(self.root / "output" / self.account_id)
        self.output.mkdir(exist_ok=True)

    def _receipt_dir(self, kind, key):
        identifier(key)
        if kind not in {"order", "cancel"}:
            raise ProtocolError("INVALID_COMMAND")
        digest = hashlib.sha256(canonical([self.account_id, kind, key])).hexdigest()
        return safe_path(self.root / "spool" / digest)

    def receipt(self, kind, key):
        directory = self._receipt_dir(kind, key)
        if not directory.exists():
            return None
        try:
            value = json.loads(safe_path(directory / "receipt.json").read_text("utf-8"))
            if (not isinstance(value, dict) or value.get("protocol") != PROTOCOL or value.get("kind") != kind
                    or value.get("key") != key or not isinstance(value.get("request"), dict)
                    or value["request"].get("account_id") != self.account_id
                    or hashlib.sha256(canonical(value["request"])).hexdigest() != value.get("request_hash")
                    or value.get("state") not in {"PREPARED", "FILE_PUBLISHED", "UNKNOWN"}):
                raise ValueError("CORRUPT_RECEIPT")
            return value
        except (OSError, ValueError):
            return {"state": "UNKNOWN", "broker_ack": False, "kind": kind, "key": key,
                    "reason": "INCOMPLETE_TRANSPORT_RECEIPT", "retry_allowed": False}

    def publish(self, kind, key, request, content, *, fault=None):
        """Claim durably before visibility. Every uncertain attempt is query-only on retry."""
        digest = hashlib.sha256(canonical(request)).hexdigest()
        directory = self._receipt_dir(kind, key)
        try:
            directory.mkdir()
        except FileExistsError:
            existing = self.receipt(kind, key)
            if existing.get("request_hash") and existing["request_hash"] != digest:
                raise ProtocolError("IDENTITY_CONFLICT")
            if existing["state"] == "PREPARED":
                existing = {**existing, "state": "UNKNOWN", "reason": "RECOVER_OR_INFLIGHT_QUERY_ONLY"}
            return {**existing, "duplicate": True}
        # A process crash anywhere after mkdir permanently consumes the transport identity.
        filename = directory.name + (".order.csv" if kind == "order" else ".cancel_order.csv")
        result = {"protocol": PROTOCOL, "kind": kind, "key": key, "sid": request["sid"],
                  "request_hash": digest, "file": filename, "state": "PREPARED", "broker_ack": False,
                  "retry_allowed": False, "created_at": time.time(), "request": request}
        staged = None
        try:
            atomic_json(directory / "receipt.json", result)
            if fault:
                fault("after_claim")
            fd, staged = tempfile.mkstemp(prefix="order-", suffix=".tmp", dir=safe_path(self.root / "staging"))
            with os.fdopen(fd, "wb") as handle:
                handle.write(content)
                handle.flush()
                os.fsync(handle.fileno())
            if fault:
                fault("before_publish")
            destination = safe_path(self.root / "input" / filename)
            # A hard link publishes the complete fsynced file atomically and never overwrites.
            # Staging and input are siblings on one local filesystem. Unsupported FS fails closed.
            os.link(staged, destination)
            if fault:
                fault("after_publish")
            with open(safe_path(Path(str(destination) + ".fin")), "xb") as handle:
                handle.flush()
                os.fsync(handle.fileno())
            result["state"] = "FILE_PUBLISHED"
            result["published_at"] = time.time()
            atomic_json(directory / "receipt.json", result)
        except OSError:
            result["state"] = "UNKNOWN"
            result["reason"] = "TRANSPORT_IO_FAILURE_QUERY_ONLY"
            try:
                atomic_json(directory / "receipt.json", result)
            except OSError:
                pass  # PREPARED / absent receipt also recovers as UNKNOWN, never resubmits.
        finally:
            if staged and os.path.exists(staged):
                os.unlink(staged)
        return result

    def receipts(self):
        rows = []
        for directory in safe_path(self.root / "spool").iterdir():
            if not directory.is_dir():
                continue
            try:
                row = json.loads(safe_path(directory / "receipt.json").read_text("utf-8"))
            except (OSError, ValueError):
                rows.append({"state": "UNKNOWN", "reason": "INCOMPLETE_TRANSPORT_RECEIPT"})
                continue
            # Receipt hash includes account, but do not mix other account transport statistics.
            if not isinstance(row, dict) or not isinstance(row.get("request"), dict):
                rows.append({"state": "UNKNOWN", "reason": "INCOMPLETE_TRANSPORT_RECEIPT"})
            elif row["request"].get("account_id") == self.account_id:
                rows.append(row)
        return rows
