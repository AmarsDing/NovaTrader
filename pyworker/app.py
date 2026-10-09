"""通达信 / AkShare HTTP 边车。解析只放在这里，datahub 不读 vipdoc 二进制。"""
from __future__ import annotations

import json
import os
import struct
import traceback
from datetime import datetime, timedelta, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import parse_qs, urlparse

SHANGHAI = timezone(timedelta(hours=8))
TDX_DIR = Path(os.environ.get("TDX_DIR", r"D:\new_tdx"))
ROLES = {x.strip() for x in os.environ.get("PYWORKER_ROLES", "local,file,tcp,akshare").split(",") if x.strip()}
LISTEN = os.environ.get("PYWORKER_LISTEN", "127.0.0.1:18080")

MARKETS = {"sh": "SH", "sz": "SZ", "bj": "BJ"}


def iso(dt: datetime) -> str:
    if dt.tzinfo is None:
        dt = dt.replace(tzinfo=SHANGHAI)
    return dt.isoformat()


def json_bytes(obj) -> bytes:
    return json.dumps(obj, ensure_ascii=False, default=str).encode("utf-8")


def unavailable(reason: str):
    return 503, {"error": "unavailable", "reason": reason}


def parse_symbol_file(name: str) -> str | None:
    stem = Path(name).stem.lower()
    for prefix, mkt in MARKETS.items():
        if stem.startswith(prefix) and stem[len(prefix) :].isdigit():
            return f"{stem[len(prefix):].zfill(6)}.{mkt}"
    return None


def code_to_file(symbol: str) -> tuple[str, str] | None:
    s = symbol.strip().upper()
    if "." not in s:
        return None
    code, mkt = s.split(".", 1)
    mkt = mkt.lower()
    if mkt not in MARKETS or len(code) != 6:
        return None
    return mkt, f"{mkt}{code}"


# --- vipdoc -----------------------------------------------------------------

DAY_STRUCT = struct.Struct("<IIIIIfII")


def read_day_file(path: Path, start: str, end: str) -> list[dict]:
    out = []
    data = path.read_bytes()
    symbol = parse_symbol_file(path.name)
    if not symbol:
        return out
    for i in range(0, len(data) - 31, 32):
        date, o, h, l, c, amount, vol, _ = DAY_STRUCT.unpack_from(data, i)
        ymd = f"{date:08d}"
        if start and ymd < start.replace("-", ""):
            continue
        if end and ymd > end.replace("-", ""):
            continue
        try:
            t = datetime.strptime(ymd, "%Y%m%d").replace(tzinfo=SHANGHAI)
        except ValueError:
            continue
        out.append(
            {
                "symbol": symbol,
                "time": iso(t),
                "freq": "1d",
                "open": o / 100.0,
                "high": h / 100.0,
                "low": l / 100.0,
                "close": c / 100.0,
                "volume": int(vol) * 100,
                "amount": float(amount),
            }
        )
    return out


LC_STRUCT = struct.Struct("<HHffffI")


def unpack_lc_date(zip_day: int, minutes: int) -> datetime | None:
    month = int((zip_day % 2048) / 100)
    year = (zip_day >> 11) + 2004
    day = (zip_day % 2048) % 100
    hour = minutes // 60
    minute = minutes % 60
    try:
        end = datetime(year, month, day, hour, minute, tzinfo=SHANGHAI)
    except ValueError:
        return None
    return end - timedelta(minutes=1)


def read_lc_file(path: Path, freq: str, start: str, end: str) -> list[dict]:
    out = []
    data = path.read_bytes()
    symbol = parse_symbol_file(path.name)
    if not symbol:
        return out
    start_d = start.replace("-", "")
    end_d = end.replace("-", "")
    rec = 32
    for i in range(0, len(data) - rec + 1, rec):
        zip_day, minutes, o, h, l, c = struct.unpack_from("<HHffff", data, i)
        vol = struct.unpack_from("<I", data, i + 24)[0]
        t = unpack_lc_date(zip_day, minutes)
        if t is None:
            continue
        ymd = t.strftime("%Y%m%d")
        if start_d and ymd < start_d:
            continue
        if end_d and ymd > end_d:
            continue
        out.append(
            {
                "symbol": symbol,
                "time": iso(t),
                "freq": freq,
                "open": float(o),
                "high": float(h),
                "low": float(l),
                "close": float(c),
                "volume": int(vol) * 100,
                "amount": 0.0,
            }
        )
    return out


