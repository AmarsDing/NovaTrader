from __future__ import annotations

import io
import json
import struct
import tempfile
import unittest
import urllib.error
from pathlib import Path
from unittest.mock import patch
import zipfile

from nt_sources.adapters import SourceError, download, normalize_fred, parse_tdx
from nt_sources.adapters import openbb_macro
from worker import freeze, run


def archive(*, omit_bj=False, duplicate=False, truncated=False):
    out = io.BytesIO()
    with zipfile.ZipFile(out, "w") as z:
        for market, code in (("sh", "600001"), ("sz", "000001"), ("bj", "920001")):
            if market == "bj" and omit_bj:
                continue
            cod = bytearray(150)
            cod[:6] = code.encode()
            struct.pack_into("<H", cod, 32, 0)
            name = "测试证券".encode("gbk")
            cod[40:40 + len(name)] = name
            price = bytearray(512)
            struct.pack_into("<d", price, 4, 10)
            struct.pack_into("<4d", price, 12, 10, 11, 9, 10.5)
            struct.pack_into("<Q", price, 56, 12345)
            struct.pack_into("<d", price, 72, 129622.5)
            if duplicate:
                cod = cod * 2
                price = price * 2
            z.writestr(f"{market}250106.cod", cod)
            z.writestr(f"{market}250106.md1", price[:-1] if truncated else price)
    return out.getvalue()


class SourceTests(unittest.TestCase):
    def test_units_coverage_and_missing_publication(self):
        rows, meta = parse_tdx(archive(), "2025-01-06")
        self.assertEqual(len(rows), 3)
        self.assertEqual(rows[0]["volume"], 12345)  # Shares, never blind *100.
        self.assertEqual(rows[0]["amount"], 129622.5)
        self.assertIsNone(rows[0]["available_at"])
        self.assertIsNone(meta["coverage_ratio"])
        self.assertEqual(meta["markets"]["bj"]["a_stock_count"], 1)

    def test_partial_markets_duplicate_truncation_changed_format(self):
        for raw in (archive(omit_bj=True), archive(duplicate=True), archive(truncated=True), b"<html>blocked</html>"):
            with self.subTest(size=len(raw)):
                with self.assertRaises(SourceError) as raised:
                    parse_tdx(raw, "2025-01-06")
                self.assertEqual(raised.exception.code, "format_changed")

    def test_empty_result_is_not_healthy(self):
        # A structurally valid archive with no code/price records is not a successful source.
        out = io.BytesIO()
        with zipfile.ZipFile(out, "w") as z:
            for market in ("sh", "sz", "bj"):
                z.writestr(f"{market}250106.cod", b"")
                z.writestr(f"{market}250106.md1", b"")
        with self.assertRaises(SourceError) as raised:
            parse_tdx(out.getvalue(), "2025-01-06")
        self.assertEqual(raised.exception.code, "empty_result")

    def test_rate_limit_and_missing_archive(self):
        for status, expected in ((429, "rate_limited"), (404, "not_published"), (503, "unavailable")):
            with patch("urllib.request.urlopen", side_effect=urllib.error.HTTPError("https://www.tdx.com.cn", status, "test", {}, None)):
                with self.assertRaises(SourceError) as raised:
                    download("https://www.tdx.com.cn/test")
                self.assertEqual(raised.exception.code, expected)

    def test_macro_preserves_provider_unit_but_not_false_vintage(self):
        rows, meta = normalize_fred({"results": [{"date": "2025-01-01", "CPIAUCSL": 300.1}],
                                     "extra": {"metadata": {"CPIAUCSL": {"units": "Index 1982-1984=100"}}}}, "CPIAUCSL")
        self.assertEqual(rows[0]["unit"], "Index 1982-1984=100")
        self.assertIsNone(rows[0]["available_at"])
        self.assertFalse(meta["training_ready"])
        with self.assertRaises(SourceError):
            normalize_fred({"results": [{"date": "2025-01-01", "OTHER": 300.1}]}, "CPIAUCSL")

    def test_macro_key_and_optional_version_fail_closed(self):
        with patch.dict("os.environ", {}, clear=True):
            with self.assertRaises(SourceError) as raised:
                openbb_macro("CPIAUCSL", "2024-01-01", "2025-01-01")
            self.assertEqual(raised.exception.code, "unavailable")
        with patch.dict("os.environ", {"FRED_API_KEY": "synthetic-test-key"}, clear=True):
            with patch("importlib.metadata.version", return_value="1.0.0"):
                with self.assertRaises(SourceError) as raised:
                    openbb_macro("CPIAUCSL", "2024-01-01", "2025-01-01")
                self.assertNotIn("synthetic-test-key", str(raised.exception))
                self.assertEqual(raised.exception.code, "unavailable")

    def test_macro_missing_values_and_duplicates_not_silently_merged(self):
        with self.assertRaises(SourceError):
            normalize_fred({"results": [{"date": "2025-01-01", "CPIAUCSL": 1},
                                         {"date": "2025-01-01", "CPIAUCSL": 2}]}, "CPIAUCSL")
        with self.assertRaises(SourceError):
            normalize_fred({"results": [{"date": "2025-01-01", "CPIAUCSL": None}]}, "CPIAUCSL")

    def test_freeze_repeat_identical_and_conflict_detected(self):
        rows, coverage = parse_tdx(archive(), "2025-01-06")
        meta = {"dataset": "unadjusted_a_share_daily_bar", "provider": "tdx.com.cn", "coverage": coverage}
        with tempfile.TemporaryDirectory() as temp:
            directory = Path(temp)
            raw = archive()
            one = freeze(directory, rows, meta, raw)
            two = freeze(directory, rows, meta, raw)
            self.assertEqual(one, two)
            (directory / one["artifact_file"]).write_text("bad", encoding="utf-8")
            with self.assertRaises(SourceError) as raised:
                freeze(directory, rows, meta, raw)
            self.assertEqual(raised.exception.code, "artifact_conflict")

    def test_failed_durable_write_never_publishes_complete_artifact(self):
        rows, coverage = parse_tdx(archive(), "2025-01-06")
        meta = {"dataset": "unadjusted_a_share_daily_bar", "provider": "tdx.com.cn", "coverage": coverage}
        with tempfile.TemporaryDirectory() as temp:
            directory = Path(temp)
            with patch("os.fsync", side_effect=OSError("disk full fixture")):
                with self.assertRaises(OSError):
                    freeze(directory, rows, meta, archive())
            self.assertEqual(list(directory.iterdir()), [])

    def test_default_disabled_and_no_arbitrary_remote_code(self):
        with tempfile.TemporaryDirectory() as temp:
            self.assertEqual(run({"schema_version": 1}, Path(temp))["status"], "disabled")
            for job in ({"schema_version": 1, "enabled": True, "operation": "exec"},
                        {"schema_version": 1, "enabled": True, "url": "https://example.org"}):
                with self.assertRaises(SourceError):
                    run(job, Path(temp))


if __name__ == "__main__":
    unittest.main()
