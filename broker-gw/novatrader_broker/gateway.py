"""Deterministic operations; a transport receipt is never a business order ACK."""

import hashlib
import time

from .protocol import CANCEL_HEADER, PROTOCOL, ProtocolError, decimal, encode_rows, identifier, integer, order, order_csv, symbol
from .reader import query, snapshot
from .storage import Storage, canonical


class Gateway:
    def __init__(self, root, account_id, *, encoding="utf-8-sig", max_age=30):
        if encoding not in {"utf-8-sig", "gb18030"} or not 1 <= max_age <= 300:
            raise ProtocolError("INVALID_READER_CONFIGURATION")
        self.storage = Storage(root, account_id)
        self.encoding, self.max_age = encoding, max_age

    def submit(self, value, *, fault=None):
        value = order(value)
        request = {**value, "account_id": self.storage.account_id}
        return self.storage.publish("order", value["sid"], request, order_csv(self.storage.account_id, value), fault=fault)

    def snapshot(self):
        return snapshot(self.storage, encoding=self.encoding, max_age=self.max_age)

    def query(self, sid):
        return query(self.storage, sid, self.snapshot())

    def cancel(self, value, *, fault=None):
        if not isinstance(value, dict) or set(value) != {"request_id", "sid"}:
            raise ProtocolError("INVALID_CANCEL_FIELDS")
        request_id, sid = identifier(value["request_id"]), identifier(value["sid"])
        request = {"sid": sid, "account_id": self.storage.account_id}
        existing = self.storage.receipt("cancel", request_id)
        if not existing:
            evidence = self.query(sid)
            if not evidence["evidence_ready"] or evidence["state"] not in {"PENDING", "ACKNOWLEDGED", "PARTIAL"}:
                raise ProtocolError("CANCEL_REQUIRES_CURRENT_ORDER_EVIDENCE")
        result = self.storage.publish("cancel", request_id, request, encode_rows(CANCEL_HEADER, [[sid, ""]]), fault=fault)
        return {**result, "cancel_confirmed": False}

    def health(self):
        evidence = self.snapshot()
        receipts = self.storage.receipts()
        pending = sum(1 for row in receipts if row.get("kind") == "order" and query(self.storage, row["sid"], evidence)["state"] == "UNKNOWN")
        incomplete = sum(1 for row in receipts if row.get("state") in {"PREPARED", "UNKNOWN"})
        return {"protocol": PROTOCOL, "mode": "ISOLATED", "account_id": self.storage.account_id,
                "reports_ready": evidence["reports_ready"], "reasons": evidence["reasons"],
                "file_published": sum(1 for row in receipts if row.get("state") == "FILE_PUBLISHED"),
                "transport_unknown": incomplete, "order_unknown": pending,
                "terminal_login_verified": False, "can_open_live": False, "observed_at": time.time()}

    def reconcile(self, expected):
        required = {"account_id", "balance", "available", "frozen", "positions", "orders"}
        if not isinstance(expected, dict) or set(expected) != required or expected["account_id"] != self.storage.account_id:
            raise ProtocolError("INVALID_RECONCILIATION_INPUT")
        if not isinstance(expected["positions"], list) or not isinstance(expected["orders"], list):
            raise ProtocolError("INVALID_RECONCILIATION_INPUT")
        evidence = self.snapshot()
        differences = []
        reports = evidence["reports"]
        if not evidence["reports_ready"]:
            differences.append({"code": "INCOMPLETE_OR_STALE_REPORTS"})
        cash = reports["cash.csv"]["records"]
        for field in ("balance", "available", "frozen"):
            expected_value = decimal(expected[field], nonnegative=True)
            if cash and abs(decimal(cash[0][field]) - expected_value) > decimal("0.01"):
                differences.append({"code": "CASH_MISMATCH", "field": field})
        local_positions = {}
        for row in expected["positions"]:
            if not isinstance(row, dict) or set(row) != {"symbol", "volume", "available_now"}:
                raise ProtocolError("INVALID_EXPECTED_POSITION")
            key = symbol(row["symbol"])
            if key in local_positions:
                raise ProtocolError("DUPLICATE_EXPECTED_POSITION")
            quantity, available = integer(row["volume"]), integer(row["available_now"])
            if available > quantity:
                raise ProtocolError("INVALID_EXPECTED_POSITION")
            local_positions[key] = (quantity, available)
        remote_positions = {row["symbol"]: (row["volume"], row["available_now"]) for row in reports["position.csv"]["records"]}
        for key in sorted(local_positions.keys() | remote_positions.keys()):
            if local_positions.get(key, (0, 0)) != remote_positions.get(key, (0, 0)):
                differences.append({"code": "POSITION_MISMATCH", "symbol": key})
        local_orders = {}
        for row in expected["orders"]:
            if not isinstance(row, dict) or set(row) != {"sid", "state", "filled_volume"}:
                raise ProtocolError("INVALID_EXPECTED_ORDER")
            key = identifier(row["sid"])
            if key in local_orders:
                raise ProtocolError("DUPLICATE_EXPECTED_ORDER")
            local_orders[key] = (row["state"], integer(row["filled_volume"]))
        remote_ids = {row["sid"] for name in ("order_status.csv", "order_status_change.csv") for row in reports[name]["records"]}
        transport_ids = {row["sid"] for row in self.storage.receipts() if row.get("kind") == "order"}
        for sid in sorted(local_orders.keys() | remote_ids | transport_ids):
            actual = query(self.storage, sid, evidence)
            if actual["state"] == "UNKNOWN" or not actual["evidence_ready"]:
                differences.append({"code": "ORDER_UNKNOWN", "sid": sid})
                continue
            if local_orders.get(sid) != (actual["state"], actual["order"]["filled_volume"]):
                differences.append({"code": "ORDER_MISMATCH", "sid": sid})
            fills = [row for row in actual["executions"] if row["kind"] == "FILL"]
            if sum(row["volume"] for row in fills) != actual["order"]["filled_volume"]:
                differences.append({"code": "FILL_COVERAGE_MISMATCH", "sid": sid})
            if abs(sum((decimal(row["amount"]) for row in fills), decimal("0")) - decimal(actual["order"]["filled_amount"])) > decimal("0.01"):
                differences.append({"code": "FILL_AMOUNT_MISMATCH", "sid": sid})
        return {"protocol": PROTOCOL, "mode": "ISOLATED", "account_id": self.storage.account_id,
                "state": "ISOLATED_MATCHED" if not differences else "BLOCKED", "differences": differences,
                "observed_at": time.time(), "expected_hash": hashlib.sha256(canonical(expected)).hexdigest(),
                "report_hashes": {name: row.get("content_hash") for name, row in reports.items()},
                "can_open_live": False, "cross_file_atomic": False}