def vipdoc_last_date() -> str:
    latest = ""
    for mkt in MARKETS:
        folder = TDX_DIR / "vipdoc" / mkt / "lday"
        if not folder.is_dir():
            continue
        for p in folder.glob("*.day"):
            try:
                size = p.stat().st_size
            except OSError:
                continue
            if size < 32:
                continue
            with p.open("rb") as f:
                f.seek(-32, os.SEEK_END)
                date = struct.unpack("<I", f.read(4))[0]
            ymd = f"{date:08d}"
            if ymd > latest:
                latest = ymd
            break
    if not latest:
        return ""
    return f"{latest[:4]}-{latest[4:6]}-{latest[6:8]}"


def list_day_files() -> list[Path]:
    files = []
    for mkt in MARKETS:
        folder = TDX_DIR / "vipdoc" / mkt / "lday"
        if folder.is_dir():
            files.extend(folder.glob("*.day"))
    return files


def file_bars(body: dict):
    freq = body.get("freq") or "1d"
    start = body.get("start") or ""
    end = body.get("end") or start
    expect = body.get("expect") or ""
    symbols = body.get("symbols") or []
    last = vipdoc_last_date()
    stale = bool(expect) and last < expect
    bars: list[dict] = []
    if freq == "1d":
        files = list_day_files()
        want = None
        if symbols:
            want = set()
            for s in symbols:
                pair = code_to_file(s)
                if pair:
                    want.add(pair[1])
        for p in files:
            if want is not None and p.stem.lower() not in want:
                continue
            bars.extend(read_day_file(p, start, end))
    else:
        suffix = "lc5" if freq == "5m" else "lc1"
        subdir = "fzline" if freq == "5m" else "minline"
        targets = symbols
        if not targets:
            return 400, {"error": "minute bars need symbols, or download full market first"}
        for s in targets:
            pair = code_to_file(s)
            if not pair:
                continue
            mkt, stem = pair
            path = TDX_DIR / "vipdoc" / mkt / subdir / f"{stem}.{suffix}"
            if not path.exists():
                alt = TDX_DIR / "vipdoc" / mkt / subdir / f"{stem}.{suffix[-1]}"
                path = alt if alt.exists() else path
            if path.exists():
                bars.extend(read_lc_file(path, freq, start, end))
    return 200, {"bars": bars, "last_date": last, "stale": stale}


def _read_gbk(path: Path) -> str:
    return path.read_text(encoding="gbk", errors="ignore")


def file_sectors():
    sectors = []
    names = {}
    incon = TDX_DIR / "incon.dat"
    if incon.exists():
        for line in _read_gbk(incon).splitlines():
            if "|" in line and not line.startswith("#"):
                code, name = line.split("|", 1)
                names[code.strip()] = name.strip()
    hy = TDX_DIR / "T0002" / "hq_cache" / "tdxhy.cfg"
    if hy.exists():
        groups: dict[str, list[str]] = {}
        for line in _read_gbk(hy).splitlines():
            parts = line.strip().split("|")
            if len(parts) < 3 or not parts[1].isdigit():
                continue
            mkt = {"0": "SZ", "1": "SH", "2": "BJ"}.get(parts[0], "SZ")
            code = parts[2].strip()
            if not code:
                continue
            groups.setdefault(code, []).append(f"{parts[1].zfill(6)}.{mkt}")
        for code, members in groups.items():
            sectors.append(
                {"code": code, "name": names.get(code, code), "kind": "industry", "members": members}
            )
    root = TDX_DIR / "T0002" / "blocknew"
    if root.is_dir():
        for p in root.glob("*.blk"):
            text = _read_gbk(p)
            members = []
            for line in text.splitlines():
                line = line.strip()
                if not line or line.startswith("#"):
                    continue
                if line[0] in "012" and len(line) >= 7 and line[1:7].isdigit():
                    market = {"1": "SH", "0": "SZ", "2": "BJ"}.get(line[0], "SZ")
                    members.append(f"{line[1:7]}.{market}")
            if members:
                sectors.append({"code": p.stem, "name": p.stem, "kind": "style", "members": members})
    if not sectors:
        return unavailable("没有板块文件，请在通达信里下载板块或盘后数据")
    return 200, {"sectors": sectors}


