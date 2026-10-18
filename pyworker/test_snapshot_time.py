import unittest
from datetime import datetime
from unittest.mock import patch

import app


class QuoteTimeTest(unittest.TestCase):
    def setUp(self):
        self.now = datetime(2026, 10, 14, 10, 0, 0, tzinfo=app.SHANGHAI)

    def test_missing_and_invalid_time_stay_unknown(self):
        for value in (None, "", "invalid", "25:00:00"):
            with self.subTest(value=value):
                self.assertIsNone(app.tcp_quote_time({"servertime": value}, self.now))

    def test_time_only_keeps_source_clock(self):
        expected = self.now.replace(hour=9, minute=59, second=51)
        self.assertEqual(app.tcp_quote_time({"servertime": "09:59:51.500"}, self.now), expected)
        self.assertEqual(app.tcp_quote_time({"time": "09:59:51"}, self.now), expected)

    def test_full_source_date_is_not_replaced(self):
        expected = datetime(2026, 10, 13, 15, 0, 0, tzinfo=app.SHANGHAI)
        self.assertEqual(app.tcp_quote_time({"servertime": expected.isoformat()}, self.now), expected)
        self.assertEqual(app.tcp_quote_time({"time": expected.replace(tzinfo=None)}, self.now), expected)

    def test_tcp_response_marks_unknown_time_stale_without_network(self):
        class Frame:
            empty = False

            def iterrows(self):
                yield 0, {"code": "000001", "market": 0, "price": 10, "vol": 0}

        class Client:
            def quotes(self, symbol):
                return Frame()

        with patch.object(app, "tcp_client", return_value=Client()):
            status, body = app.tcp_snapshots(["000001.SZ"])
        self.assertEqual(status, 200)
        self.assertEqual(len(body["items"]), 1)
        self.assertIsNone(body["items"][0]["time"])
        self.assertTrue(body["items"][0]["stale"])


if __name__ == "__main__":
    unittest.main()
