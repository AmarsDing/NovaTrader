from __future__ import annotations

import hashlib
import json
import os
from pathlib import Path
import tempfile
import threading
import unittest
import urllib.error
import urllib.request
from unittest.mock import patch

from http_worker import SourceServer, collect, encode, RECORDED_SHA


class HTTPSourceTests(unittest.TestCase):
    def server(self, directory, *, enabled=False):
        server = SourceServer(("127.0.0.1", 0), enabled=enabled, evidence=Path(directory), recorded_only=True)
        thread = threading.Thread(target=server.serve_forever, kwargs={"poll_interval": 0.05}, daemon=True)
        thread.start()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        return f"http://127.0.0.1:{server.server_port}"

    def post(self, base, job):
        req = urllib.request.Request(base + "/v1/source/collect", data=encode(job), headers={"Content-Type": "application/json"})
        try:
            with urllib.request.urlopen(req, timeout=10) as response:
                return response.status, json.load(response)
        except urllib.error.HTTPError as error:
            return error.code, json.load(error)

    def test_default_disabled_and_request_cannot_enable(self):
        with tempfile.TemporaryDirectory() as directory:
            base = self.server(directory)
            status, body = self.post(base, {"schema_version": 1, "operation": "tdx_history", "date": "2025-01-06", "enabled": True})
            self.assertEqual(status, 503)
            self.assertEqual(body["status"], "disabled")
            self.assertEqual(list(Path(directory).iterdir()), [])
            with urllib.request.urlopen(base + "/healthz") as response:
                health = json.load(response)
            self.assertFalse(health["enabled"])
            self.assertFalse(health["coverage_known"])
            self.assertEqual(health["providers"], "unprobed")

    def test_full_recorded_archive_is_not_thirty_sample_poc(self):
        with tempfile.TemporaryDirectory() as directory:
            base = self.server(directory, enabled=True)
            with patch("urllib.request.urlopen", wraps=urllib.request.urlopen) as request:
                status, body = self.post(base, {"schema_version": 1, "operation": "tdx_history", "date": "2025-01-06"})
            self.assertEqual(request.call_count, 1)  # Only this loopback POST; no redownload.
            self.assertEqual(status, 200)
            self.assertEqual(body["record_count"], 5370)
            self.assertEqual(len(body["records"]), 5370)
            self.assertEqual(body["provider_raw_sha256"], RECORDED_SHA)
            self.assertEqual(body["records_sha256"], hashlib.sha256(encode(body["records"])).hexdigest())
            self.assertEqual(body["acquisition_mode"], "recorded_provider_bytes")
            self.assertFalse(body["training_ready"])
            self.assertIsNone(body["coverage"]["coverage_ratio"])
            self.assertTrue(all(row["available_at"] is None for row in body["records"]))
            samples = [json.loads(path.read_bytes()) for path in Path(directory).glob("*.json")]
            self.assertEqual(samples[0]["sample_count"], 30)
            self.assertEqual(samples[0]["row_count"], 5370)

    def test_no_free_url_path_snapshot_or_recording_date(self):
        with tempfile.TemporaryDirectory() as directory:
            base = self.server(directory, enabled=True)
            for job in ({"schema_version": 1, "operation": "snapshot"},
                        {"schema_version": 1, "operation": "tdx_history", "date": "2025-01-06", "url": "https://secret.invalid"},
                        {"schema_version": 1, "operation": "tdx_history", "date": "2025-01-06", "path": "C:/secret"}):
                status, body = self.post(base, job)
                self.assertEqual(status, 400)
                self.assertNotIn("secret", json.dumps(body))
            status, body = self.post(base, {"schema_version": 1, "operation": "tdx_history", "date": "2025-01-07"})
            self.assertEqual(status, 503)
            self.assertEqual(body["status"], "unavailable")

    def test_macro_missing_key_is_unavailable_and_no_body_secret(self):
        with tempfile.TemporaryDirectory() as directory, patch.dict(os.environ, {}, clear=True):
            base = self.server(directory, enabled=True)
            status, body = self.post(base, {"schema_version": 1, "operation": "openbb_macro", "series": "CPIAUCSL", "start": "2025-01-01", "end": "2025-12-31"})
            self.assertEqual(status, 503)
            self.assertEqual(body["status"], "unavailable")
            self.assertFalse(body["training_ready"])
            self.assertEqual(list(Path(directory).iterdir()), [])

    def test_loopback_and_parallel_limit(self):
        with tempfile.TemporaryDirectory() as directory:
            with self.assertRaises(ValueError):
                SourceServer(("0.0.0.0", 0), evidence=Path(directory))
            server = SourceServer(("127.0.0.1", 0), evidence=Path(directory), enabled=True)
            self.addCleanup(server.server_close)
            self.assertTrue(server.slots.acquire(False))
            self.assertTrue(server.slots.acquire(False))
            self.assertFalse(server.slots.acquire(False))
            server.slots.release()
            server.slots.release()


if __name__ == "__main__":
    unittest.main()