# --- 7709 -------------------------------------------------------------------

_tcp = None


def tcp_client():
    global _tcp
    if _tcp is not None:
        return _tcp
    from mootdx.quotes import Quotes

    _tcp = Quotes.factory(market="std", bestip=True, timeout=8)
    return _tcp


def to_symbol(code: str, market: int) -> str:
    code = str(code).zfill(6)
    if market == 1:
        return code + ".SH"
    if str(code).startswith("8") or str(code).startswith("4") or str(code).startswith("9"):
        return code + ".BJ"
    return code + ".SZ"


def tcp_snapshots(symbols: list[str]):
    client = tcp_client()
    codes = []
    if symbols:
        codes = [s.split(".")[0] for s in symbols if "." in s]
    else:
        # 全市场走证券列表再分批。
        secs = tcp_securities_items()
        codes = [x["symbol"].split(".")[0] for x in secs]
    items = []
    for i in range(0, len(codes), 80):
        df = client.quotes(symbol=codes[i : i + 80])
        if df is None or getattr(df, "empty", True):
            continue
        for _, row in df.iterrows():
            code = str(row.get("code", row.get("symbol", "")))
            market = int(row.get("market", 1 if code.startswith("6") else 0))
            last = float(row.get("price", 0) or 0)
            if last <= 0:
                continue
            t = datetime.now(SHANGHAI)
            server_time = str(row.get("servertime", "") or row.get("time", ""))
            if server_time:
                try:
                    t = datetime.strptime(server_time[:8], "%H:%M:%S").replace(
                        year=t.year, month=t.month, day=t.day, tzinfo=SHANGHAI
                    )
                except ValueError:
                    pass
            vol = int(float(row.get("vol", 0) or 0)) * 100
            items.append(
                {
                    "symbol": to_symbol(code, market),
                    "time": iso(t),
                    "pre_close": float(row.get("last_close", 0) or 0),
                    "open": float(row.get("open", 0) or 0),
                    "high": float(row.get("high", 0) or 0),
                    "low": float(row.get("low", 0) or 0),
                    "last": last,
                    "volume": vol,
                    "amount": float(row.get("amount", 0) or 0),
                    "bid": [
                        [float(row.get(f"bid{j}", 0) or 0), float(row.get(f"bid_vol{j}", 0) or 0) * 100]
                        for j in range(1, 6)
                    ],
                    "ask": [
                        [float(row.get(f"ask{j}", 0) or 0), float(row.get(f"ask_vol{j}", 0) or 0) * 100]
                        for j in range(1, 6)
                    ],
                }
            )
    return 200, {"items": items}


def tcp_securities_items() -> list[dict]:
    client = tcp_client()
    items = []
    for market, suffix in ((1, "SH"), (0, "SZ")):
        start = 0
        while True:
            df = client.stocks(market=market)
            if df is None or getattr(df, "empty", True):
                break
            for _, row in df.iterrows():
                code = str(row.get("code", "")).zfill(6)
                name = str(row.get("name", "") or row.get("code", ""))
                items.append({"symbol": f"{code}.{suffix}", "name": name, "market": suffix})
            break
    return items


def tcp_securities():
    items = tcp_securities_items()
    if len(items) < 100:
        return unavailable("7709 证券列表过短，主站可能不可用")
    return 200, {"items": items}


