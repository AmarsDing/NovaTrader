"""Read terminal evidence without inventing missing values or acknowledging file writes."""

import csv
from datetime import datetime
import hashlib
import io
import os
import time

from .protocol import ALIASES, EXECUTION, HEADERS, STATUS, ProtocolError, decimal, identifier, integer, symbol
from .storage import canonical, safe_path


MAX_BYTES = 8 * 1024 * 1024
MAX_ROWS = 100_000


def terminal_time(value):
    # The local terminal CSV has no timezone field. Manifest pins Asia/Shanghai;
    # this is a local ordering key, not a guessed UTC source timestamp.
    try:
        result = datetime.fromisoformat(value)
    except (ValueError, TypeError):
        raise ProtocolError("INVALID_TERMINAL_TIME") from None
    if result.tzinfo is not None or result.year < 2000:
        raise ProtocolError("INVALID_TERMINAL_TIME")
    return result


def normalize(name, row, account_id):
    if row.get("account_id") != account_id:
        raise ProtocolError("ACCOUNT_MISMATCH")
    for field in ("created_at", "updated_at", "recv_at", "sent_at"):
        if field in row:
            terminal_time(row[field])
    if "symbol" in row:
        row["symbol"] = symbol(row["symbol"])
    if "sid" in row:
        identifier(row["sid"])
    if name == "cash.csv":
        for field in ("market_value", "nav", "pnl", "fpnl", "frozen", "available", "balance", "order_frozen"):
            row[field] = str(decimal(row[field], nonnegative=field not in {"pnl", "fpnl", "nav"}))
    elif name == "position.csv":
        for field in ("volume", "volume_today", "available_now"):
            row[field] = integer(row[field])
        if row["available_now"] > row["volume"] or row["volume_today"] > row["volume"] or row["side"] != "1":
            raise ProtocolError("INVALID_POSITION")
        for field in ("vwap", "vwap_diluted", "market_value", "price", "fpnl", "cost"):
            row[field] = str(decimal(row[field]))
    elif name in {"order_status.csv", "order_status_change.csv"}:
        if row["status"] not in STATUS or row["order_business"] not in {"1", "2"} or row["order_type"] != "1":
            raise ProtocolError("UNVERIFIED_ORDER_ENUM")
        row["state"] = STATUS[row["status"]]
        row["volume"] = integer(row["volume"], positive=True)
        row["filled_volume"] = integer(row["filled_volume"])
        if row["filled_volume"] > row["volume"] or (row["state"] == "FILLED" and row["filled_volume"] != row["volume"]):
            raise ProtocolError("INVALID_FILLED_QUANTITY")
        if row["state"] == "PARTIAL" and not 0 < row["filled_volume"] < row["volume"]:
            raise ProtocolError("INVALID_FILLED_QUANTITY")
        for field in ("price", "filled_vwap", "filled_amount"):
            row[field] = str(decimal(row[field], nonnegative=True))
        if decimal(row["price"]) <= 0 or (row["filled_volume"] > 0 and (decimal(row["filled_vwap"]) <= 0 or decimal(row["filled_amount"]) <= 0)):
            raise ProtocolError("INVALID_FILLED_AMOUNT")
    elif name == "execution_report.csv":
        if row["exec_type"] not in EXECUTION or row["order_business"] not in {"1", "2"}:
            raise ProtocolError("UNVERIFIED_EXECUTION_ENUM")
        row["kind"] = EXECUTION[row["exec_type"]]
        # Cancel rejection can legitimately carry no fill id or quantity.
        if row["kind"] == "FILL":
            identifier(row["exec_id"])
        row["volume"] = integer(row["volume"], positive=row["kind"] == "FILL")
        row["price"] = str(decimal(row["price"], nonnegative=True))
        row["amount"] = str(decimal(row["amount"], nonnegative=True))
        if row["kind"] == "FILL" and (decimal(row["price"]) <= 0 or abs(decimal(row["amount"]) - decimal(row["price"]) * row["volume"]) > decimal("0.01")):
            raise ProtocolError("INVALID_EXECUTION_AMOUNT")
    # recv_at is transport observation, not a new business version. A repeated
    # execution with a later receipt time must not credit the account twice.
    version = {key: value for key, value in row.items() if key != "recv_at"}
    row["record_version"] = hashlib.sha256(canonical(version)).hexdigest()
    return row


def stable_read(path):
    path = safe_path(path)
    with open(path, "rb") as handle:
        before = path.stat()
        if before.st_size > MAX_BYTES:
            raise ProtocolError("REPORT_LIMIT_REQUIRES_ROTATION")
        content = handle.read(MAX_BYTES + 1)
        after = path.stat()
        opened = os.fstat(handle.fileno())
    fingerprint = lambda value: (value.st_ino, value.st_size, value.st_mtime_ns)
    if fingerprint(before) != fingerprint(after) or fingerprint(after) != fingerprint(opened) or len(content) != after.st_size:
        raise ProtocolError("REPORT_CHANGED_DURING_READ")
    return content, after


