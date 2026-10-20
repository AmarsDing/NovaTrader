"""Baostock 角色离线形态校验：sys.modules 注假模块，不装真包、不联网、不起服务。

跑法（pyworker 目录下）：
    python -m unittest test_baostock.py -v
pytest 兼容：未安装真 baostock 不影响（全部用假模块注入）；真包冒烟用例自带 skip 哨兵。
"""
import builtins
import importlib.util
import sys
import threading
import time
import unittest
from unittest.mock import patch

import app

_HAS_REAL_BS = importlib.util.find_spec("baostock") is not None


class _FakeResult:
    """对齐 baostock ResultData 形态：error_code / fields / next() / get_row_data()。"""

    def __init__(self, error_code="0", error_msg="", fields=None, rows=None):
        self.error_code = error_code
        self.error_msg = error_msg
        self.fields = fields or []
        self._rows = rows or []
        self._i = -1

    def next(self):
        self._i += 1
        return self._i < len(self._rows)

    def get_row_data(self):
        return self._rows[self._i]


D_FIELDS = "date,code,open,high,low,close,preclose,volume,amount".split(",")


class _FakeBs:
    """假 baostock 模块：login 计数、可注入断线码、查询可 sleep 以检验互斥。"""

    def __init__(self, *, disconnect_once=False, query_sleep=0.0, lock_log=None):
        self.login_calls = 0
        self._disconnect_once = disconnect_once
        self._query_sleep = query_sleep
        self._lock_log = lock_log  # list，记 (事件, 线程名) 检验串行
        self.bars_rows = [
            # 好行：preclose 正常
            ["2026-10-16", "sh.600519", "1700.0", "1710.0", "1690.0", "1705.0", "1699.5", "3600000", "6120000000"],
            # 坏值行：preclose/volume 空串 → 应映射 null（不冒充 0）
            ["2026-10-15", "sh.600519", "1690.0", "1700.0", "1680.0", "1695.0", "", "", ""],
        ]

    def login(self):
        self.login_calls += 1
        return _FakeResult(error_code="0")

    def query_history_k_data_plus(self, code, fields, **kwargs):
        if self._lock_log is not None:
            self._lock_log.append(("enter", threading.current_thread().name))
        if self._query_sleep:
            time.sleep(self._query_sleep)
        if self._disconnect_once:
            self._disconnect_once = False
            if self._lock_log is not None:
                self._lock_log.append(("exit", threading.current_thread().name))
            return _FakeResult(error_code="-10001", error_msg="断线")
        if self._lock_log is not None:
            self._lock_log.append(("exit", threading.current_thread().name))
        if code == "sh.600519":
            return _FakeResult(fields=D_FIELDS, rows=self.bars_rows)
        return _FakeResult(fields=D_FIELDS, rows=[])


class BaostockRoleTest(unittest.TestCase):
    def setUp(self):
        # 每个用例干净的模块/登录态（bs_module 缓存在 app._bs）。
        app._bs["mod"] = None
        app._bs["logged_in"] = False

    def tearDown(self):
        sys.modules.pop("baostock", None)
        app._bs["mod"] = None
        app._bs["logged_in"] = False

    def _install(self, fake):
        sys.modules["baostock"] = fake

    # ① 行映射：time ISO 带 +08:00、volume 原样股数不乘100、坏值 → null、BJ 进 skipped。
    def test_bars_mapping_and_skipped(self):
        self._install(_FakeBs())
        status, body = app.bs_bars(
            {"freq": "1d", "start": "2026-10-15", "end": "2026-10-16", "expect": "2026-10-16",
             "symbols": ["600519.SH", "830799.BJ"]}
        )
        self.assertEqual(status, 200)
        self.assertEqual(body["skipped"], ["830799.BJ"])
        self.assertEqual(len(body["bars"]), 2)
        good = next(b for b in body["bars"] if b["time"].startswith("2026-10-16"))
        bad = next(b for b in body["bars"] if b["time"].startswith("2026-10-15"))
        self.assertEqual(good["time"], "2026-10-16T00:00:00+08:00")
        self.assertEqual(good["close"], 1705.0)
        self.assertEqual(good["pre_close"], 1699.5)
        self.assertEqual(good["volume"], 3600000)  # 股，原样不乘 100
        self.assertIsNone(bad["pre_close"])  # 空串 → null，不冒充 0
        self.assertIsNone(bad["volume"])
        self.assertEqual(body["last_date"], "2026-10-16")
        self.assertFalse(body["stale"])

    def test_bars_unsupported_freq_is_400(self):
        self._install(_FakeBs())
        status, body = app.bs_bars({"freq": "1m", "symbols": ["600519.SH"]})
        self.assertEqual(status, 400)

    # ② 假 import 失败：sys.modules 拔键 + __import__ 拒载 → 503 reason 含「未安装 baostock」。
    def test_import_failure_is_503_unavailable(self):
        sys.modules.pop("baostock", None)
        real_import = builtins.__import__

        def deny(name, *args, **kwargs):
            if name == "baostock":
                raise ImportError("No module named 'baostock'")
            return real_import(name, *args, **kwargs)

        with patch.object(builtins, "__import__", side_effect=deny):
            status, body = app.bs_bars({"freq": "1d", "symbols": ["600519.SH"]})
        self.assertEqual(status, 503)
        self.assertEqual(body["error"], "unavailable")
        self.assertIn("未安装 baostock", body["reason"])

    # ③ 首次查询 error_code==-10001：锁内重登并重试一次，login 被调两次。
    def test_disconnect_code_triggers_single_relogin_retry(self):
        fake = _FakeBs(disconnect_once=True)
        self._install(fake)
        status, body = app.bs_bars({"freq": "1d", "start": "2026-10-16", "end": "2026-10-16",
                                    "symbols": ["600519.SH"]})
        self.assertEqual(status, 200)
        self.assertEqual(fake.login_calls, 2)  # 首次登录 + 断线后锁内重登各一次
        self.assertEqual(len(body["bars"]), 2)

    # ④ 两线程并发调 bs_bars（假查询 sleep 5ms）：事件日志证明互斥串行进入。
    def test_concurrent_calls_are_serialized(self):
        log = []
        self._install(_FakeBs(query_sleep=0.005, lock_log=log))
        symbols = ["600519.SH"]
        out = []

        def worker():
            out.append(app.bs_bars({"freq": "1d", "start": "2026-10-16", "end": "2026-10-16",
                                    "symbols": symbols}))

        ts = [threading.Thread(target=worker) for _ in range(2)]
        for t in ts:
            t.start()
        for t in ts:
            t.join()
        self.assertTrue(all(s == 200 for s, _ in out))
        events = [e for e, _ in log]
        # 串行铁证：enter/exit 严格交替，绝不允许 enter,enter 交错。
        self.assertEqual(len(events), 4)
        for i, (e, _) in enumerate(log):
            self.assertEqual(e, "enter" if i % 2 == 0 else "exit", f"事件流交错：{log}")

    # 哨兵：真包装了才跑的形态冒烟（本批环境纪律禁止 pip install，默认 skip）。
    @unittest.skipUnless(_HAS_REAL_BS, "baostock 未安装（pyworker/requirements.txt）")
    def test_real_module_smoke(self):
        import baostock as bs

        lg = bs.login()
        self.assertEqual(getattr(lg, "error_code", "-1"), "0")
        bs.logout()


if __name__ == "__main__":
    unittest.main()