def tcp_bars(body: dict):
    client = tcp_client()
    freq = body.get("freq") or "1d"
    start = body.get("start") or ""
    end = body.get("end") or start
    symbols = body.get("symbols") or []
    if not symbols:
        symbols = [x["symbol"] for x in tcp_securities_items()]
    frequency = 9 if freq == "1d" else (0 if freq == "1m" else 1)
    bars = []
    for s in symbols:
        code = s.split(".")[0]
        df = client.bars(symbol=code, frequency=frequency, offset=800)
        if df is None or getattr(df, "empty", True):
            continue
        for _, row in df.iterrows():
            dt = row.get("datetime") or row.get("date")
            if hasattr(dt, "to_pydatetime"):
                t = dt.to_pydatetime()
            else:
                t = datetime.fromisoformat(str(dt)[:19])
            if t.tzinfo is None:
                t = t.replace(tzinfo=SHANGHAI)
            if freq != "1d":
                t = t - timedelta(minutes=1)
            ymd = t.strftime("%Y-%m-%d")
            if start and ymd < start:
                continue
            if end and ymd > end:
                continue
            bars.append(
                {
                    "symbol": s,
                    "time": iso(t.replace(hour=0, minute=0, second=0) if freq == "1d" else t),
                    "freq": freq,
                    "open": float(row.get("open", 0) or 0),
                    "high": float(row.get("high", 0) or 0),
                    "low": float(row.get("low", 0) or 0),
                    "close": float(row.get("close", 0) or 0),
                    "volume": int(float(row.get("vol", 0) or 0)) * 100,
                    "amount": float(row.get("amount", 0) or 0),
                }
            )
    last = max((b["time"][:10] for b in bars), default="")
    expect = body.get("expect") or ""
    return 200, {"bars": bars, "last_date": last, "stale": bool(expect) and last < expect}


def tcp_adj(body: dict):
    client = tcp_client()
    day = body.get("date") or ""
    symbols = body.get("symbols") or []
    items = []
    for s in symbols:
        code = s.split(".")[0]
        df = client.xdxr(symbol=code)
        if df is None or getattr(df, "empty", True):
            continue
        factor = 1.0
        for _, row in df.iterrows():
            dt = str(row.get("date", row.get("datetime", "")))[:10]
            if day and dt > day:
                break
            peigu = float(row.get("peigu", 0) or 0)
            songgu = float(row.get("songzhuangu", 0) or row.get("songgu", 0) or 0)
            if peigu or songgu:
                factor *= (10 + songgu + peigu) / 10.0
        items.append({"symbol": s, "date": day, "factor": factor})
    return 200, {"items": items}


# --- 官方本地接口 -----------------------------------------------------------

def local_reason() -> str:
    plugin = TDX_DIR / "PYPlugins"
    if not plugin.is_dir():
        return "安装目录没有 PYPlugins，正式版通达信没有官方 Python 接口"
    return "未连上已打开的通达信客户端"


def local_snapshot(_body):
    return unavailable(local_reason())


def local_bars(_body):
    return unavailable(local_reason())


def local_securities():
    return unavailable(local_reason())


# --- AkShare ----------------------------------------------------------------

def ak_global():
    import akshare as ak

    items = []
    mapping = [
        ("USDCNY", "美元兑人民币", lambda: ak.fx_spot_quote(symbol="美元人民币")),
        ("HSI", "恒生指数", lambda: ak.stock_hk_index_spot_em()),
        ("IXIC", "纳斯达克", lambda: ak.index_us_stock_sina(symbol=".IXIC") if False else None),
    ]
    try:
        df = ak.stock_us_spot_em()
        if df is not None and not df.empty:
            row = df.iloc[0]
            items.append(
                {
                    "code": "US_SPOT",
                    "name": str(row.get("名称", "美股")),
                    "trade_date": datetime.now(SHANGHAI).strftime("%Y-%m-%d"),
                    "close": float(row.get("最新价", 0) or 0),
                    "pct_chg": float(row.get("涨跌幅", 0) or 0),
                }
            )
    except Exception:
        pass
    try:
        df = ak.fx_spot_quote(symbol="美元人民币")
        if df is not None and not df.empty:
            row = df.iloc[-1]
            items.append(
                {
                    "code": "USDCNH",
                    "name": "美元兑人民币",
                    "trade_date": str(row.get("日期", datetime.now(SHANGHAI).date())),
                    "close": float(row.get("最新价", row.get("卖出价", 0)) or 0),
                    "pct_chg": 0.0,
                }
            )
    except Exception:
        pass
    try:
        df = ak.futures_foreign_commodity_realtime(symbol="NYMEX原油")
        if df is not None and not df.empty:
            row = df.iloc[0]
            items.append(
                {
                    "code": "CL",
                    "name": "WTI原油",
                    "trade_date": datetime.now(SHANGHAI).strftime("%Y-%m-%d"),
                    "close": float(row.get("最新价", 0) or 0),
                    "pct_chg": float(row.get("涨跌幅", 0) or 0),
                }
            )
    except Exception:
        pass
    if len(items) < 1:
        return unavailable("AkShare 外围接口不可用")
    return 200, {"items": items}


