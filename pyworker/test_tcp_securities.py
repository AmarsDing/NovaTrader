"""tdx_tcp 证券枚举离线形态校验：注入假客户端，不联网、不起服务（S10/gap⑤）。

跑法（pyworker 目录下）：
    python -m unittest test_tcp_securities.py -v

钉死的纪律：
- BJ 走 pytdx 原生 get_security_list(market=2) 并按 stock_count 真分页；
- 计数失败/整表为空时显式跳过：skipped_markets=["BJ"]，
  绝不允许拿空清单冒充「BJ 无证券」；
- 中间断页时保留已取前缀、只告警不标 skipped（部分枚举优于整体缺失）。
"""
import unittest

import pandas as pd

import app


def _shz_df():
    return pd.DataFrame(
        [
            {"code": "600519", "name": "贵州茅台"},
            {"code": "000001", "name": "平安银行"},
        ]
    )


class _FakeRaw:
    """pytdx 原生客户端替身：记录分页 start，按页返回 BJ 行。"""

    def __init__(self, pages=None, fail_at=None):
        self.calls = []
        self._pages = pages or []
        self._fail_at = fail_at

    def get_security_list(self, market, start):
        self.calls.append(start)
        if self._fail_at is not None and start >= self._fail_at:
            raise ConnectionError("relay dropped")
        idx = start // 1000
        if idx >= len(self._pages):
            return []
        return self._pages[idx]


class _FakeTCP:
    """mootdx StdQuotes 替身：stocks()/stock_count() + .client 原生口。"""

    def __init__(self, bj_count=0, bj_pages=None, count_fails=False, fail_at=None):
        self.client = _FakeRaw(bj_pages, fail_at=fail_at)
        self._bj_count = bj_count
        self._count_fails = count_fails

    def stocks(self, market=1):
        return _shz_df()

    def stock_count(self, market=1):
        if self._count_fails and market == 2:
            raise ConnectionError("server has no BJ segment")
        return self._bj_count


class TcpSecuritiesTest(unittest.TestCase):
    def setUp(self):
        self._saved = app._tcp

    def tearDown(self):
        app._tcp = self._saved

    # ① BJ 真分页：count=2100 → start 依次为 0/1000/2000，末页 100 行也收进。
    def test_bj_enumeration_paginates_by_count(self):
        pages = [
            [{"code": f"83{i:04d}", "name": f"北{i}"} for i in range(1000)],
            [{"code": f"87{i:04d}", "name": f"京{i}"} for i in range(1000)],
            [{"code": f"92{i:04d}", "name": f"新{i}"} for i in range(100)],
        ]
        fake = _FakeTCP(bj_count=2100, bj_pages=pages)
        app._tcp = fake
        items = app.tcp_securities_items()
        self.assertEqual(fake.client.calls, [0, 1000, 2000])
        bj = [x for x in items if x["market"] == "BJ"]
        self.assertEqual(len(bj), 2100)
        # SH/SZ 两市场仍在，BJ 追加而非替代。
        self.assertIn({"symbol": "600519.SH", "name": "贵州茅台", "market": "SH"}, items)
        self.assertIn({"symbol": "000001.SZ", "name": "平安银行", "market": "SZ"}, items)
        self.assertEqual(bj[-1]["symbol"], "920099.BJ")

    # ② 主站无 BJ 段（计数抛错）：枚举照常给 SH/SZ，出口显式标 skipped_markets。
    def test_bj_unsupported_server_marks_skipped_markets(self):
        fake = _FakeTCP(count_fails=True)
        app._tcp = fake

        class FakeDF:
            @staticmethod
            def _rows():
                rows = [{"code": f"60{i:04d}", "name": n} for i, n in enumerate(list("甲乙丙丁") * 30)]
                return pd.DataFrame(rows)

        # SH/SZ 各给 120 只，总数过 100 的 unavailable 闸。
        def stocks(market=1):
            return FakeDF._rows()

        fake.stocks = stocks
        status, body = app.tcp_securities()
        self.assertEqual(status, 200)
        self.assertEqual(len(body["items"]), 240)
        self.assertEqual(body["skipped_markets"], ["BJ"])

    # ③ BJ 分页中段断连：保留已取页，BJ 条目不完整但不再静默整市场失踪时同样标注。
    def test_bj_page_failure_keeps_prefix(self):
        pages = [[{"code": "830799", "name": "某北"}]]
        fake = _FakeTCP(bj_count=2000, bj_pages=pages, fail_at=1000)
        app._tcp = fake

        def stocks(market=1):
            return pd.DataFrame([{"code": f"60{i:04d}", "name": "测"} for i in range(120)])

        fake.stocks = stocks
        status, body = app.tcp_securities()
        self.assertEqual(status, 200)
        bj = [x for x in body["items"] if x["market"] == "BJ"]
        self.assertEqual(bj, [{"symbol": "830799.BJ", "name": "某北", "market": "BJ"}])
        # 有 BJ 条目就不标 skipped_markets（部分覆盖的信号在日志，count 对账见 _tcp_bj_items）。
        self.assertNotIn("skipped_markets", body)

    # ④ BJ 计数承诺仍有券、中间页却返空：真实 pytdx 的失败形态是静默空页不抛错，
    #    必须告警留痕（否则部分枚举与「BJ 恰好就这么几只」无法区分）。
    def test_bj_empty_page_mid_count_warns(self):
        pages = [[{"code": "830799", "name": "某北"}]]  # 第 2 页 1000 起返空
        fake = _FakeTCP(bj_count=2100, bj_pages=pages)
        app._tcp = fake
        with self.assertLogs("pyworker", level="WARNING") as cm:
            items = app.tcp_securities_items()
        bj = [x for x in items if x["market"] == "BJ"]
        self.assertEqual(bj, [{"symbol": "830799.BJ", "name": "某北", "market": "BJ"}])
        self.assertTrue(
            any("空页" in m and "1000" in m and "2100" in m for m in cm.output),
            f"空页告警未打或口径不对: {cm.output}",
        )


if __name__ == "__main__":
    unittest.main()