def read_report(storage, name, *, encoding="utf-8-sig", max_age=30, clock=time.time):
    if name not in HEADERS or encoding not in {"utf-8-sig", "gb18030"}:
        raise ProtocolError("UNSUPPORTED_REPORT")
    observed_at = clock()
    try:
        content, stat = stable_read(storage.output / name)
        append = name in {"execution_report.csv", "order_status_change.csv"}
        complete = content.rfind(b"\n") + 1
        partial_bytes = len(content) - complete
        if not content or (partial_bytes and not append):
            raise ProtocolError("PARTIAL_REPORT")
        if complete == 0:
            raise ProtocolError("PARTIAL_HEADER")
        rows = list(csv.reader(io.StringIO(content[:complete].decode(encoding), newline=""), strict=True))
        if not rows or len(rows) > MAX_ROWS + 1:
            raise ProtocolError("REPORT_ROW_LIMIT")
        header = [ALIASES.get(field, field) for field in rows[0]]
        expected = {ALIASES[field] for field in HEADERS[name]}
        if len(set(header)) != len(header) or set(header) != expected:
            raise ProtocolError("REPORT_HEADER_MISMATCH")
        parsed, dedup = [], {}
        for values in rows[1:]:
            if len(values) != len(header):
                raise ProtocolError("REPORT_ROW_SHAPE")
            row = normalize(name, dict(zip(header, values)), storage.account_id)
            if name == "execution_report.csv":
                key = (row["sid"], row["exec_id"]) if row["kind"] == "FILL" else (row["sid"], row["record_version"])
            elif name in {"order_status.csv", "order_status_change.csv"}:
                key = (row["sid"], row["updated_at"]) if append else row["sid"]
            elif name == "position.csv":
                key = row["symbol"]
            else:
                key = row["account_id"]
            if key in dedup:
                if dedup[key] != row["record_version"]:
                    raise ProtocolError("CONFLICTING_REPORT_IDENTITY")
                continue
            dedup[key] = row["record_version"]
            parsed.append(row)
        if name == "cash.csv" and len(parsed) != 1:
            raise ProtocolError("CASH_ROW_REQUIRED")
        age = observed_at - stat.st_mtime
        # Append streams can remain unchanged when no trades occur. Snapshot freshness
        # is required; unchanged streams alone do not prove terminal connectivity.
        fresh = -5 <= age <= max_age if not append else None
        return {"available": True, "name": name, "records": parsed, "content_hash": hashlib.sha256(content[:complete]).hexdigest(),
                "complete_bytes": complete, "partial_bytes": partial_bytes, "file_mtime": stat.st_mtime,
                "observed_at": observed_at, "fresh": fresh, "age_seconds": round(age, 3), "terminal_timezone": "Asia/Shanghai"}
    except ProtocolError as error:
        reason = str(error)
    except (OSError, UnicodeError, csv.Error, ValueError):
        reason = "REPORT_UNREADABLE_OR_MALFORMED"
    return {"available": False, "name": name, "reason": reason, "observed_at": observed_at, "records": [], "fresh": False}


def snapshot(storage, *, encoding="utf-8-sig", max_age=30):
    reports = {name: read_report(storage, name, encoding=encoding, max_age=max_age) for name in HEADERS}
    reasons = [name + ":" + (row.get("reason") or "STALE") for name, row in reports.items()
               if not row["available"] or row["fresh"] is False or row.get("partial_bytes", 0)]
    return {"protocol": "eastmoney-file-csv-isolated-v1", "mode": "ISOLATED", "account_id": storage.account_id,
            "observed_at": time.time(), "reports": reports, "reports_ready": not reasons, "reasons": reasons,
            "terminal_login_verified": False, "cross_file_atomic": False, "can_open_live": False}


def query(storage, sid, evidence=None):
    sid = identifier(sid)
    evidence = evidence or snapshot(storage)
    reports = evidence["reports"]
    candidates = [row for name in ("order_status.csv", "order_status_change.csv")
                  for row in reports[name]["records"] if row["sid"] == sid]
    fills = [row for row in reports["execution_report.csv"]["records"] if row["sid"] == sid]
    transport = storage.receipt("order", sid)
    result = {"protocol": evidence["protocol"], "account_id": storage.account_id, "sid": sid, "state": "UNKNOWN",
              "broker_ack": False, "transport": transport, "executions": fills, "retry_allowed": False,
              "evidence_ready": evidence["reports_ready"], "reason": "NO_TERMINAL_ORDER_EVIDENCE", "can_open_live": False}
    if candidates:
        candidates.sort(key=lambda row: terminal_time(row["updated_at"]))
        latest = candidates[-1]
        identity = {(row["symbol"], row["order_business"], row["volume"], decimal(row["price"])) for row in candidates}
        conflict = len(identity) != 1 or latest["filled_volume"] < max(row["filled_volume"] for row in candidates)
        conflict = conflict or any(row["updated_at"] == latest["updated_at"] and row["record_version"] != latest["record_version"] for row in candidates)
        requested = (transport or {}).get("request")
        if requested:
            conflict = conflict or (latest["symbol"], latest["order_business"], latest["volume"], decimal(latest["price"])) != (
                requested["symbol"], "1" if requested["side"] == "buy" else "2", requested["volume"], decimal(requested["price"]))
        conflict = conflict or any(row["symbol"] != latest["symbol"] or row["order_business"] != latest["order_business"]
                                   or (row["order_id"] and latest["order_id"] and row["order_id"] != latest["order_id"])
                                   for row in fills)
        if conflict:
            result["reason"] = "CONFLICTING_ORDER_EVIDENCE"
            result["evidence_ready"] = False
        else:
            result.update(state=latest["state"], broker_ack=latest["state"] != "PENDING", order=latest,
                          reason="TERMINAL_REPORT_EVIDENCE", status_version=latest["record_version"])
    return result