def ak_macro():
    import akshare as ak

    items = []
    series = [
        ("CPI_YOY", "CPI同比", "ak.macro_china_cpi_yearly", "%"),
        ("PPI_YOY", "PPI同比", "ak.macro_china_ppi_yearly", "%"),
        ("PMI_MFG", "制造业PMI", "ak.macro_china_pmi_yearly", ""),
    ]
    fetchers = {
        "CPI_YOY": lambda: ak.macro_china_cpi_yearly(),
        "PPI_YOY": lambda: ak.macro_china_ppi_yearly(),
        "PMI_MFG": lambda: ak.macro_china_pmi(),
    }
    names = {"CPI_YOY": "CPI同比", "PPI_YOY": "PPI同比", "PMI_MFG": "制造业PMI"}
    units = {"CPI_YOY": "%", "PPI_YOY": "%", "PMI_MFG": ""}
    for code, fn in fetchers.items():
        try:
            df = fn()
        except Exception:
            continue
        if df is None or getattr(df, "empty", True):
            continue
        row = df.iloc[-1]
        date_col = df.columns[0]
        val_col = df.columns[-1]
        items.append(
            {
                "code": code,
                "name": names[code],
                "date": str(row[date_col])[:10],
                "value": float(row[val_col] or 0),
                "unit": units[code],
            }
        )
    if not items:
        return unavailable("AkShare 宏观接口不可用")
    return 200, {"items": items}


def ak_limit_pool(qs: dict):
    import akshare as ak

    date = (qs.get("date") or [datetime.now(SHANGHAI).strftime("%Y-%m-%d")])[0].replace("-", "")
    pool = (qs.get("pool") or ["up"])[0]
    fn = {
        "up": ak.stock_zt_pool_em,
        "down": ak.stock_zt_pool_dtgc_em,
        "broken": ak.stock_zt_pool_zbgc_em,
    }.get(pool)
    if fn is None:
        return 400, {"error": "unknown pool"}
    try:
        df = fn(date=date)
    except TypeError:
        df = fn()
    except Exception as e:
        return unavailable(str(e))
    items = []
    if df is None or getattr(df, "empty", True):
        return 200, {"items": []}
    for _, row in df.iterrows():
        code = str(row.get("代码", "")).zfill(6)
        if not code.isdigit():
            continue
        mkt = "SH" if code.startswith("6") else ("BJ" if code[0] in "489" else "SZ")
        items.append(
            {
                "symbol": f"{code}.{mkt}",
                "name": str(row.get("名称", "")),
                "close": _f(row.get("最新价")),
                "pct_chg": _f(row.get("涨跌幅")),
                "amount": _f(row.get("成交额")),
                "open_count": int(float(row.get("炸板次数", 0) or 0)),
                "consecutive": int(float(row.get("连板数", row.get("连续涨停天数", 0)) or 0)),
                "reason": str(row.get("涨停原因类别", row.get("所属行业", "")) or ""),
            }
        )
    return 200, {"items": items}


