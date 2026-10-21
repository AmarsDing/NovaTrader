import concurrent.futures
import copy
import errno
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time
import unittest
import unittest.mock

from novatrader_broker.gateway import Gateway
from novatrader_broker.protocol import HEADERS, ProtocolError, order
from novatrader_broker.reader import read_report
from novatrader_broker.simulator import Simulator
from novatrader_broker import storage
from novatrader_broker.storage import initialize


class Crash(BaseException):
    pass


class GatewayTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="novatrader-broker-test-")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        initialize(self.root)
        self.gateway = Gateway(self.root, "TEST_ACCOUNT")
        self.order = {"sid": "test-1", "symbol": "600519.SH", "side": "buy", "price": "10.50", "volume": 200}

    def simulator(self):
        return Simulator(self.root, "TEST_ACCOUNT")

    def test_full_partial_cancel_reconcile_and_late_fill(self):
        simulator = self.simulator()
        receipt = self.gateway.submit(self.order)
        self.assertEqual(receipt["state"], "FILE_PUBLISHED")
        self.assertFalse(receipt["broker_ack"])
        self.assertEqual(self.gateway.query("test-1")["state"], "UNKNOWN")
        self.assertEqual(self.gateway.reconcile(simulator.expected())["state"], "BLOCKED")
        simulator.scan()
        self.assertEqual(self.gateway.query("test-1")["state"], "ACKNOWLEDGED")
        simulator.fill("test-1", 100, "fill-1")
        partial = self.gateway.query("test-1")
        self.assertEqual(partial["state"], "PARTIAL")
        self.assertEqual(partial["order"]["filled_volume"], 100)
        cancel = self.gateway.cancel({"request_id": "cancel-1", "sid": "test-1"})
        self.assertFalse(cancel["cancel_confirmed"])
        self.assertEqual(self.gateway.query("test-1")["state"], "PARTIAL")
        simulator.scan()
        cancelled = self.gateway.query("test-1")
        self.assertEqual(cancelled["state"], "CANCELLED")
        self.assertNotEqual(cancelled["status_version"], partial["status_version"])
        self.assertEqual(self.gateway.reconcile(simulator.expected())["state"], "ISOLATED_MATCHED")
        simulator.fill("test-1", 100, "late-fill-2")
        self.assertEqual(self.gateway.query("test-1")["state"], "FILLED")
        self.assertEqual(self.gateway.reconcile(simulator.expected())["state"], "ISOLATED_MATCHED")
        self.assertFalse(self.gateway.health()["can_open_live"])

    def test_duplicate_identity_survives_restart_and_terminal_cleanup(self):
        receipt = self.gateway.submit(self.order)
        csv = self.root / "input" / receipt["file"]
        csv.unlink()
        Path(str(csv) + ".fin").unlink()
        restarted = Gateway(self.root, "TEST_ACCOUNT")
        repeated = restarted.submit(self.order)
        self.assertTrue(repeated["duplicate"])
        self.assertFalse(csv.exists())
        with self.assertRaisesRegex(ProtocolError, "IDENTITY_CONFLICT"):
            restarted.submit({**self.order, "volume": 300})
        self.assertFalse(csv.exists())

    def test_concurrent_repeated_sid_publishes_once(self):
        with concurrent.futures.ThreadPoolExecutor(max_workers=12) as pool:
            replies = list(pool.map(lambda _: self.gateway.submit(self.order), range(32)))
        self.assertEqual(sum(1 for row in replies if not row.get("duplicate")), 1)
        self.assertEqual(len(list((self.root / "input").glob("*.order.csv"))), 1)
        self.assertEqual(self.gateway.submit(self.order)["state"], "FILE_PUBLISHED")

    def test_disk_full_before_visibility_remains_query_only(self):
        def fault(stage):
            if stage == "before_publish":
                raise OSError(errno.ENOSPC, "injected disk full")
        receipt = self.gateway.submit(self.order, fault=fault)
        self.assertEqual(receipt["state"], "UNKNOWN")
        self.assertFalse(list((self.root / "input").glob("*.csv")))
        self.assertEqual(Gateway(self.root, "TEST_ACCOUNT").submit(self.order)["state"], "UNKNOWN")

    def test_permission_error_after_visibility_fin_is_not_commit(self):
        simulator = self.simulator()
        def fault(stage):
            if stage == "after_publish":
                raise PermissionError("injected fin permission failure")
        receipt = self.gateway.submit(self.order, fault=fault)
        self.assertEqual(receipt["state"], "UNKNOWN")
        self.assertFalse(list((self.root / "input").glob("*.fin")))
        simulator.scan()
        self.assertEqual(self.gateway.query("test-1")["state"], "ACKNOWLEDGED")
        self.assertTrue(self.gateway.submit(self.order)["duplicate"])
        self.assertEqual(len(list((self.root / "input").glob("*.csv"))), 1)

    def test_crash_after_claim_does_not_retry(self):
        def fault(stage):
            if stage == "after_claim":
                raise Crash()
        with self.assertRaises(Crash):
            self.gateway.submit(self.order, fault=fault)
        result = Gateway(self.root, "TEST_ACCOUNT").submit(self.order)
        self.assertEqual(result["state"], "UNKNOWN")
        self.assertFalse(list((self.root / "input").glob("*.csv")))

    def test_corrupt_transport_receipt_cannot_reset_identity(self):
        self.gateway.submit(self.order)
        receipt = next((self.root / "spool").glob("*/receipt.json"))
        receipt.write_text("{", encoding="utf-8")
        repeated = self.gateway.submit(self.order)
        self.assertEqual(repeated["state"], "UNKNOWN")
        self.assertEqual(len(list((self.root / "input").glob("*.csv"))), 1)

    def test_real_process_exit_after_publish_recovers_without_rewrite(self):
        script = """
import json, os, sys
from novatrader_broker.gateway import Gateway
def crash(stage):
    if stage == 'after_publish': os._exit(77)
Gateway(sys.argv[1], 'TEST_ACCOUNT').submit(json.loads(sys.argv[2]), fault=crash)
"""
        completed = subprocess.run([sys.executable, "-c", script, str(self.root), json.dumps(self.order)], check=False, timeout=10)
        self.assertEqual(completed.returncode, 77)
        self.assertEqual(self.gateway.submit(self.order)["state"], "UNKNOWN")
        self.assertEqual(len(list((self.root / "input").glob("*.csv"))), 1)
        simulator = self.simulator()
        simulator.scan()
        self.assertEqual(self.gateway.query("test-1")["state"], "ACKNOWLEDGED")

    def test_missing_report_is_unavailable_not_zero(self):
        snapshot = self.gateway.snapshot()
        self.assertFalse(snapshot["reports_ready"])
        self.assertFalse(snapshot["reports"]["cash.csv"]["available"])
        expected = {"account_id": "TEST_ACCOUNT", "balance": "0", "available": "0", "frozen": "0", "positions": [], "orders": []}
        self.assertEqual(self.gateway.reconcile(expected)["state"], "BLOCKED")

    def test_t1_cash_and_position_discrepancies_block(self):
        simulator = self.simulator()
        self.gateway.submit(self.order)
        simulator.scan()
        simulator.fill("test-1", 100, "fill-1")
        expected = simulator.expected()
        expected["available"] = "0"
        expected["positions"][0]["available_now"] = 100
        result = self.gateway.reconcile(expected)
        self.assertEqual(result["state"], "BLOCKED")
        self.assertEqual({item["code"] for item in result["differences"]}, {"CASH_MISMATCH", "POSITION_MISMATCH"})

    def test_duplicate_fill_deduplicates_but_conflict_is_unavailable(self):
        simulator = self.simulator()
        self.gateway.submit(self.order)
        simulator.scan()
        simulator.fill("test-1", 100, "fill-1")
        simulator.executions.append(dict(simulator.executions[0]))
        simulator.write_all()
        self.assertEqual(len(self.gateway.query("test-1")["executions"]), 1)
        self.assertEqual(self.gateway.reconcile(simulator.expected())["state"], "ISOLATED_MATCHED")
        simulator.executions[-1]["volume"] = 200
        simulator.executions[-1]["amount"] = "2100.00"
        simulator.write_all()
        report = self.gateway.snapshot()["reports"]["execution_report.csv"]
        self.assertFalse(report["available"])
        self.assertEqual(report["reason"], "CONFLICTING_REPORT_IDENTITY")

    def test_repeated_fill_with_new_receive_time_keeps_identity(self):
        simulator = self.simulator()
        self.gateway.submit(self.order)
        simulator.scan()
        simulator.fill("test-1", 100, "fill-1")
        repeated = dict(simulator.executions[0])
        repeated["recv_at"] = simulator._timestamp()
        simulator.executions.append(repeated)
        simulator.write_all()
        self.assertEqual(len(self.gateway.query("test-1")["executions"]), 1)
        self.assertEqual(self.gateway.reconcile(simulator.expected())["state"], "ISOLATED_MATCHED")

    def test_execution_symbol_or_money_mismatch_blocks(self):
        simulator = self.simulator()
        self.gateway.submit(self.order)
        simulator.scan()
        simulator.fill("test-1", 100, "fill-1")
        simulator.executions[0]["symbol"] = "SZSE.000001"
        simulator.write_all()
        self.assertEqual(self.gateway.query("test-1")["state"], "UNKNOWN")
        self.assertEqual(self.gateway.reconcile(simulator.expected())["state"], "BLOCKED")
        simulator.executions[0]["symbol"] = "SHSE.600519"
        simulator.orders["test-1"]["filled_amount"] = "1000"
        simulator.changes[-1]["filled_amount"] = "1000"
        simulator.write_all()
        result = self.gateway.reconcile(simulator.expected())
        self.assertIn("FILL_AMOUNT_MISMATCH", {row["code"] for row in result["differences"]})

    def test_partial_append_preserves_complete_rows_and_blocks_reconcile(self):
        simulator = self.simulator()
        self.gateway.submit(self.order)
        simulator.scan()
        simulator.fill("test-1", 100, "fill-1")
        report_path = self.gateway.storage.output / "execution_report.csv"
        with open(report_path, "ab") as handle:
            handle.write(b"TEST_ACCOUNT,unfinished")
        snapshot = self.gateway.snapshot()
        self.assertEqual(len(snapshot["reports"]["execution_report.csv"]["records"]), 1)
        self.assertGreater(snapshot["reports"]["execution_report.csv"]["partial_bytes"], 0)
        self.assertFalse(snapshot["reports_ready"])
        self.assertEqual(self.gateway.reconcile(simulator.expected())["state"], "BLOCKED")

    def test_partial_snapshot_and_unknown_enum_are_not_accepted(self):
        simulator = self.simulator()
        cash_path = self.gateway.storage.output / "cash.csv"
        content = cash_path.read_bytes()
        cash_path.write_bytes(content[:-3])
        self.assertEqual(self.gateway.snapshot()["reports"]["cash.csv"]["reason"], "PARTIAL_REPORT")
        simulator.write_all()
        self.gateway.submit(self.order)
        simulator.scan()
        simulator.orders["test-1"]["status"] = "999"
        simulator.write_all()
        report = self.gateway.snapshot()["reports"]["order_status.csv"]
        self.assertFalse(report["available"])
        self.assertEqual(report["reason"], "UNVERIFIED_ORDER_ENUM")

    def test_stale_or_future_mtime_never_ready(self):
        self.simulator()
        path = self.gateway.storage.output / "cash.csv"
        for stamp in (time.time() - 60, time.time() + 60):
            os.utime(path, (stamp, stamp))
            self.assertFalse(self.gateway.snapshot()["reports_ready"])

    def test_cancel_rejected_after_fill_is_report_not_success(self):
        simulator = self.simulator()
        self.gateway.submit(self.order)
        simulator.scan()
        self.gateway.cancel({"request_id": "cancel-1", "sid": "test-1"})
        simulator.fill("test-1", 200, "fill-1")
        simulator.scan()
        result = self.gateway.query("test-1")
        self.assertEqual(result["state"], "FILLED")
        self.assertIn("CANCEL_REJECTED", [row["kind"] for row in result["executions"]])
        self.assertFalse(self.gateway.cancel({"request_id": "cancel-1", "sid": "test-1"})["cancel_confirmed"])
        with self.assertRaises(ProtocolError):
            self.gateway.cancel({"request_id": "cancel-2", "sid": "test-1"})

    def test_regressive_or_conflicting_order_evidence_blocks(self):
        simulator = self.simulator()
        self.gateway.submit(self.order)
        simulator.scan()
        simulator.fill("test-1", 100, "fill-1")
        simulator.orders["test-1"].update(filled_volume=0, status="1", updated_at=simulator._timestamp())
        simulator.write_all()
        self.assertEqual(self.gateway.query("test-1")["state"], "UNKNOWN")
        self.assertEqual(self.gateway.reconcile(simulator.expected())["state"], "BLOCKED")

    def test_protocol_validation_and_unknown_mapping(self):
        for change in ({"price": "NaN"}, {"price": "Infinity"}, {"price": "10.501"}, {"volume": True},
                       {"symbol": "830001.BJ"}, {"sid": "../escape"}, {"sid": "x\r\n"}, {"extra": 1}):
            with self.subTest(change=change), self.assertRaises(ProtocolError):
                order({**self.order, **change})
        with self.assertRaises(ProtocolError):
            Gateway(self.root, "../bad")
        with self.assertRaises(ProtocolError):
            Gateway(self.root, "CON")

    def test_initialization_cannot_adopt_existing_directory(self):
        with self.assertRaisesRegex(ProtocolError, "EMPTY_DIRECTORY"):
            initialize(self.root)
        with tempfile.TemporaryDirectory() as directory:
            with self.assertRaisesRegex(ProtocolError, "ISOLATED_ROOT_REQUIRED"):
                Gateway(directory, "TEST_ACCOUNT")


