"""Protocol fixture simulator. It models no fees, exchange rules or real execution."""

import csv
from datetime import datetime, timedelta
from decimal import Decimal
import io
import os
import tempfile

from .protocol import ALIASES, CANCEL_HEADER, HEADERS, ORDER_HEADER, ProtocolError, decimal, encode_rows, integer, order
from .storage import Storage, safe_path


class Simulator:
    def __init__(self, root, account_id):
        self.storage = Storage(root, account_id)
        self.orders, self.positions = {}, {}
        self.executions, self.changes, self.seen = [], [], set()
        self.cash = Decimal("100000.00")
        self.sequence = 0
        self.write_all()

    def _timestamp(self):
        self.sequence += 1
        # Stable, monotonically increasing local source timestamps even in a fast test.
        return (datetime(2026, 10, 16, 9, 30) + timedelta(microseconds=self.sequence)).isoformat(sep=" ", timespec="microseconds")

    def _row(self, name):
        stamp = self._timestamp()
        row = {ALIASES[key]: "0" for key in HEADERS[name]}
        row["account_id"] = self.storage.account_id
        for key in ("created_at", "updated_at", "sent_at", "recv_at"):
            if key in row:
                row[key] = stamp
        for key in ("scan_name", "cl_ord_id", "order_id", "exec_id", "ord_rej_reason_detail"):
            if key in row:
                row[key] = ""
        return row

    def _write(self, name, rows):
        data = encode_rows(HEADERS[name], [[row[ALIASES[key]] for key in HEADERS[name]] for row in rows])
        fd, temporary = tempfile.mkstemp(dir=self.storage.output, suffix=".tmp")
        try:
            with os.fdopen(fd, "wb") as handle:
                handle.write(data)
                handle.flush()
                os.fsync(handle.fileno())
            os.replace(temporary, safe_path(self.storage.output / name))
        finally:
            if os.path.exists(temporary):
                os.unlink(temporary)

    def write_all(self):
        frozen = sum((decimal(row["price"]) * (row["volume"] - row["filled_volume"]) for row in self.orders.values()
                      if row["status"] in {"1", "2", "10"} and row["order_business"] == "1"), Decimal(0))
        market_value = sum((decimal(row["price"]) * row["volume"] for row in self.positions.values()), Decimal(0))
        cash = self._row("cash.csv")
        cash.update(balance=str(self.cash), available=str(self.cash - frozen), frozen=str(frozen), order_frozen=str(frozen),
                    nav=str(self.cash + market_value), market_value=str(market_value))
        self._write("cash.csv", [cash])
        self._write("position.csv", self.positions.values())
        self._write("order_status.csv", self.orders.values())
        self._write("execution_report.csv", self.executions)
        self._write("order_status_change.csv", self.changes)

    def scan(self):
        # Official scanner reacts to CSV visibility; .fin is deliberately not tested.
        for path in sorted(safe_path(self.storage.root / "input").glob("*.csv")):
            if path.name in self.seen:
                continue
            with open(safe_path(path), encoding="utf-8", newline="") as handle:
                reader = csv.DictReader(handle, strict=True)
                rows = list(reader)
            if path.name.endswith(".cancel_order.csv"):
                if tuple(reader.fieldnames or []) != CANCEL_HEADER or len(rows) != 1:
                    raise ProtocolError("SIMULATOR_BAD_CANCEL_FILE")
                self.cancel(rows[0]["sid"])
            else:
                if tuple(reader.fieldnames or []) != ORDER_HEADER or len(rows) != 1:
                    raise ProtocolError("SIMULATOR_BAD_ORDER_FILE")
                value = rows[0]
                if value["account_id"] != self.storage.account_id or value["order_type"] != "1":
                    continue
                request = order({"sid": value["sid"], "symbol": value["symbol"], "side": "buy" if value["order_business(order_biz)"] == "1" else "sell",
                                 "price": value["price"], "volume": value["volume"]})
                if request["sid"] not in self.orders:
                    row = self._row("order_status.csv")
                    row.update(sid=request["sid"], symbol=request["symbol"], order_type="1", order_business=value["order_business(order_biz)"],
                               status="1", price=request["price"], volume=request["volume"], filled_volume=0,
                               order_id="SIM-" + request["sid"], cl_ord_id=request["sid"], scan_name="isolated-simulator")
                    self.orders[request["sid"]] = row
                    self.changes.append(dict(row))
            self.seen.add(path.name)
        self.write_all()

    def fill(self, sid, volume, exec_id):
        row = self.orders[sid]
        volume = integer(volume, positive=True)
        if row["filled_volume"] + volume > row["volume"]:
            raise ProtocolError("SIMULATOR_OVERFILL")
        if any(value["exec_id"] == exec_id for value in self.executions):
            raise ProtocolError("SIMULATOR_DUPLICATE_FILL_ID")
        price = decimal(row["price"])
        row["filled_volume"] += volume
        row["status"] = "3" if row["filled_volume"] == row["volume"] else ("5" if row["status"] == "5" else "2")
        row.update(filled_vwap=str(price), filled_amount=str(price * row["filled_volume"]), updated_at=self._timestamp())
        self.changes.append(dict(row))
        execution = self._row("execution_report.csv")
        execution.update(sid=sid, symbol=row["symbol"], order_business=row["order_business"], exec_id=exec_id, exec_type="15",
                         volume=volume, price=str(price), amount=str(price * volume), order_id=row["order_id"], cl_ord_id=sid)
        self.executions.append(execution)
        position = self.positions.get(row["symbol"]) or self._row("position.csv")
        if row["symbol"] not in self.positions:
            position.update(symbol=row["symbol"], side="1", volume=0, volume_today=0, available_now=0, price=str(price))
        sign = 1 if row["order_business"] == "1" else -1
        if sign < 0 and position["available_now"] < volume:
            raise ProtocolError("SIMULATOR_INSUFFICIENT_AVAILABLE")
        self.cash -= sign * price * volume
        position["volume"] += sign * volume
        if sign > 0:
            position["volume_today"] += volume
        else:
            position["available_now"] -= volume
        position.update(vwap=str(price), vwap_diluted=str(price), market_value=str(price * position["volume"]), cost=str(price * position["volume"]))
        self.positions[row["symbol"]] = position
        self.write_all()

    def cancel(self, sid):
        row = self.orders.get(sid)
        if row is None:
            raise ProtocolError("SIMULATOR_UNKNOWN_ORDER")
        if row["status"] in {"3", "8", "12"}:
            execution = self._row("execution_report.csv")
            execution.update(sid=sid, symbol=row["symbol"], order_business=row["order_business"], exec_type="19", volume=0)
            self.executions.append(execution)
        else:
            row.update(status="5", updated_at=self._timestamp())
            self.changes.append(dict(row))
        self.write_all()

    def expected(self):
        frozen = sum((decimal(row["price"]) * (row["volume"] - row["filled_volume"]) for row in self.orders.values()
                      if row["status"] in {"1", "2", "10"} and row["order_business"] == "1"), Decimal(0))
        states = {"1": "ACKNOWLEDGED", "2": "PARTIAL", "3": "FILLED", "5": "CANCELLED", "8": "REJECTED", "10": "PENDING", "12": "EXPIRED"}
        return {"account_id": self.storage.account_id, "balance": str(self.cash), "available": str(self.cash - frozen), "frozen": str(frozen),
                "positions": [{key: row[key] for key in ("symbol", "volume", "available_now")} for row in self.positions.values()],
                "orders": [{"sid": sid, "state": states[row["status"]], "filled_volume": row["filled_volume"]} for sid, row in self.orders.items()]}