def ak_minute(qs: dict):
    import akshare as ak

    symbol = (qs.get("symbol") or [""])[0]
    freq = (qs.get("freq") or ["1m"])[0]
    start = (qs.get("start") or [""])[0]
    end = (qs.get("end") or [""])[0]
    code = symbol.split(".")[0]
    period = "1" if freq == "1m" else "5"
    try:
        df = ak.stock_zh_a_hist_min_em(symbol=code, period=period, start_date=start, end_date=end)
    except Exception as e:
        return unavailable(str(e))
    bars = []
    if df is None or getattr(df, "empty", True):
        return 200, {"bars": []}
    for _, row in df.iterrows():
        t = datetime.fromisoformat(str(row.iloc[0])[:19]).replace(tzinfo=SHANGHAI)
        bars.append(
            {
                "symbol": symbol,
                "time": iso(t),
                "freq": freq,
                "open": float(row.get("开盘", 0) or 0),
                "high": float(row.get("最高", 0) or 0),
                "low": float(row.get("最低", 0) or 0),
                "close": float(row.get("收盘", 0) or 0),
                "volume": int(float(row.get("成交量", 0) or 0)) * 100,
                "amount": float(row.get("成交额", 0) or 0),
            }
        )
    return 200, {"bars": bars}


def _f(v):
    try:
        if v is None:
            return None
        return float(v)
    except (TypeError, ValueError):
        return None


# --- HTTP -------------------------------------------------------------------

def read_json(handler: BaseHTTPRequestHandler) -> dict:
    n = int(handler.headers.get("Content-Length") or 0)
    if n <= 0:
        return {}
    raw = handler.rfile.read(n)
    if not raw:
        return {}
    return json.loads(raw.decode("utf-8"))


class Handler(BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
        return

    def send(self, status: int, obj):
        body = json_bytes(obj)
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def need(self, role: str) -> bool:
        if role not in ROLES:
            self.send(404, {"error": "role disabled", "role": role})
            return False
        return True

    def do_GET(self):
        try:
            self.handle_get()
        except Exception as e:
            self.send(500, {"error": str(e), "trace": traceback.format_exc()[-800:]})

    def do_POST(self):
        try:
            self.handle_post()
        except Exception as e:
            self.send(500, {"error": str(e), "trace": traceback.format_exc()[-800:]})

    def handle_get(self):
        u = urlparse(self.path)
        qs = parse_qs(u.query)
        if u.path == "/healthz":
            self.send(200, {"status": "ok", "roles": sorted(ROLES), "tdx_dir": str(TDX_DIR)})
            return
        routes = {
            "/file/sectors": ("file", file_sectors),
            "/tcp/securities": ("tcp", tcp_securities),
            "/ak/global": ("akshare", ak_global),
            "/ak/macro": ("akshare", ak_macro),
            "/api/securities": ("local", local_securities),
        }
        if u.path in routes:
            role, fn = routes[u.path]
            if not self.need(role):
                return
            status, obj = fn()
            self.send(status, obj)
            return
        if u.path == "/ak/limit_pool":
            if not self.need("akshare"):
                return
            self.send(*ak_limit_pool(qs))
            return
        if u.path == "/ak/minute":
            if not self.need("akshare"):
                return
            self.send(*ak_minute(qs))
            return
        self.send(404, {"error": "not found"})

    def handle_post(self):
        u = urlparse(self.path)
        body = read_json(self)
        routes = {
            "/api/snapshot": ("local", lambda: local_snapshot(body)),
            "/api/bars": ("local", lambda: local_bars(body)),
            "/file/bars": ("file", lambda: file_bars(body)),
            "/tcp/snapshot": ("tcp", lambda: tcp_snapshots(body.get("symbols") or [])),
            "/tcp/bars": ("tcp", lambda: tcp_bars(body)),
            "/tcp/adj_factor": ("tcp", lambda: tcp_adj(body)),
        }
        if u.path in routes:
            role, fn = routes[u.path]
            if not self.need(role):
                return
            self.send(*fn())
            return
        self.send(404, {"error": "not found"})


def main():
    host, port = LISTEN.split(":")
    httpd = ThreadingHTTPServer((host, int(port)), Handler)
    print(f"pyworker {LISTEN} roles={sorted(ROLES)} tdx={TDX_DIR}", flush=True)
    httpd.serve_forever()


if __name__ == "__main__":
    main()