class AtomicReplaceRetryTests(unittest.TestCase):
    """元数据 os.replace 限次重试矩阵（D7）：只对 winerror 5/32/33 退避，预算 ~2s 封顶。

    其余错误码立即抛——重试无意义；重试预算参数化但有生产默认，测试里用
    极小 sleep 验证次数语义，不打时间仗。"""

    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="novatrader-storage-test-")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        initialize(self.root)
        self.target = self.root / "spool" / "receipt.json"

    def _blocking_replace(self, winerror, failures_before_success=None):
        calls = {"n": 0}
        real_replace = os.replace

        def fake_replace(src, dst):
            calls["n"] += 1
            if failures_before_success is None or calls["n"] <= failures_before_success:
                error = PermissionError("injected reader holding file")
                error.winerror = winerror
                raise error
            return real_replace(src, dst)

        return fake_replace, calls

    def test_retryable_winerror_eventually_succeeds(self):
        for winerror in (5, 32, 33):
            with self.subTest(winerror=winerror):
                fake, calls = self._blocking_replace(winerror, failures_before_success=3)
                with unittest.mock.patch("novatrader_broker.storage.os.replace", fake), \
                        unittest.mock.patch("novatrader_broker.storage.time.sleep", lambda _: None):
                    storage.atomic_json(self.target, {"state": "PREPARED"}, retry_base_sleep=0)
                self.assertEqual(calls["n"], 4)
                self.assertEqual(json.loads(self.target.read_text("utf-8")), {"state": "PREPARED"})
                self.target.unlink()

    def test_retry_budget_exhaustion_raises_and_leaves_no_partial(self):
        for winerror in (5, 32, 33):
            with self.subTest(winerror=winerror):
                fake, calls = self._blocking_replace(winerror)
                with unittest.mock.patch("novatrader_broker.storage.os.replace", fake), \
                        unittest.mock.patch("novatrader_broker.storage.time.sleep") as sleep_mock, \
                        self.assertRaises(PermissionError):
                    storage.atomic_json(self.target, {"state": "PREPARED"}, retry_base_sleep=0)
                self.assertEqual(calls["n"], storage.REPLACE_RETRY_ATTEMPTS)
                self.assertEqual(sleep_mock.call_count, storage.REPLACE_RETRY_ATTEMPTS - 1)
                self.assertFalse(self.target.exists())
                self.assertFalse(list(self.root.glob("spool/.state-*")))

    def test_non_retryable_winerror_raises_immediately(self):
        fake, calls = self._blocking_replace(87, failures_before_success=10)
        with unittest.mock.patch("novatrader_broker.storage.os.replace", fake), \
                unittest.mock.patch("novatrader_broker.storage.time.sleep") as sleep_mock, \
                self.assertRaises(PermissionError):
            storage.atomic_json(self.target, {"state": "PREPARED"}, retry_base_sleep=0)
        self.assertEqual(calls["n"], 1)
        sleep_mock.assert_not_called()

    def test_backoff_budget_stays_under_two_seconds(self):
        budget = sum(storage.REPLACE_RETRY_BASE_SLEEP * (attempt + 1)
                     for attempt in range(storage.REPLACE_RETRY_ATTEMPTS - 1))
        self.assertLessEqual(budget, 2.0)


if __name__ == "__main__":
    unittest.main()
